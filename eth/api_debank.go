package eth

import (
	"context"
	"fmt"
	"strings"

	ptracer "github.com/Chaintable/pipeline/tracer"
	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"
)

type DebankAPI struct {
	eth *Ethereum
}

func NewDebankAPI(eth *Ethereum) *DebankAPI {
	return &DebankAPI{
		eth: eth,
	}
}

func (api *DebankAPI) DebankBlock(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*ptypes.DebankOutPut, error) {
	block, err := api.eth.APIBackend.BlockByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("block not found")
	}
	if block.NumberU64() == 0 {
		genesis, err := core.ReadGenesis(api.eth.chainDb)
		if err != nil {
			return nil, fmt.Errorf("could not read genesis: %w", err)
		}
		header := util.BuildPilelineBlockHeader(block)
		blockDiff := ptracer.GenesisAllocToStateDiff(genesis.Alloc)
		blockDiff.Hash = header.StateRoot
		blockFile := &ptypes.BlockFile{
			Block:            util.BuildPipelineBlock(block),
			Txs:              make([]ptypes.Transaction, 0),
			Events:           make([]ptypes.Event, 0),
			Traces:           make([]ptypes.Trace, 0),
			ErrorEvents:      make([]ptypes.Event, 0),
			ErrorTraces:      make([]ptypes.Trace, 0),
			StorageContracts: make([]string, 0),
		}
		for addr, account := range genesis.Alloc {
			if len(account.Storage) > 0 {
				blockFile.StorageContracts = append(blockFile.StorageContracts, strings.ToLower(addr.Hex()))
			}
		}
		var stateDiffBytes []byte
		if blockDiff != nil {
			stateDiffBytes, err = util.EncodeToRlp(blockDiff)
			if err != nil {
				log.Error("Failed to encode state diff", "err", err)
				stateDiffBytes = []byte{}
			}
		} else {
			stateDiffBytes = []byte{}
		}

		return &ptypes.DebankOutPut{
			BlockFile:      blockFile,
			Header:         header,
			StateDiff:      hexutil.Bytes(stateDiffBytes),
			ValidationHash: blockFile.Validation().ValidationHash,
		}, nil
	}
	// Prepare base state
	parent, err := api.eth.APIBackend.BlockByHash(ctx, block.ParentHash())
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, fmt.Errorf("parent block %s not found", block.ParentHash().Hex())
	}
	statedb, release, err := api.eth.APIBackend.StateAtBlock(ctx, parent, 128, nil, true, false)
	if err != nil {
		return nil, err
	}
	defer release()

	chainConfig := api.eth.APIBackend.ChainConfig()

	// Handle build-in system contract code upgrades before normal txs, mirroring
	// core.StateProcessor.Process (TryUpdateBuildInSystemContract with atBlockBegin=true).
	systemcontracts.TryUpdateBuildInSystemContract(chainConfig, block.Number(), parent.Time(), block.Time(), statedb, true)

	rpcTracer := ptracer.RPCTracer{}
	hooks := &tracing.Hooks{
		OnTxStart: rpcTracer.OnTxStart,
		OnTxEnd:   rpcTracer.OnTxEnd,
		OnEnter:   rpcTracer.OnEnter,
		OnExit:    rpcTracer.OnExit,
		OnOpcode:  rpcTracer.OnOpcode,
		OnLog:     rpcTracer.OnLog,
	}
	blockCtx := core.NewEVMBlockContext(block.Header(), ethapi.NewChainContext(ctx, api.eth.APIBackend), nil)
	evm := vm.NewEVM(blockCtx, statedb, chainConfig, vm.Config{Tracer: hooks})

	rpcTracer.OnBlockStart(block)

	// EIP-4788 beacon-root and EIP-2935 parent-block-hash system calls run before the normal
	// txs (mirroring core.StateProcessor.Process). ProcessParentBlockHash is NOT Parlia-gated
	// upstream, so it executes on every post-Prague Chiliz block; omitting it would diverge the
	// replayed state root from consensus on every Prague block.
	if beaconRoot := block.BeaconRoot(); beaconRoot != nil {
		core.ProcessBeaconBlockRoot(*beaconRoot, evm)
	}
	if chainConfig.IsPrague(block.Number(), block.Time()) || chainConfig.IsVerkle(block.Number(), block.Time()) {
		core.ProcessParentBlockHash(block.ParentHash(), evm)
	}

	var (
		txs      = block.Transactions()
		signer   = types.MakeSigner(chainConfig, block.Number(), block.Time())
		gp       = new(core.GasPool).AddGas(block.GasLimit())
		usedGas  = new(uint64)
		receipts = make([]*types.Receipt, 0, len(txs))
	)
	posa, isPoSA := api.eth.engine.(consensus.PoSA)
	commonTxs := make([]*types.Transaction, 0, len(txs))
	// usually do have two tx, one for validator set contract, another for system reward contract.
	systemTxs := make([]*types.Transaction, 0, 2)

	for i, tx := range txs {
		if isPoSA {
			isSystemTx, err := posa.IsSystemTransaction(tx, block.Header())
			if err != nil {
				return nil, err
			}
			if isSystemTx {
				systemTxs = append(systemTxs, tx)
				continue
			}
		}
		if chainConfig.IsCancun(block.Number(), block.Time()) {
			if len(systemTxs) > 0 {
				// systemTxs should be always at the end of block.
				return nil, fmt.Errorf("normal tx %d [%v] after systemTx", i, tx.Hash().Hex())
			}
		}

		msg, err := core.TransactionToMessage(tx, signer, blockCtx.BaseFee)
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		statedb.SetTxContext(tx.Hash(), i)
		evm.SetTxContext(core.NewEVMTxContext(msg))

		receipt, err := core.ApplyTransactionWithEVM(msg, gp, statedb, block.Number(), block.Hash(), block.Time(), tx, usedGas, evm)
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		commonTxs = append(commonTxs, tx)
		receipts = append(receipts, receipt)
	}

	// Fail if Shanghai not enabled and len(withdrawals) is non-zero.
	withdrawals := block.Withdrawals()
	if len(withdrawals) > 0 && !chainConfig.IsShanghai(block.Number(), block.Time()) {
		return nil, fmt.Errorf("withdrawals before shanghai")
	}

	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards,
	// system txs). Passing the tracer hooks lets the PoSA engine trace the system transactions
	// natively in the same pass — upstream Finalize threads the tracer through system-tx
	// execution (OnTxStart/OnTxEnd/OnSystemTx*), so the indexer captures system-tx EVM traces
	// without a separate replay on a copied state. Tracing hooks are read-only observers and do
	// not affect the resulting state/root.
	if err := api.eth.engine.Finalize(api.eth.blockchain, block.Header(), statedb, &commonTxs, block.Uncles(), withdrawals, &receipts, &systemTxs, usedGas, hooks); err != nil {
		return nil, err
	}

	root, destructs, accounts, storages, codes, err := statedb.StateDiff(chainConfig.IsEIP158(block.Number()))
	if err != nil {
		return nil, fmt.Errorf("could not get state diff: %w", err)
	}

	if root != block.Header().Root {
		return nil, fmt.Errorf("state root mismatch: expected %s, got %s", block.Header().Root.Hex(), root.Hex())
	}

	parentRoot := parent.Root()

	res := rpcTracer.GetOutPut(parentRoot, root, destructs, accounts, storages, codes)

	return res, nil
}

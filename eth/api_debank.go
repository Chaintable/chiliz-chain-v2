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
	"github.com/ethereum/go-ethereum/core/state"
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

type posaSystemTxReplayer interface {
	ReplaySystemTransactions(
		chain consensus.ChainHeaderReader,
		header *types.Header,
		state *state.StateDB,
		commonTxs []*types.Transaction,
		systemTxs []*types.Transaction,
		usedGasStart uint64,
		hooks *tracing.Hooks,
	) error
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
	statedb, release, err := api.eth.APIBackend.StateAtBlock(ctx, parent, 128, nil, true, false)
	if err != nil {
		return nil, err
	}
	defer release()

	chainConfig := api.eth.APIBackend.ChainConfig()

	// upgrade build-in system contract before normal txs if Feynman is not enabled
	if !chainConfig.IsFeynman(block.Number(), block.Time()) {
		systemcontracts.UpgradeBuildInSystemContract(chainConfig, block.Number(), parent.Time(), block.Time(), statedb)
	}

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
	evm := vm.NewEVM(blockCtx, vm.TxContext{}, statedb, chainConfig, vm.Config{Tracer: hooks})

	rpcTracer.OnBlockStart(block)

	if beaconRoot := block.BeaconRoot(); beaconRoot != nil {
		core.ProcessBeaconBlockRoot(*beaconRoot, evm, statedb)
	}

	var (
		txs      = block.Transactions()
		signer   = types.MakeSigner(chainConfig, block.Number(), block.Time())
		gp       = new(core.GasPool).AddGas(block.GasLimit())
		usedGas  = new(uint64)
		receipts = make([]*types.Receipt, 0, len(txs))
	)
	posa, isPoSA := api.eth.engine.(consensus.PoSA)
	var systemReplayer posaSystemTxReplayer
	if isPoSA {
		var ok bool
		systemReplayer, ok = api.eth.engine.(posaSystemTxReplayer)
		if !ok {
			return nil, fmt.Errorf("PoSA engine %T does not support ReplaySystemTransactions", api.eth.engine)
		}
	}
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
		evm.Reset(core.NewEVMTxContext(msg), statedb)

		receipt, err := core.ApplyTransactionWithEVM(msg, chainConfig, gp, statedb, block.Number(), block.Hash(), block.Time(), tx, usedGas, evm)
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

	// Copy state for synthetic system-tx tracing (does not affect final state/diff).
	// We need this because PoSA finalization executes system messages without a tracer.
	usedGasBeforeFinalize := *usedGas
	var traceStateCopy *state.StateDB
	var traceSystemTxs []*types.Transaction
	if isPoSA && len(systemTxs) > 0 {
		traceStateCopy = statedb.Copy()
		traceSystemTxs = append(traceSystemTxs, systemTxs...)
	}

	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards, system txs).
	if err := api.eth.engine.Finalize(api.eth.blockchain, block.Header(), statedb, &commonTxs, block.Uncles(), withdrawals, &receipts, &systemTxs, usedGas); err != nil {
		return nil, err
	}

	// Replay system transactions on the copied state to generate synthetic EVM traces.
	// This keeps state/root aligned to consensus (Finalize result), while still producing
	// detailed opcode/call traces for systemTx execution.
	if traceStateCopy != nil {
		// System tx is replayed by Parlia consensus path for consistency.
		if err := systemReplayer.ReplaySystemTransactions(api.eth.blockchain, block.Header(), traceStateCopy, commonTxs, traceSystemTxs, usedGasBeforeFinalize, hooks); err != nil {
			return nil, err
		}
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

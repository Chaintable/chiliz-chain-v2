package eth

import (
	"context"
	"fmt"
	"strings"

	ptracer "github.com/Chaintable/pipeline/tracer"
	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/ethereum/go-ethereum/common"
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
	"github.com/holiman/uint256"
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
		txs     = block.Transactions()
		signer  = types.MakeSigner(chainConfig, block.Number(), block.Time())
		gp      = new(core.GasPool).AddGas(block.GasLimit())
		usedGas = new(uint64)

		beforeSystemTx = true
		systemTxSeen   = false
	)
	posa, isPoSA := api.eth.engine.(consensus.PoSA)

	for i, tx := range txs {
		// Enforce Cancun rule: system txs must be at the end of the block.
		if chainConfig.IsCancun(block.Number(), block.Time()) {
			if systemTxSeen {
				if isPoSA {
					isSystem, _ := posa.IsSystemTransaction(tx, block.Header())
					if !isSystem {
						return nil, fmt.Errorf("normal tx %d [%v] after systemTx", i, tx.Hash().Hex())
					}
				}
			}
		}

		// Apply PoSA system-tx pre-processing matching eth/tracers/api.go.
		if isPoSA {
			isSystem, _ := posa.IsSystemTransaction(tx, block.Header())
			if isSystem {
				systemTxSeen = true
				// Before the first system tx, sweep any leftover SystemAddress balance to coinbase.
				if beforeSystemTx {
					balance := statedb.GetBalance(consensus.SystemAddress)
					if balance != nil && balance.Cmp(common.U2560) > 0 {
						statedb.SetBalance(consensus.SystemAddress, uint256.NewInt(0))
						statedb.AddBalance(blockCtx.Coinbase, balance)
					}
					// If Feynman is enabled, upgrade builtins right before system txs.
					if chainConfig.IsFeynman(block.Number(), block.Time()) {
						systemcontracts.UpgradeBuildInSystemContract(chainConfig, block.Number(), parent.Time(), block.Time(), statedb)
					}
					beforeSystemTx = false
				}
				// Tokenomics deposit and Pepper8 mint are reflected as direct balance changes.
				if posa.IsTokenomicsDeposit(tx.To(), tx.Data()) {
					statedb.AddBalance(blockCtx.Coinbase, uint256.MustFromBig(tx.Value()))
				}
				if posa.IsPepper8Block(block.Time(), parent.Time()) {
					statedb.AddBalance(blockCtx.Coinbase, uint256.MustFromBig(posa.GetPepper8MintAmount()))
				}
			}
		}

		msg, err := core.TransactionToMessage(tx, signer, blockCtx.BaseFee)
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		statedb.SetTxContext(tx.Hash(), i)
		evm.Reset(core.NewEVMTxContext(msg), statedb)

		_, err = core.ApplyTransactionWithEVM(msg, chainConfig, gp, statedb, block.Number(), block.Hash(), block.Time(), tx, usedGas, evm)
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

	}

	root, destructs, accounts, storages, codes, err := statedb.StateDiff(chainConfig.IsEIP158(block.Number()))
	if err != nil {
		return nil, fmt.Errorf("could not get state diff: %w", err)
	}

	if root != block.Header().Root {
		return nil, fmt.Errorf("state root mismatch: expected %x, got %x", block.Header().Root, root)
	}

	parentRoot := parent.Root()

	res := rpcTracer.GetOutPut(parentRoot, root, destructs, accounts, storages, codes)

	return res, nil
}

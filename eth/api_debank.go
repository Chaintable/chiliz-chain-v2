package eth

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	ptracer "github.com/Chaintable/pipeline/tracer"
	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/ethereum/go-ethereum/common"
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
	"github.com/ethereum/go-ethereum/params"
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
	if isPoSA && len(systemTxs) > 0 {
		traceStateCopy = statedb.Copy()
	}

	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards, system txs).
	if err := api.eth.engine.Finalize(api.eth.blockchain, block.Header(), statedb, &commonTxs, block.Uncles(), withdrawals, &receipts, &systemTxs, usedGas); err != nil {
		return nil, err
	}

	// Replay system transactions on the copied state to generate synthetic EVM traces.
	// This keeps state/root aligned to consensus (Finalize result), while still producing
	// detailed opcode/call traces for systemTx execution.
	if traceStateCopy != nil {
		traceEVM := vm.NewEVM(blockCtx, vm.TxContext{}, traceStateCopy, chainConfig, vm.Config{Tracer: hooks})
		if err := tracePoSASystemTxs(traceEVM, traceStateCopy, hooks, chainConfig, parent, block, signer, posa, usedGasBeforeFinalize, commonTxs, systemTxs); err != nil {
			return nil, err
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

// tracePoSASystemTxs replays PoSA system transactions on a copied state with hooks enabled.
// It intentionally bypasses GasPool semantics and executes the transaction payload via EVM.Call
// similar to Parlia's internal applyMessage path.
func tracePoSASystemTxs(
	evmenv *vm.EVM,
	statedb *state.StateDB,
	hooks *tracing.Hooks,
	chainConfig *params.ChainConfig,
	parent *types.Block,
	block *types.Block,
	signer types.Signer,
	posa consensus.PoSA,
	usedGasStart uint64,
	commonTxs []*types.Transaction,
	systemTxs []*types.Transaction,
) error {
	// Note: statedb is a copy, safe to mutate.
	var (
		beforeSystemTx = true
		cumulativeGas  = usedGasStart
	)
	for j, tx := range systemTxs {
		// Apply PoSA system-tx pre-processing matching eth/tracers/api.go (trace-only).
		if beforeSystemTx {
			balance := statedb.GetBalance(consensus.SystemAddress)
			if balance != nil && balance.Cmp(common.U2560) > 0 {
				statedb.SetBalance(consensus.SystemAddress, uint256.NewInt(0))
				statedb.AddBalance(evmenv.Context.Coinbase, balance)
			}
			if chainConfig.IsFeynman(block.Number(), block.Time()) {
				systemcontracts.UpgradeBuildInSystemContract(chainConfig, block.Number(), parent.Time(), block.Time(), statedb)
			}
			beforeSystemTx = false
		}
		if posa != nil {
			if posa.IsTokenomicsDeposit(tx.To(), tx.Data()) {
				statedb.AddBalance(evmenv.Context.Coinbase, uint256.MustFromBig(tx.Value()))
			}
			if posa.IsPepper8Block(block.Time(), parent.Time()) {
				statedb.AddBalance(evmenv.Context.Coinbase, uint256.MustFromBig(posa.GetPepper8MintAmount()))
			}
		}

		msg, err := core.TransactionToMessage(tx, signer, evmenv.Context.BaseFee)
		if err != nil {
			return fmt.Errorf("could not build message for system tx %d [%v]: %w", j, tx.Hash().Hex(), err)
		}
		// Parlia applies system txs with a synthetic tx-index based on the common tx count.
		txIndex := len(commonTxs) + j
		statedb.SetTxContext(tx.Hash(), txIndex)
		evmenv.Reset(core.NewEVMTxContext(msg), statedb)

		// Hook-based tracers need explicit tx boundaries with the transaction object.
		if hooks != nil && hooks.OnTxStart != nil {
			vmctx := &tracing.VMContext{
				StateDB:     statedb,
				Coinbase:    evmenv.Context.Coinbase,
				BlockNumber: new(big.Int).Set(evmenv.Context.BlockNumber),
				Time:        evmenv.Context.Time,
				BlockHash:   block.Hash(),
				TxHash:      tx.Hash(),
				TxIndex:     statedb.TxIndex(),
				BaseFee:     evmenv.Context.BaseFee,
				BlobBaseFee: evmenv.Context.BlobBaseFee,
				GasLimit:    evmenv.Context.GasLimit,
				ChainID:     evmenv.ChainConfig().ChainID,
				Random:      evmenv.Context.Random,
				Difficulty:  evmenv.Context.Difficulty,
			}
			hooks.OnTxStart(vmctx, tx, msg.From)
		}

		// Mimic Parlia applyMessage: prepare rules (Cancun), increment nonce, then execute via EVM.Call.
		if chainConfig.IsCancun(block.Number(), block.Time()) {
			rules := evmenv.ChainConfig().Rules(evmenv.Context.BlockNumber, evmenv.Context.Random != nil, evmenv.Context.Time)
			statedb.Prepare(rules, msg.From, evmenv.Context.Coinbase, msg.To, vm.ActivePrecompiles(rules), msg.AccessList)
		}
		statedb.SetNonce(msg.From, statedb.GetNonce(msg.From)+1)

		var (
			gas      = msg.GasLimit
			gasUsed  uint64
			callErr  error
			receipt  *types.Receipt
			postRoot []byte
		)
		if msg.To == nil {
			_, _, leftOverGas, err := evmenv.Create(vm.AccountRef(msg.From), msg.Data, gas, uint256.MustFromBig(msg.Value))
			gasUsed = gas - leftOverGas
			callErr = err
		} else {
			_, leftOverGas, err := evmenv.Call(vm.AccountRef(msg.From), *msg.To, msg.Data, gas, uint256.MustFromBig(msg.Value))
			gasUsed = gas - leftOverGas
			callErr = err
		}
		cumulativeGas += gasUsed

		// Finalise/write changes similarly to consensus path.
		if chainConfig.IsByzantium(block.Number()) {
			statedb.Finalise(true)
		} else {
			postRoot = statedb.IntermediateRoot(chainConfig.IsEIP158(block.Number())).Bytes()
		}

		receipt = &types.Receipt{Type: tx.Type(), PostState: postRoot, CumulativeGasUsed: cumulativeGas}
		if callErr != nil {
			receipt.Status = types.ReceiptStatusFailed
		} else {
			receipt.Status = types.ReceiptStatusSuccessful
		}
		receipt.TxHash = tx.Hash()
		receipt.GasUsed = gasUsed
		receipt.BlockHash = block.Hash()
		receipt.BlockNumber = block.Number()
		receipt.TransactionIndex = uint(statedb.TxIndex())
		receipt.Logs = statedb.GetLogs(tx.Hash(), block.NumberU64(), block.Hash())
		receipt.Bloom = types.CreateBloom(types.Receipts{receipt})

		if hooks != nil && hooks.OnTxEnd != nil {
			hooks.OnTxEnd(receipt, callErr)
		}
		if callErr != nil {
			return fmt.Errorf("could not replay system tx %d [%v]: %w", j, tx.Hash().Hex(), callErr)
		}
	}
	return nil
}

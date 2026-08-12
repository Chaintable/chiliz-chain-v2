package eth

import (
	"context"
	"fmt"
	"strings"

	ptracer "github.com/Chaintable/pipeline/tracer"
	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
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
	statedb, release, err := api.eth.APIBackend.StateAtBlock(ctx, parent, api.eth.BlockChain().TriesInMemory(), nil, true, false)
	if err != nil {
		return nil, err
	}
	defer release()

	chainConfig := api.eth.APIBackend.ChainConfig()

	rpcTracer := ptracer.RPCTracer{}
	hooks := &tracing.Hooks{
		OnTxStart: rpcTracer.OnTxStart,
		OnTxEnd:   rpcTracer.OnTxEnd,
		OnEnter:   rpcTracer.OnEnter,
		OnExit:    rpcTracer.OnExit,
		OnOpcode:  rpcTracer.OnOpcode,
		OnLog:     rpcTracer.OnLog,
	}

	statedb.SetExpectedStateRoot(block.Root())
	rpcTracer.OnBlockStart(block)

	// Drive the replay through the canonical block processor with the tracer attached,
	// matching the production BSC debank fork (Chaintable/bsc-x). Process runs the exact
	// consensus code path — build-in system-contract upgrades, the EIP-4788 beacon-root and
	// EIP-2935 parent-block-hash system calls, the tx loop, and the PoSA Finalize with native
	// system-tx tracing — so the replayed state root always matches consensus and every
	// current/future hardfork system call is handled automatically, with no hand-rolled tx
	// loop to keep in sync with upstream.
	if _, err = api.eth.BlockChain().Processor().Process(block, statedb, vm.Config{Tracer: hooks, HistoricalStateReplay: true}); err != nil {
		return nil, fmt.Errorf("could not process block: %w", err)
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

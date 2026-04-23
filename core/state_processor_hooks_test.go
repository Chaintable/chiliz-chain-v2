package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func TestTxEndHookErr(t *testing.T) {
	vmErr := errors.New("vm failure")
	outerErr := errors.New("outer failure")

	t.Run("preserves outer error", func(t *testing.T) {
		hooks := &tracing.Hooks{}
		hooks.CaptureTxStart(1)

		got := txEndHookErr(hooks, &ExecutionResult{Err: vmErr}, outerErr, nil, nil, nil, common.Hash{})
		if !errors.Is(got, outerErr) {
			t.Fatalf("expected outer error, got %v", got)
		}
	})

	t.Run("surfaces vm error when top call never started", func(t *testing.T) {
		hooks := &tracing.Hooks{}
		hooks.CaptureTxStart(1)

		got := txEndHookErr(hooks, &ExecutionResult{Err: vmErr}, nil, nil, nil, nil, common.Hash{})
		if !errors.Is(got, vmErr) {
			t.Fatalf("expected vm error, got %v", got)
		}
	})

	t.Run("keeps nil error after top call starts", func(t *testing.T) {
		hooks := &tracing.Hooks{}
		hooks.CaptureTxStart(1)
		hooks.CaptureStart(nil, common.Address{}, common.Address{}, false, nil, 21000, big.NewInt(0))

		got := txEndHookErr(hooks, &ExecutionResult{Err: vmErr}, nil, nil, nil, nil, common.Hash{})
		if got != nil {
			t.Fatalf("expected nil error, got %v", got)
		}
	})
}

func TestApplyTransactionWithEVMHookLogIndicesAreBlockGlobal(t *testing.T) {
	var (
		config       = params.TestChainConfig
		key, _       = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		sender       = crypto.PubkeyToAddress(key.PublicKey)
		contractAddr = common.HexToAddress("0x1000000000000000000000000000000000000001")
		header       = &types.Header{
			Number:     big.NewInt(1),
			GasLimit:   1_000_000,
			Time:       1,
			Difficulty: common.Big1,
			BaseFee:    big.NewInt(0),
		}
		logCode  = common.FromHex("0x60006000a000")
		logIndex []uint
	)

	statedb, err := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatalf("failed to create state db: %v", err)
	}
	statedb.CreateAccount(sender)
	statedb.AddBalance(sender, uint256.NewInt(1_000_000_000_000_000))
	statedb.CreateAccount(contractAddr)
	statedb.SetCode(contractAddr, logCode)

	hooks := &tracing.Hooks{
		OnLog: func(log *types.Log) {
			logIndex = append(logIndex, log.Index)
		},
	}
	blockCtx := NewEVMBlockContext(header, nil, &common.Address{})
	evm := vm.NewEVM(blockCtx, vm.TxContext{}, statedb, config, vm.Config{Tracer: hooks})
	gp := new(GasPool).AddGas(header.GasLimit)
	usedGas := new(uint64)
	signer := types.MakeSigner(config, header.Number, header.Time)

	makeTx := func(nonce uint64) *types.Transaction {
		tx, signErr := types.SignTx(types.NewTransaction(nonce, contractAddr, big.NewInt(0), 100000, big.NewInt(1), nil), signer, key)
		if signErr != nil {
			t.Fatalf("failed to sign tx %d: %v", nonce, signErr)
		}
		return tx
	}
	applyTx := func(index int, tx *types.Transaction) *types.Receipt {
		msg, msgErr := TransactionToMessage(tx, signer, header.BaseFee)
		if msgErr != nil {
			t.Fatalf("failed to build message for tx %d: %v", index, msgErr)
		}
		statedb.SetTxContext(tx.Hash(), index)
		evm.Reset(NewEVMTxContext(msg), statedb)
		receipt, applyErr := ApplyTransactionWithEVM(msg, config, gp, statedb, header.Number, header.Hash(), header.Time, tx, usedGas, evm)
		if applyErr != nil {
			t.Fatalf("failed to apply tx %d: %v", index, applyErr)
		}
		return receipt
	}

	receipt0 := applyTx(0, makeTx(0))
	receipt1 := applyTx(1, makeTx(1))

	if len(logIndex) != 2 {
		t.Fatalf("captured %d hook logs, want 2", len(logIndex))
	}
	if logIndex[0] != 0 || logIndex[1] != 1 {
		t.Fatalf("hook log indices = %v, want [0 1]", logIndex)
	}
	if len(receipt0.Logs) != 1 || receipt0.Logs[0].Index != 0 {
		t.Fatalf("receipt0 log index = %v, want 0", receipt0.Logs)
	}
	if len(receipt1.Logs) != 1 || receipt1.Logs[0].Index != 1 {
		t.Fatalf("receipt1 log index = %v, want 1", receipt1.Logs)
	}
}

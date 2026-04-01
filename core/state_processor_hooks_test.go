package core

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
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

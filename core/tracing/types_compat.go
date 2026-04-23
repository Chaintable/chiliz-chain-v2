package tracing

import "github.com/ethereum/go-ethereum/core/vm"

// StateDB is a compatibility alias used by hook-based tracers.
//
// Newer tracer implementations reference core/tracing.StateDB. In this codebase,
// the canonical state interface is vm.StateDB.
type StateDB = vm.StateDB

// BalanceChangeReason is a compatibility type used by some tracers.
// The concrete values are chain- and tracer-specific; tracers may ignore it.
type BalanceChangeReason uint8

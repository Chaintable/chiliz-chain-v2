// Copyright 2024 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package tracing defines hooks for 'live tracing' of block processing and transaction
// execution.
package tracing

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// OpContext provides the context at which the opcode is being executed in.
//
// Note: This is a lightweight subset used by demo/live tracers.
type OpContext interface {
	MemoryData() []byte
	StackData() []uint256.Int
	Caller() common.Address
	Address() common.Address
	CallValue() *uint256.Int
	CallInput() []byte
	ContractCode() []byte
}

// StateDB gives tracers access to the whole state.
//
// Note: This is intentionally minimal; callers can extend by asserting concrete types.
type StateDB interface {
	GetBalance(common.Address) *uint256.Int
	GetNonce(common.Address) uint64
	GetCode(common.Address) []byte
	GetCodeHash(common.Address) common.Hash
	GetState(common.Address, common.Hash) common.Hash
	GetTransientState(common.Address, common.Hash) common.Hash
	Exist(common.Address) bool
	GetRefund() uint64
}

// VMContext provides the context for the EVM execution.
type VMContext struct {
	Coinbase    common.Address
	BlockNumber *big.Int
	Time        uint64
	Random      *common.Hash
	BaseFee     *big.Int
	StateDB     StateDB
}

// BlockEvent is emitted upon tracing an incoming block.
// It contains the block as well as consensus related information.
type BlockEvent struct {
	Block     *types.Block
	Finalized *types.Header
	Safe      *types.Header
}

type (
	// VM events
	TxStartHook = func(vm *VMContext, tx *types.Transaction, from common.Address)
	TxEndHook   = func(receipt *types.Receipt, err error)

	EnterHook  = func(depth int, typ byte, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int)
	ExitHook   = func(depth int, output []byte, gasUsed uint64, err error, reverted bool)
	OpcodeHook = func(pc uint64, op byte, gas, cost uint64, scope OpContext, rData []byte, depth int, err error)
	FaultHook  = func(pc uint64, op byte, gas, cost uint64, scope OpContext, depth int, err error)

	// Chain events
	BlockchainInitHook = func(chainConfig *params.ChainConfig)
	CloseHook          = func()
	BlockStartHook     = func(event BlockEvent)
	BlockEndHook       = func(err error)
	SkippedBlockHook   = func(event BlockEvent)
	GenesisBlockHook   = func(genesis *types.Block, alloc types.GenesisAlloc)

	// State events
	BalanceChangeHook = func(addr common.Address, prev, new *big.Int, reason BalanceChangeReason)
	NonceChangeHook   = func(addr common.Address, prev, new uint64)
	NonceChangeHookV2 = func(addr common.Address, prev, new uint64, reason NonceChangeReason)
	CodeChangeHook    = func(addr common.Address, prevCodeHash common.Hash, prevCode []byte, codeHash common.Hash, code []byte)
	StorageChangeHook = func(addr common.Address, slot common.Hash, prev, new common.Hash)
	LogHook           = func(log *types.Log)
	CommitHook        = func(originRoot common.Hash, root common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, accountsOrigin map[common.Address][]byte, storages map[common.Hash]map[common.Hash][]byte, storagesOrigin map[common.Address]map[common.Hash][]byte, codes map[common.Hash][]byte)
	BlockHashReadHook = func(blockNumber uint64, hash common.Hash)
)

// Hooks contains callbacks invoked by the core at various points in execution.
//
// This is a small compatibility layer to support the pipeline demo.
type Hooks struct {
	// VM events
	OnTxStart TxStartHook
	OnTxEnd   TxEndHook
	OnEnter   EnterHook
	OnExit    ExitHook
	OnOpcode  OpcodeHook
	OnFault   FaultHook

	// Chain events
	OnBlockchainInit BlockchainInitHook
	OnClose          CloseHook
	OnBlockStart     BlockStartHook
	OnBlockEnd       BlockEndHook
	OnSkippedBlock   SkippedBlockHook
	OnGenesisBlock   GenesisBlockHook

	// State events
	OnBalanceChange BalanceChangeHook
	OnNonceChange   NonceChangeHook
	OnNonceChangeV2 NonceChangeHookV2
	OnCodeChange    CodeChangeHook
	OnStorageChange StorageChangeHook
	OnLog           LogHook

	// Custom hook
	OnCommit CommitHook

	// Block hash read
	OnBlockHashRead BlockHashReadHook
}

// BalanceChangeReason is used to indicate the reason for a balance change.
type BalanceChangeReason byte

const (
	BalanceChangeUnspecified BalanceChangeReason = 0

	BalanceDecreaseSelfdestruct     BalanceChangeReason = 13
	BalanceDecreaseSelfdestructBurn BalanceChangeReason = 14
)

// NonceChangeReason is used to indicate the reason for a nonce change.
type NonceChangeReason byte

const (
	NonceChangeUnspecified NonceChangeReason = 0
)

package tracing

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

// BlockEvent is a minimal compatibility wrapper used by hook-based tracers.
type BlockEvent struct {
	Block *types.Block
}

// VMContext contains contextual information about an EVM execution.
// It is used by hook-based tracers which require access to StateDB and block info.
type VMContext struct {
	StateDB     StateDB
	Coinbase    common.Address
	BlockNumber *big.Int
	Time        uint64
	BlockHash   common.Hash
	TxHash      common.Hash
	TxIndex     int
	BaseFee     *big.Int
	BlobBaseFee *big.Int
	GasLimit    uint64
	ChainID     *big.Int
	Random      *common.Hash
	Difficulty  *big.Int
}

// OpContext is a minimal interface exposed to hook-based tracers for opcode-level
// inspection.
type OpContext interface {
	StackData() []uint256.Int
	MemoryData() []byte
	Address() common.Address
}

type scopeOpContext struct {
	scope *vm.ScopeContext
}

func (c scopeOpContext) StackData() []uint256.Int {
	if c.scope == nil || c.scope.Stack == nil {
		return nil
	}
	return c.scope.Stack.Data()
}

func (c scopeOpContext) MemoryData() []byte {
	if c.scope == nil || c.scope.Memory == nil {
		return nil
	}
	return c.scope.Memory.Data()
}

func (c scopeOpContext) Address() common.Address {
	if c.scope == nil || c.scope.Contract == nil {
		return common.Address{}
	}
	return c.scope.Contract.Address()
}

// Hooks provides a hook-style tracer API while implementing vm.EVMLogger for this codebase.
//
// Transaction-level hooks (OnTxStart/OnTxEnd) are invoked by core.ApplyTransactionWithEVM.
type Hooks struct {
	OnTxStart func(env *VMContext, tx *types.Transaction, from common.Address)
	OnTxEnd   func(receipt *types.Receipt, err error)

	OnEnter  func(depth int, typ byte, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int)
	OnExit   func(depth int, output []byte, gasUsed uint64, err error, reverted bool)
	OnOpcode func(pc uint64, opcode byte, gas, cost uint64, scope OpContext, rData []byte, depth int, err error)
	OnLog    func(log *types.Log)

	logIndex      uint
	logFrameStack []uint
	txHasTopCall  bool
}

type logSizeStateDB interface {
	LogSize() uint
}

func (h *Hooks) CaptureTxStart(gasLimit uint64) {
	if h == nil {
		return
	}
	h.txHasTopCall = false
	h.logIndex = 0
}
func (h *Hooks) CaptureTxEnd(restGas uint64)            {}
func (h *Hooks) CaptureSystemTxEnd(intrinsicGas uint64) {}

func (h *Hooks) CaptureStart(env *vm.EVM, from, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	if h == nil {
		return
	}
	h.txHasTopCall = true
	if env != nil {
		if stateDB, ok := env.StateDB.(logSizeStateDB); ok {
			h.logIndex = stateDB.LogSize()
		}
	}
	h.logFrameStack = h.logFrameStack[:0]
	h.pushLogFrame()
	if h.OnEnter != nil {
		typ := byte(vm.CALL)
		if create {
			typ = byte(vm.CREATE)
		}
		h.OnEnter(0, typ, from, to, input, gas, value)
	}
}

func (h *Hooks) CaptureEnd(output []byte, gasUsed uint64, err error) {
	if h == nil {
		return
	}
	h.popLogFrame(err != nil)
	if h.OnExit != nil {
		h.OnExit(0, output, gasUsed, err, errors.Is(err, vm.ErrExecutionReverted))
	}
}

func (h *Hooks) CaptureEnter(typ vm.OpCode, from, to common.Address, input []byte, gas uint64, value *big.Int) {
	if h == nil {
		return
	}
	h.pushLogFrame()
	if h.OnEnter != nil {
		// Depth is supplied on opcode callbacks; for enter/exit we use -1.
		h.OnEnter(-1, byte(typ), from, to, input, gas, value)
	}
}

func (h *Hooks) CaptureExit(output []byte, gasUsed uint64, err error) {
	if h == nil {
		return
	}
	h.popLogFrame(err != nil)
	if h.OnExit != nil {
		h.OnExit(-1, output, gasUsed, err, errors.Is(err, vm.ErrExecutionReverted))
	}
}

func (h *Hooks) pushLogFrame() {
	h.logFrameStack = append(h.logFrameStack, h.logIndex)
}

// TxHasTopCall reports whether the current transaction reached the top-level
// call/create tracer boundary via CaptureStart.
func (h *Hooks) TxHasTopCall() bool {
	if h == nil {
		return false
	}
	return h.txHasTopCall
}

func (h *Hooks) popLogFrame(reverted bool) {
	if len(h.logFrameStack) == 0 {
		return
	}
	start := h.logFrameStack[len(h.logFrameStack)-1]
	h.logFrameStack = h.logFrameStack[:len(h.logFrameStack)-1]
	if reverted {
		h.logIndex = start
	}
}

func (h *Hooks) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
	if h == nil {
		return
	}
	ctx := scopeOpContext{scope: scope}
	if h.OnOpcode != nil {
		h.OnOpcode(pc, byte(op), gas, cost, ctx, rData, depth, err)
	}
	if h.OnLog != nil {
		h.captureLogIfAny(op, ctx)
	}
}

func (h *Hooks) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, depth int, err error) {
	if h == nil {
		return
	}
	if h.OnOpcode != nil {
		h.OnOpcode(pc, byte(op), gas, cost, scopeOpContext{scope: scope}, nil, depth, err)
	}
}

func (h *Hooks) captureLogIfAny(op vm.OpCode, ctx scopeOpContext) {
	if op < vm.LOG0 || op > vm.LOG4 {
		return
	}
	stack := ctx.StackData()
	mem := ctx.MemoryData()
	count := int(op - vm.LOG0)
	if len(stack) < 2+count {
		return
	}
	mstart := stack[len(stack)-1].Uint64()
	msize := stack[len(stack)-2].Uint64()
	data := getMemoryCopyPadded(mem, int64(mstart), int64(msize))

	topics := make([]common.Hash, 0, count)
	for i := 0; i < count; i++ {
		topic := common.Hash(stack[len(stack)-2-(i+1)].Bytes32())
		topics = append(topics, topic)
	}
	l := &types.Log{
		Address: ctx.Address(),
		Topics:  topics,
		Data:    data,
		Index:   h.logIndex,
	}
	h.logIndex++
	h.OnLog(l)
}

func getMemoryCopyPadded(m []byte, offset, size int64) []byte {
	if offset < 0 || size <= 0 {
		return nil
	}

	// Avoid pathological zero-padding during tracing while preserving
	// large in-memory log data.
	const memoryPadLimit = 1024 * 1024
	const maxInt = int64(^uint(0) >> 1)
	if size > maxInt {
		return nil
	}
	end := offset + size
	if end < offset { // int64 overflow
		return nil
	}

	length := int64(len(m))
	if offset >= length {
		if size > memoryPadLimit {
			return nil
		}
		return make([]byte, size)
	}
	if end <= length {
		cpy := make([]byte, size)
		copy(cpy, m[offset:end])
		return cpy
	}
	if end-length > memoryPadLimit {
		return nil
	}
	cpy := make([]byte, size)
	available := length - offset
	if available > 0 {
		copy(cpy, m[offset:offset+available])
	}
	return cpy
}

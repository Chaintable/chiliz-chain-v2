//go:build pipeline

package core

import (
	pipetracer "github.com/Chaintable/pipeline/tracer"
	"github.com/ethereum/go-ethereum/core/tracing"
)

// BuildTraceHooksFromPipelineTracer converts a pipeline tracer into a core tracing
// Hooks bundle.
//
// Kept in package core (instead of core/tracing) to avoid an import cycle:
// pipeline/tracer already imports github.com/ethereum/go-ethereum/core/tracing.
func BuildTraceHooksFromPipelineTracer(t *pipetracer.PipelineTracer) *tracing.Hooks {
	if t == nil {
		return nil
	}
	return &tracing.Hooks{
		OnBlockchainInit: t.OnBlockchainInit,
		OnClose:          t.OnClose,
		OnBlockStart:     t.OnBlockStart,
		OnBlockEnd:       t.OnBlockEnd,
		OnTxStart:        t.OnTxStart,
		OnTxEnd:          t.OnTxEnd,
		OnEnter:          t.OnEnter,
		OnExit:           t.OnExit,
		OnOpcode:         t.OnOpcode,
		OnFault:          t.OnFault,
		OnLog:            t.OnLog,
		OnGenesisBlock:   t.OnGenesisBlock,
		OnCommit:         t.OnCommit,
		OnBlockHashRead:  t.OnBlockHashRead,
	}
}

package stateless

import "github.com/ethereum/go-ethereum/core/types"

// Witness is a placeholder for state witness generation.
//
// NOTE: This is a minimal stub introduced to satisfy imports from the pipeline
// demo changes. Proper witness generation should be ported if/when needed.
type Witness struct{}

// NewWitness creates a witness for the given block header.
// The full implementation depends on state access and is intentionally omitted.
func NewWitness(_ *types.Header, _ any) (*Witness, error) {
	return &Witness{}, nil
}

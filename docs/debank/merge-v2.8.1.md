# Merge upstream v2.8.1 into debank — validation report

## Summary

Bring Chiliz Chain upstream **v2.6.0 → v2.8.1** into `debank`. The increment is a
single wholesale BSC v1.4.14..v1.6.3 rebase (`b4ffff19f`) that brings the
**Cancun + Prague + Pascal** hardforks and go-ethereum's modern native
live-tracing stack (`core/tracing.Hooks`, `state.NewHookedState`, native parlia
tracer threading, `OnSystemCallStart/End`).

**Mandatory upgrade**: mainnet (chainId 88888) activates Cancun on
**2026-06-30 09:00 UTC** and Prague+Pascal 09:30 UTC (`config/embedded/chiliz.json`).
A node not running this code forks off at the activation block.

## Resolution strategy

The DeBank tracesvc layer was hand-built at v2.6.0 to *substitute* for a
live-tracing stack go-ethereum didn't yet have. v2.8.1 provides that stack
natively, so the redundant DeBank wiring is **retired and re-pointed at
upstream's native hooks** (the indexer's `RPCTracer` already targets the native
func-table `tracing.Hooks`).

- `core/tracing/{hooks_compat,types_compat}.go` — **deleted** (upstream now
  declares the same symbols in `package tracing`; keeping the shims is a
  duplicate-declaration compile failure invisible to git merge-tree).
- `core/state_processor.go`, `core/types/{transaction,block}.go` — reset to
  upstream; DeBank's duplicate `ApplyTransactionWithEVM` and the EIP-7702/7685
  stubs are now real upstream implementations.
- `consensus/parlia/parlia.go` — take upstream's native tracer threading.
- `core/state/statedb.go` `StateDiff()` — **re-ported** onto upstream's
  `mutations`/`pendingStorage` model (the old `s.storages`/`s.stateObjectsDirty`
  fields were removed), preserving the `addrHash→slotHash→trimmed-RLP` output and
  the committed-storages semantics of fix `954174e7e`.
- `eth/api_debank.go` `DebankBlock` — **rewritten to drive the replay through the
  canonical `core.StateProcessor.Process(block, statedb, vm.Config{Tracer})`**,
  matching the production BSC debank fork (`Chaintable/bsc-x`). This replaces the
  hand-rolled tx loop + manual system calls, so the replay runs the exact
  consensus code path and every system call (EIP-4788 beacon root, EIP-2935
  parent-block-hash, system-contract upgrades, parlia Finalize + native system-tx
  tracing) is handled automatically — guaranteeing the replayed state root matches
  consensus with no hand-maintained sequence to drift at a future hardfork.
- `core/vm/chiliz.go` — take upstream; the depth-0 suppression of the synthetic
  `0x7005` DeployerProxy call is now native (`evm.Call` skips `captureBegin` for
  that address).
- `go.mod` — upstream KZG-v2 set + gnark v0.18.1 (dropping DeBank's stale
  `replace` pins); re-add only `github.com/Chaintable/pipeline`.
- Dockerfile — upstream `golang:1.25.5-alpine` builder, keeping the `ACCESS_TOKEN`
  ARG + git `insteadOf` + `ENTRYPOINT`.

## Validation

**Build / static**: `go build ./...` clean; `go vet ./core/... ./eth/...` clean;
`core/state` + `core/vm` tests pass. CI build green on both amd64 and arm64
(alpine + CGO).

**Runtime** (backup test writer restored from a recent production snapshot,
synced to head, no production push path attached):

- **Block-execution / sync**: imports the canonical chain cleanly; block hashes
  match the public RPC 20/20 with zero lag; chain config loads the mainnet
  Cancun/Prague/Pascal fork timestamps (fork-capable).
- **`trace_debankBlock` self-validation**: the replay's internal root-mismatch
  guard (`root != block.Header().Root` → error) passes on all sampled current
  blocks → the StateDiff re-port and the `Process()` replay are consensus-correct.
- **`trace_debankBlock` parity vs the live writer (v2.6.0-debank-8)** on the same
  blocks:
  - `validation_hash` **identical** → `block_file` (txs / traces / events) is
    **byte-identical** to the current production output.
  - `storage_contracts` **set-identical** (ordering only).
  - `state_diff` **content-identical** (same state root, same length/prefix); the
    byte ordering is non-deterministic in **both** versions (Go map-iteration
    order over the diff maps — a pre-existing property, confirmed against the live
    writer), and the indexer decodes it order-agnostically.

**Not yet validatable**: the Cancun/Prague/Pascal hardfork execution paths
(EIP-2935 history writes, blob/EIP-4844 txs, EIP-7702 setcode, system-call
traces) cannot be exercised at runtime until mainnet activates the forks on
2026-06-30; they must be re-validated on a node that has run through activation.

## Deployment / cutover

Image-only swap — the merged binary accepts the existing production command
unchanged (verified, incl. `--gcmode=archive --state.scheme=hash`). The writer's
trace output is consumed by the etl sidecar via the `trace_debankBlock` RPC
(unchanged method/namespace), so no consumer-side change is required.

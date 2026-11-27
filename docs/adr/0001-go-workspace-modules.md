# 0001. The Go code is a workspace of modules

- Date: 2026-10-23

## Decision

- The repository root holds a `go.work` file. Each area of the package map is its own module: `proto`, `core`, `plugin`, `testkit`, `cmd/dql`, and later `sources`, `querysources`, `dialects`, `typeplugins`, `emitters`, `runtime/go` and `conformance`.
- Module paths are `github.com/thec1oud/dql/<directory>`.
- Generated protobuf code lives in the `proto` module beside its `.proto` files and is committed. CI regenerates it and fails on any difference.
- Every module targets Go 1.25.
- Hand-written code has 100% statement coverage. `scripts/coverage.sh` runs every module's tests and fails below it; generated `*.pb.go` files are excluded.

## Consequences

- The core, plugins and runtime version independently, so generated code depends only on `runtime/go`, never on the compiler.
- A module may import another only along the package map's boundaries: the core never imports a plugin, and no plugin imports another.
- Code that cannot be reached by a test is removed rather than excluded.

# Repository Guidelines

## Project Structure & Module Organization

This repository is currently design-first. The top-level architecture and system constraints live in [DESIGN.md](/home/liliang/Codes/oss/ROMA/DESIGN.md). Detailed implementation contracts are under [`docs/`](/home/liliang/Codes/oss/ROMA/docs):

* [`docs/domain-schema-spec.md`](/home/liliang/Codes/oss/ROMA/docs/domain-schema-spec.md): artifact and domain contracts
* [`docs/state-machine-spec.md`](/home/liliang/Codes/oss/ROMA/docs/state-machine-spec.md): lifecycle and recovery rules
* [`docs/backend-module-design.md`](/home/liliang/Codes/oss/ROMA/docs/backend-module-design.md): `romad` module boundaries

When code is added, keep Go packages under `internal/` with module-aligned directories such as `internal/domain`, `internal/events`, `internal/store`, `internal/runtime`, and `internal/scheduler`.

## Build, Test, and Development Commands

The repository does not yet contain executable code. Once bootstrapped, prefer these commands:

```bash
go test ./...
go test ./internal/...
go build ./...
```

Use `go test ./...` for full validation, `go test ./internal/...` for package-scoped iteration, and `go build ./...` to catch interface and wiring regressions.

## Coding Style & Naming Conventions

Write Go in standard `gofmt` style with tabs for indentation. Keep package names short and lowercase: `store`, `policy`, `runtime`. Exported types use PascalCase; unexported helpers use camelCase. Interface names should reflect capability, such as `EventStore`, `Supervisor`, or `Broker`.

Name documents and specs in kebab-case, for example `domain-schema-spec.md`. Keep terminology aligned with the existing specs: `ArtifactEnvelope`, `ExecutionPlan`, `FailedRecoverable`.

## Testing Guidelines

Use Go’s built-in `testing` package. Prefer table-driven tests for domain validation, policy rules, and state transitions. Name files `*_test.go` and test functions `TestXxx`. Add focused unit tests for pure logic first, then integration tests for PTY, worktree, and persistence boundaries.

## Commit & Pull Request Guidelines

Git history is not available in this snapshot, so follow a simple convention: imperative, scoped commit messages such as `store: add event append interface` or `docs: refine Curia failure semantics`.

PRs should include:

* a short problem statement
* the design doc or spec touched
* implementation notes and tradeoffs
* test evidence or an explicit “docs only” note

## Architecture Notes

Do not bypass the spec chain. Schema contracts come first, then state machines, then module wiring. In this repository, persistence, policy, and replay semantics are core behavior, not later cleanup work.

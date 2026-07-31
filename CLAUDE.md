# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build, Test, Run

The Makefile sets `GOWORK=off`; reuse it instead of invoking `go` bare so workspace files in parent directories don't pollute the build.

```sh
make build        # builds bin/tagit, bin/tagitd
make test         # GOWORK=off go test -count=1 ./...
make install      # installs the two binaries to $HOME/.local/bin
```

Single-package or single-test iteration (mirror the env the Makefile uses):

```sh
GOWORK=off go test ./internal/runtime/...
GOWORK=off go test -run TestSupervisorStart ./internal/runtime
GOWORK=off go test -race -count=1 ./...   # what CI runs
```

CI (`.github/workflows/ci.yml`) runs `go build ./...` and `go test -race -count=1 ./...` on Ubuntu and macOS. The `-race` flag matters: PTY/runtime/scheduler code spawns goroutines and races usually only show up with it on.

## Big Picture

TagIt is a daemon-first orchestrator that runs interactive coding-agent CLIs (claude, codex, gemini, ...) under a single control plane. Two binaries:

- `cmd/tagitd` — kernel daemon. A 28-line `main.go` that just wires `internal/app.Daemon` with signal handling and an optional `--acp-port`.
- `cmd/tagit` — CLI client. `cmd/tagit/main.go` is a single ~4000-line dispatcher: `run()` switches on the first arg into `runRun`, `runQueue`, `runAgents`, `runDebug`, etc. Most commands talk to `tagitd` via the API server in `internal/api`; some inspection commands open the SQLite store directly.

Source-of-truth rule (from `DESIGN.md` §3 and `docs/backend-module-design.md` §1, §8): **business state lives in the persisted event/store, not in runtime memory.** Allowed in-memory state is limited to runtime handle registries, classifier buffers, scheduler work queues. Anything orchestration-shaped (session/task lifecycle, accepted artifacts, approvals) must round-trip through `internal/store`.

### Module layout under `internal/`

The package layout in `internal/` follows `docs/backend-module-design.md` §10. Dependency direction is **strictly enforced** and acyclic; before adding an import, check the rules in that doc:

- `domain` — value types, enums, schema mirrors. Depended on by everything; depends on nothing.
- `events` — canonical event vocabulary and reducers.
- `store` — SQLite + blob persistence (`memory.go`, `sqlite_events.go`, `file_events.go`). Append-only writes; returns facts, never workflow decisions.
- `artifacts` — envelope parsing, payload validation, schema dispatch. Validates before scheduler consumes.
- `workspace` — git-worktree allocation under `~/.tagit/workspaces/`. Write workspaces never point at the main working tree.
- `policy` — `Broker` evaluates plans/artifacts/runtime events and returns `Decision{allow|warn|require_approval|block}`. Outputs decisions only; **never** transitions state itself.
- `classifier` — PTY byte-stream → semantic events. Side-effect-free. Cannot kill processes or mutate task state.
- `runtime` — PTY spawn, stdin injection, interrupt/terminate, rebind. Manages process truth, not workflow truth. Has unix and windows PTY backends.
- `scheduler` — DAG progression, retry, Curia orchestration, approval suspend/resume. **The only module allowed to author task execution-state transitions.**
- `sessions`, `taskstore`, `queue`, `history`, `artifacts` — service layers above `store`.
- `curia` — multi-agent voting / arbitration engine, including `Augustus` arbitration and reputation tracking.
- `run` — high-level orchestration of `tagit run` modes (`caesar.go` for rage, `senate.go`, `dynamic.go`, `curia_auto.go`, `recover.go`, `graph.go`). `mode.go` defines `RunModeCollab`, `RunModeSenate`, `RunModeRage`.
- `app` — daemon bootstrap (`NewDaemonWithOptions`) wiring all of the above plus `api`, `acpserver`, `gateway`.
- `api`, `acpserver`, `gateway`, `relay`, `replay`, `plans`, `orchestrator`, `agents`, `tagitpath`, `sqliteutil`, `syncdb`.
- `mcpserver` — exposes TagIt to other agents over MCP (`tagit mcp`, stdio). A thin facade over the `api` client built on `github.com/modelcontextprotocol/go-sdk`: every tool is a call the CLI could already make, so the daemon stays the only control plane. `tagit_submit` deliberately has no policy-override field — MCP callers can never bypass the policy broker.
- `memory` — cross-agent persistent memory (CortexDB via agent-go, lexical/no-key); advisory layer wired into `run` (recall→inject→record). Never a source of truth.
- `chatbot` — platform-agnostic @Tag bot core (per-channel repo bindings, dedup, ack, enqueue, progress streaming, `/bind` `/agent` `/mode` `/status` commands). `feishu` (long-connection), `slack` (Socket Mode) and `openclaw` are the adapters; the daemon starts them best-effort when `~/.tagit/{feishu,slack,openclaw}.json` exists. See `docs/feishu-setup.md`.
- `openclaw` — bridges TagIt to every channel OpenClaw carries (WeChat, Telegram, iMessage, …) by being an MCP *client* of `openclaw mcp serve`: `events_wait`/`events_poll` in, `messages_send` out. Note the direction — `mcpserver` makes TagIt an MCP server; this makes it a client. OpenClaw routes have no @mention and no threads, so the per-session binding in `openclaw.json` is the authorization gate and role filtering (`role == "user"`) is what stops the bot reacting to its own replies. See `docs/openclaw-setup.md`.

Forbidden cross-cuts (from §3):
- `store` must not depend on orchestration modules.
- `policy` must not depend on `scheduler`.
- `workspace` must not depend on `runtime`.
- `classifier` must not mutate scheduler state.
- `api` must not bypass scheduler to change task execution state.

### Run modes

`tagit run` picks a mode automatically when omitted: one agent → `rage`, multiple agents → `senate`. The three modes (`internal/run/mode.go`):

- **rage** — single agent, worker/foreman rounds until done.
- **collab** — starter agent scopes work, delegates implement in parallel workspaces, starter synthesizes.
- **senate** — multi-stage voting: agents propose plans → vote on plan → implement → vote on implementations → merge winner.

`Curia` is a separate approval-oriented decision flow producing a `DecisionPack` + `ExecutionPlan`; the scheduler can auto-promote risky multi-agent runs into Curia. `Graph` is a DAG executor exposed only via `tagit debug graph run`.

### Merge-back protocol

Agents emit:

```
TAGIT_MERGE_BACK: direct_merge | <reason>
TAGIT_MERGE_FILE: path/to/changed/file
```

TagIt applies these patches back to the main repository with `git apply --3way`. Conflicts or policy blocks hold for manual review. Don't change this protocol without coordinating with the classifier and the policy broker.

### State and paths

`tagitd` keeps its control-plane state under `$HOME/.tagit`:

- `~/.tagit/tagit.db` (+ `-shm`, `-wal`) — SQLite store.
- `~/.tagit/workspaces/` — per-task isolated git worktrees.
- `~/.tagit/tagitd.log`, `~/.tagit/tagitd.pid` — daemon log and pid file.

Target repositories are **separate** — chosen per command via `tagit run --cwd <repo>` or by running `tagit` from inside the repo. Never mix the two.

## Spec chain

`AGENTS.md` calls this out and it matters: schema (`docs/domain-schema-spec.md`) → state machines (`docs/state-machine-spec.md`) → module wiring (`docs/backend-module-design.md`). Persistence, policy, and replay semantics are core behavior, not later cleanup. When changing a contract, update the spec doc in the same change.

Keep terminology aligned with the specs: `ArtifactEnvelope`, `ExecutionPlan`, `DecisionPack`, `FailedRecoverable`, `RuntimeSemanticEvent`. Interface names should reflect capability (`EventStore`, `Supervisor`, `Broker`).

## Testing notes

- Use Go's `testing` package; prefer table-driven tests for domain validation, policy rules, and state transitions.
- `scheduler` is tested against fakes for `runtime`, `policy`, `workspace`, `artifacts`, `store` — keep it that way; don't reach for real PTYs or git in scheduler unit tests.
- `runtime` and `workspace` tests are integration-style (real PTY, real worktree). They expect a working `git` and a unix-like PTY on the host.
- `classifier` uses golden stream fixtures.

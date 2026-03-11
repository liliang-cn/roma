# Task Plan

## Goal

Continue evolving ROMA from a local prototype into a daemon-first multi-agent orchestrator with:
- PTY-backed runtime supervision
- event-store-centered execution truth
- daemon-managed prompt runs and task-graph relay runs
- inspectable queue/session/artifact/event relationships

## Current State

- `romad` supports daemon API, queue consumption, PTY-backed runtime execution, and graph job execution.
- `roma` supports direct run, submit, graph run, session/artifact/event queries, and `queue inspect`.
- Queue jobs now carry `session_id`, `task_id`, and `artifact_ids`.
- Session/task/event writes now mirror into `.roma/roma.db` in addition to existing file persistence.
- Direct, delegated, and graph runs can now opt into `--continuous` multi-round execution with `--max-rounds`.

## Phases

### Phase 1: Queue and Execution Truth Consolidation
Status: complete

- [x] Make PTY execution real for supported runtimes
- [x] Append runtime/relay/session/artifact events to event store
- [x] Return run metadata from runner to daemon
- [x] Backfill queue jobs with `session_id`, `task_id`, `artifact_ids`
- [x] Improve queue display and filters for graph/direct jobs

### Phase 2: Daemon-First Graph and API Expansion
Status: complete

- [x] Submit graph jobs through daemon queue
- [x] Add first-class task graph submit API payload, not only `graph_file`
- [x] Add queue inspect API tests
- [x] Make all read paths prefer daemon API when healthy

### Phase 3: Scheduler Alignment
Status: complete

- [x] Introduce persisted task records for graph node execution
- [x] Move graph execution from ad hoc runner wiring toward scheduler-owned task graph execution
- [x] Persist task-node level state transitions outside relay executor only
- [x] Add recover/replay flow from event store instead of file inference

### Phase 4: Policy and Guardrails
Status: complete

- [x] Introduce minimum Policy Broker checks before execution
- [x] Add runtime command risk classification hooks
- [x] Record policy decisions into event store

### Phase 5: Gateway Integration
Status: complete

- [x] Emit queue/session approval-friendly notifications from daemon events
- [x] Add remote command audit path into event store
- [x] Connect queue/session inspection with gateway notifications

### Phase 6: Unified Persistence
Status: complete

- [x] Add SQLite mirror backend for session/task/event persistence
- [x] Move CLI/API read paths to prefer SQLite-backed metadata queries
- [x] Move queue/artifact metadata into unified persistence with compatibility fallback
- [x] Add workspace sync/backfill so old file-only metadata is imported before inspection and recovery
- [x] Move recovery/replay and daemon inspection to a single persistent backend

### Phase 7: Recovery and Approval
Status: complete

- [x] Add SQLite-authoritative scheduler recovery snapshots
- [x] Expose recovery inspection via CLI
- [x] Expose pre-flight policy check via CLI
- [x] Convert policy warnings into explicit approval workflow states
- [x] Resume scheduler dispatch from recovered runnable sessions

### Phase 8: Scheduler-Owned Dispatch
Status: complete

- [x] Add opt-in continuous multi-round agent execution for long-running coding tasks
- [x] Move ready-batch planning into scheduler lifecycle/checkpoint logic
- [x] Add concurrent ready-node dispatch for relay graphs
- [x] Move relay execution entrypoints behind scheduler-owned dispatcher instead of run-layer executor loops
- [x] Persist scheduler dispatch leases/checkpoints for resumed graphs

### Phase 9: Deeper Scheduler Control
Status: complete

- [x] Extend approval semantics from queue-level runs to node-level scheduler gates
- [x] Reduce or remove the remaining compatibility dependency on `internal/relay` as the main execution abstraction
- [x] Start introducing workspace-isolated execution hooks for scheduler-dispatched tasks

### Phase 10: Real Workspace Isolation
Status: in_progress

- [x] Replace shared-read scheduler workspace fallback with worktree-backed isolated execution for writable tasks when Git is available, with explicit fallback metadata otherwise
- [x] Persist workspace lifecycle state for recovery and cleanup
- [x] Add explicit workspace cleanup/reclaim operations for orphaned prepared worktrees, not only released ones
- [x] Surface workspace state and cleanup controls through CLI/API inspection paths
- [x] Reconcile node/task approval with resumed leases so approval can continue without queue-level mediation only

### Phase 11: Lease-Integrated Workspace Truth
Status: in_progress

- [x] Attach workspace ownership metadata to scheduler leases
- [x] Use lease-owned workspace metadata instead of status-only heuristics during cleanup/recovery
- [x] Surface lease/workspace linkage inside queue/session inspection

### Phase 12: Lease-Driven Approval Resume
Status: in_progress

- [x] Persist approval-pending task ids inside scheduler leases
- [x] Use lease-owned approval metadata during recovery instead of inferring only from task/session status
- [x] Surface approval-resume readiness inside queue/session inspection

### Phase 13: Lease-Centric Recovery Refinement
Status: in_progress

- [ ] Append pending-approval metadata into scheduler lease events for replay clarity
- [ ] Expose lease-aware recovery snapshots through CLI/API inspection
- [ ] Reduce remaining queue-level approval semantics that duplicate lease-owned recovery truth

## Risks

- PTY behavior differs across coding CLIs; some still assume stronger TTY semantics.
- Current persistence is mirrored across file and SQLite backends, so read-path divergence is still possible.
- Concurrent DAG dispatch now exists for ready batches, and run/graph/recovery entrypoints now execute through `scheduler.Dispatcher`.
- Scheduler leases now persist ownership/checkpoint state in SQLite and are recovered on daemon restart.
- Real worktree isolation now exists only when the working directory is a Git repository; non-Git execution still falls back to shared-read mode.
- Continuous execution currently relies on agent-emitted `ROMA_DONE:` markers and is still coarse-grained.
- Policy approval exists at queue and node level, but resumed approval still depends on queue/session recovery rather than lease-owned approval continuation.

## Next Immediate Steps

1. Append pending-approval metadata into scheduler lease events for replay clarity.
2. Expose lease-aware recovery snapshots through CLI/API inspection.
3. Reduce remaining queue-level approval semantics that duplicate lease-owned recovery truth.

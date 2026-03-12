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
- ROMA's own default state root is now converging on `$HOME/.roma`, while repository-targeted task execution still keeps `--cwd` semantics separate from ROMA home.

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
Status: complete

- [x] Replace shared-read scheduler workspace fallback with worktree-backed isolated execution for writable tasks when Git is available, with explicit fallback metadata otherwise
- [x] Persist workspace lifecycle state for recovery and cleanup
- [x] Add explicit workspace cleanup/reclaim operations for orphaned prepared worktrees, not only released ones
- [x] Surface workspace state and cleanup controls through CLI/API inspection paths
- [x] Reconcile node/task approval with resumed leases so approval can continue without queue-level mediation only
- [x] Add worktree patch capture and merge-back operations through CLI/API inspection paths

### Phase 11: Lease-Integrated Workspace Truth
Status: complete

- [x] Attach workspace ownership metadata to scheduler leases
- [x] Use lease-owned workspace metadata instead of status-only heuristics during cleanup/recovery
- [x] Surface lease/workspace linkage inside queue/session inspection

### Phase 12: Lease-Driven Approval Resume
Status: complete

- [x] Persist approval-pending task ids inside scheduler leases
- [x] Use lease-owned approval metadata during recovery instead of inferring only from task/session status
- [x] Surface approval-resume readiness inside queue/session inspection

### Phase 13: Lease-Centric Recovery Refinement
Status: completed

- [x] Append pending-approval metadata into scheduler lease events for replay clarity
- [x] Expose lease-aware recovery snapshots through CLI/API inspection
- [x] Reduce remaining queue-level approval semantics that duplicate lease-owned recovery truth

### Phase 14: Scheduler Surface Consolidation
Status: in_progress

- [x] Add richer node-level summaries to queue list/status views
- [x] Keep shrinking legacy `internal/relay` usage to compatibility-only boundaries
- [x] Expose scheduler-owned lease summaries directly in top-level status output
- [x] Expose daemon-owned status counters through the local API
- [x] Route direct-run approval gating through scheduler-owned task + lease state
- [x] Route queue-level approval through lease-backed task approvals when node approval is active
- [x] Collapse `internal/relay` into a scheduler compatibility boundary only

### Phase 15: Structured Follow-Up Contracts
Status: complete

- [x] Replace raw `ROMA_DELEGATE` scraping with structured `FollowUpRequest` extraction in report artifacts
- [x] Carry follow-up instruction hints into scheduler node assignments
- [x] Allow shared run/graph execution paths to append scheduler-native follow-up nodes
- [x] Move dynamic follow-up requests behind formal artifact/policy-aware validation primitives

### Phase 16: Policy Depth
Status: in_progress

- [x] Add path-scoped policy rules tied to workspace/effective directories
- [x] Add minimum override ACL semantics so only approved actors can force policy overrides
- [x] Record richer override metadata in policy decision events and queue/session inspection
- [x] Push path-scoped policy checks further into merge/apply boundaries, not only run/dispatch pre-flight
- [x] Add a first action-aware path policy matrix for execution-plan apply

### Phase 17: Curia Minimal
Status: in_progress

- [x] Add formal artifact payloads for `proposal`, `ballot`, `debate_log`, `decision_pack`, and `execution_plan`
- [x] Add scheduler support for `TaskStrategyCuria`
- [x] Execute Curia minimal flow with scatter, blind review, and human-first decision pack generation
- [x] Persist Curia intermediate artifacts alongside the node's primary execution-plan artifact
- [x] Add CLI examples and inspection shortcuts specialized for Curia sessions
- [x] Add first dispute-detection signals and formalize `winning_mode` beyond hard-coded accept-only output
- [x] Surface reviewer weight / reputation truth through Curia session and inspection summaries

### Phase 18: ExecutionPlan Closure
Status: in_progress

- [x] Add `roma plans inspect/apply/rollback`
- [x] Validate changed workspace paths against `execution_plan.expected_files` and `forbidden_paths`
- [x] Add dry-run plan application
- [x] Add reverse-apply rollback for merged worktree patches
- [x] Push `execution_plan` apply/rollback behind daemon API and approval-aware policy gates
- [x] Emit dedicated plan-apply / plan-rollback / apply-rejected events for replay and audit
- [x] Return structured merge/apply conflict and validation details from the plan service
- [x] Make `dry-run` perform real merge preview instead of static path checks only
- [x] Return conflict-context snippets alongside conflict paths during plan preview and apply failures

### Phase 19: Runtime Visibility and Attachability
Status: in_progress

- [x] Add a first-class `roma cancel <job_id>` / daemon queue-cancel path so operators do not have to kill child processes manually
- [x] Refresh running-job timestamps and journald output with daemon heartbeats while a job is still executing
- [x] Simplify CLI entrypoints so top-level help emphasizes `run`, `submit`, `status`, `cancel`, `help`, with `agent` as management and deep inspection under `debug`
- [x] Remove built-in coding-agent registry entries so runtime selection is fully driven by user-provided profiles (`name`, `path`, `args`, default, PTY)
- [x] Add running-job heartbeat updates so `queue.updated_at` and top-level status change while an agent is still executing
- [x] Persist enough running-node runtime metadata for live inspection:
  - current node id
  - workspace path
  - started_at / last_active_at
  - last output timestamp
- [x] Make `queue inspect` and `sessions inspect` surface live execution state for running jobs, not only completed artifacts and final task state
- [x] Add a CLI command to tail one running job without requiring direct foreground execution
- [x] Improve daemon logs so `journalctl --user -u romad -f` shows periodic heartbeats instead of only start/end markers
- [ ] Fix running-session inspection parity so daemon/API and CLI fallback return the same structure while a job is in progress
- [ ] Persist runtime pid and expose it through live inspection
- [x] Emit lightweight progress events while nodes are running instead of only at node completion
- [ ] Add an attach mode beyond polling tail so users can watch one running session without re-printing full inspect payloads
- [x] Make `queue tail` default to structured runtime events, with `--raw` preserving raw stdout chunks
- [x] Add a first-class user-facing session outcome artifact and expose it via `roma result show <session_id>`

## Risks

- PTY behavior differs across coding CLIs; some still assume stronger TTY semantics.
- Current persistence is mirrored across file and SQLite backends, so read-path divergence is still possible.
- Concurrent DAG dispatch now exists for ready batches, and run/graph/recovery entrypoints now execute through `scheduler.Dispatcher`.
- Scheduler leases now persist ownership/checkpoint state in SQLite and are recovered on daemon restart.
- Real worktree isolation now exists only when the working directory is a Git repository; non-Git execution still falls back to shared-read mode.
- Continuous execution currently relies on agent-emitted `ROMA_DONE:` markers and is still coarse-grained.
- Policy merge/apply boundaries still need tightening; current run-time path-scoped checks are stronger, but plan apply is not yet daemon-governed.
- Dynamic follow-up node generation now uses structured report payloads, but follow-up validation is still permissive compared with a future formal command schema.
- Curia minimal is now real, but still human-first and score-lite; there is no Augustus arbitration or automatic dispute engine yet.
- Execution-plan apply now works through daemon API too, but it still needs richer eventing and plan-specific approval inbox UX.
- Running jobs still look stalled from the outside because queue/session inspection mostly updates on node completion; there is no live heartbeat, runtime metadata, or attach/tail path yet.

## Next Immediate Steps

1. Finish Phase 19 by persisting runtime pid and adding an attach mode richer than polling `queue tail`.
2. Tighten running-session parity so daemon/API and CLI fallback expose exactly the same live structure while a job is active.
3. Keep refining Curia arbitration and conflict UX now that running jobs are observable.
4. Extend structured runtime logging beyond `queue tail`, especially for daemon journald summaries and session-level watch flows.
5. Use the improved live surface to continue CLI product simplification around `run`, `submit`, and `cancel`.
6. Keep improving the user-facing outcome layer so `result show` can remain the main exit, not just a thin artifact wrapper.

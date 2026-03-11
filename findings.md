# Findings

## Runtime

- `codex` now executes successfully under real PTY when run by `romad`.
- PTY fallback must recreate `exec.Cmd`; reusing the same command after failed PTY startup causes `exec: already started`.
- `gemini` and some other CLIs remain more sensitive to TTY environment and may still need adapter-specific handling.

## Daemon/API

- Unix socket API works under current permissions.
- `Client.Available()` must probe `/health`; stale `api.json` alone is not reliable.
- `events` are now queryable through daemon API, not only filesystem fallback.
- Inline task graph submission now works through `/submit`; `graph_file` is no longer required for daemon graph jobs.

## Execution Truth

- Direct runs and graph runs now both produce:
  - session record
  - artifact records
  - event timeline
- Queue jobs now link to `session_id`, `task_id`, and `artifact_ids`.
- `queue inspect` is now the best single command to inspect one executed job.
- Queue jobs can now carry structured graph payloads, making daemon execution independent of on-disk graph file paths.
- Graph node execution now also persists dedicated task records under `.roma/tasks`, with node state, agent id, and artifact id.
- Task state transitions are now emitted as explicit `TaskStateChanged` events by scheduler lifecycle control, instead of relay directly mutating stored task state without dedicated transition events.
- `roma replay <session_id>` now reconstructs task progression, artifact linkage, and ordered timeline from the event store only.
- Session history, task records, and event records now also mirror into `.roma/roma.db`, giving ROMA its first unified persistent backend.
- CLI and daemon API now prefer SQLite-backed reads for sessions, tasks, events, replay, and queue inspection metadata.
- Queue metadata and artifact envelopes now also mirror into SQLite, with fallback to file-backed records for older runs that predate the mirror.
- `syncdb` now backfills file-backed sessions, tasks, events, queue records, and artifact envelopes into SQLite before CLI inspection and daemon recovery paths run.
- Recovery inspection is now SQLite-authoritative: `scheduler.RecoverableSessions` rebuilds runnable task views directly from SQLite-backed sessions and tasks.
- Interrupted `Running` task records are now normalized back to `Ready` during daemon startup, giving recovery a stable resumption point in SQLite-backed metadata.
- `scheduler.ResumeRecoverableSessions` now resumes runnable sessions directly from persisted session/task/artifact state when no live queue owner already exists.
- Relay execution now dispatches each ready batch concurrently instead of running every node strictly one-by-one.
- Ready-batch selection is no longer a relay-only concern: scheduler lifecycle now returns ready task records and records `SchedulerCheckpointRecorded` events for each dispatch checkpoint.
- The main run paths (`direct` delegates, `graph`, and recovery resume) now execute through `scheduler.Dispatcher`, not through relay-owned loops in the run layer.
- Scheduler dispatch ownership is now persisted in SQLite via `scheduler_leases`, with `Acquire`, `Renew`, `Release`, and daemon-start recovery of orphaned active leases.

## Policy and Guardrails

- A minimum `policy` package now blocks clearly unsafe pre-flight cases such as `/` as the working directory.
- Policy decisions are emitted as `PolicyDecisionRecorded` events with `actor_type=policy`.
- Runtime launch commands are now classified before execution, giving ROMA a concrete hook point for later command/path enforcement.
- `roma policy check ...` now exposes the pre-flight policy decision directly, which is a useful stepping stone toward an explicit approval workflow.
- Policy warnings for daemon-managed jobs now become enforceable queue/session state:
  - queue status `awaiting_approval`
  - session status `awaiting_approval`
  - explicit human approval/rejection events
- `roma approve <job_id>` and `roma reject <job_id>` now drive the approval workflow through daemon API or SQLite-backed fallback.
- Scheduler nodes can now independently enter `AwaitingApproval` before execution:
  - risky ready tasks are marked at the task-record level
  - `scheduler.Dispatcher` returns `ApprovalPendingError`
  - `roma tasks approve <task_id>` and `roma tasks reject <task_id>` drive node-level approval through daemon API or fallback
- Scheduler-dispatched tasks now pass through a dedicated workspace preparation hook before runtime launch:
  - per-task workspace metadata is written under `.roma/workspaces/<session>/<task>/workspace.json`
  - `WorkspacePrepared` / `WorkspaceReleased` events are emitted
  - direct/write tasks now use detached Git worktrees when the base directory is a Git repository
  - non-Git working directories now persist an explicit fallback reason instead of silently pretending isolation exists
- The main run/recovery/graph execution paths no longer import `internal/relay`; scheduler now owns the shared node-assignment/result types used by the live execution path.
- Queue requests now reserve `session_id` / `task_id` before execution starts, so crash recovery can resume the same session instead of spawning a replacement one.
- Coding-agent execution can now opt into continuous multi-round mode with `--continuous` and `--max-rounds`, and runtime supervision will keep prompting until the agent emits `ROMA_DONE:` or the round budget is exhausted.
- `MemoryStore.ListEvents` was missing `Type` filtering; fixing it removed a hidden testing/runtime inconsistency between memory-backed and SQLite/file-backed event stores.

## Remaining Gaps

- Queue records still do not expose richer node-level state summaries in list view.
- Recovery now resumes through `scheduler.Dispatcher`, and dispatcher leases are persisted, but released/orphaned worktrees are not yet reclaimed automatically.
- Policy remains minimum viable; there is still no path-scoped approval policy or override ACL model.
- Direct non-daemon runs can now enter node-level `awaiting_approval`, but resumption still depends on rerunning dispatch rather than a dedicated inbox/lease handoff.
- `internal/relay` still exists as a compatibility package, but workspace cleanup/reclaim is still missing after task completion or daemon crash.

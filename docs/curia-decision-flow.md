# Curia Decision Flow Demo

This repository now has a deterministic Curia demo path that shows:

1. a Curia node entering dispute handling
2. `proposal`, `ballot`, `debate_log`, `decision_pack`, and `execution_plan` artifacts being produced
3. the final `execution_plan` appearing in the plan approval inbox as `pending_approval`

## What This Demo Covers

The current demo proves the Curia path that exists today:

- multi-senator scatter
- blind review
- dispute detection
- decision pack generation
- execution plan generation
- API-visible approval inbox entry

## What It Does Not Claim

This demo does **not** prove automatic file-conflict detection across two graph nodes.
ROMA does not yet upgrade a graph into Curia just because two nodes touch the same file.

The current Curia demo is instead based on a deterministic disagreement in the Curia review phase.

## Regression Test

The main regression test is:

```bash
go test ./internal/api -run TestServerCuriaDecisionFlowProducesPlanInboxApproval -count=1 -v
```

That test uses a deterministic adapter to force:

- a Curia dispute
- a non-empty `winning_mode`
- a generated `execution_plan`
- a `pending_approval` item from `GET /plans/inbox`

## User-Level Inspection

For a real run, the current Curia inspection path is:

```bash
go run ./cmd/roma graph run --file examples/curia-graph.json
go run ./cmd/roma sessions curia <session_id>
go run ./cmd/roma plans inbox --session <session_id>
```

The Curia summary now shows:

- dispute flags
- critical veto
- top score gap
- selected proposals
- per-proposal scoreboard:
  - raw score
  - weighted score
  - veto count
  - reviewer count

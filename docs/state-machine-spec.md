# State Machine Spec

**Document Status**: Draft  
**Version**: v1  
**Last Updated**: 2026-03-10

This document defines the state-transition semantics for ROMA v1. It covers:

* session lifecycle
* task node lifecycle
* Curia phase flow
* failure classes
* recovery rules

This spec is normative for scheduler, policy broker, event store, and client rendering.

## 1. State Machine Rules

These rules apply to all ROMA state machines:

* every state transition must be appended to the event store
* transitions are driven by explicit events, never implicit in-memory assumptions
* terminal states are immutable except for audit annotations
* recoverable failures must include a resume path
* a parent object cannot reach `Succeeded` while any required child remains non-terminal
* policy blocks always take precedence over scheduler progress

## 2. Common Event Vocabulary

Recommended lifecycle events:

* `SessionCreated`
* `TaskGraphSubmitted`
* `DependenciesSatisfied`
* `AgentStarted`
* `ArtifactValidated`
* `QuorumReached`
* `ReviewCompleted`
* `ArbitrationRequired`
* `DecisionPackAccepted`
* `ApprovalRequested`
* `ApprovalGranted`
* `ApprovalRejected`
* `PolicyBlocked`
* `PolicyReleased`
* `RetryScheduled`
* `RetryExhausted`
* `ExecutionCompleted`
* `ExecutionFailed`
* `Cancelled`
* `Paused`
* `Resumed`

These event names are not yet transport contracts, but they should map directly into persisted event types.

## 3. Session State Machine

## 3.1 States

Allowed session states:

* `Pending`
* `Running`
* `AwaitingApproval`
* `BlockedByPolicy`
* `Paused`
* `Succeeded`
* `FailedRecoverable`
* `FailedTerminal`
* `Cancelled`

## 3.2 State Meanings

* `Pending`: session exists but execution has not begun
* `Running`: at least one runnable task may progress
* `AwaitingApproval`: one or more blocking approvals are outstanding
* `BlockedByPolicy`: policy has prevented progress and requires explicit release or override
* `Paused`: user or system pause has suspended scheduling
* `Succeeded`: all required task nodes completed successfully
* `FailedRecoverable`: session cannot currently progress but can be retried or resumed
* `FailedTerminal`: session is permanently failed
* `Cancelled`: session was explicitly cancelled

## 3.3 Transition Table

### From `Pending`

Allowed transitions:

* `Running` on `TaskGraphSubmitted`
* `Cancelled` on `Cancelled`
* `FailedTerminal` on invalid graph, invalid initial context, or store initialization failure

### From `Running`

Allowed transitions:

* `AwaitingApproval` on any blocking `ApprovalRequested`
* `BlockedByPolicy` on `PolicyBlocked`
* `Paused` on `Paused`
* `FailedRecoverable` on recoverable execution halt with no immediately runnable fallback
* `FailedTerminal` on unrecoverable daemon, workspace, or persistence failure
* `Succeeded` when all required task nodes are `Succeeded`
* `Cancelled` on `Cancelled`

### From `AwaitingApproval`

Allowed transitions:

* `Running` on `ApprovalGranted` when no other approvals remain
* `FailedTerminal` on approval rejection for a required path with no alternative execution path
* `BlockedByPolicy` if approval resolution triggers policy block
* `Cancelled` on `Cancelled`

### From `BlockedByPolicy`

Allowed transitions:

* `Running` on `PolicyReleased`
* `FailedTerminal` on irreversible veto
* `Cancelled` on `Cancelled`

### From `Paused`

Allowed transitions:

* `Running` on `Resumed`
* `Cancelled` on `Cancelled`
* `FailedTerminal` if required runtime resources cannot be reacquired

### From `FailedRecoverable`

Allowed transitions:

* `Running` on `RetryScheduled` or operator resume
* `FailedTerminal` on `RetryExhausted`
* `Cancelled` on `Cancelled`

### Terminal States

`Succeeded`, `FailedTerminal`, and `Cancelled` are terminal.

## 3.4 Session-Level Invariants

* session state is a reduction over task-node states plus scheduler conditions
* session enters `AwaitingApproval` if any blocking approval prevents global progress
* session enters `BlockedByPolicy` if any active policy block prevents global progress
* session may remain `Running` while some nodes are blocked if other nodes can still legally progress

## 4. TaskNode State Machine

## 4.1 States

Allowed task node states:

* `Pending`
* `Ready`
* `Running`
* `AwaitingQuorum`
* `UnderReview`
* `UnderArbitration`
* `AwaitingApproval`
* `BlockedByPolicy`
* `Succeeded`
* `FailedRecoverable`
* `FailedTerminal`
* `Cancelled`

## 4.2 State Meanings

* `Pending`: node exists but dependencies are not yet satisfied
* `Ready`: dependencies are satisfied and node may be scheduled
* `Running`: runtime execution is active
* `AwaitingQuorum`: Curia scatter phase is collecting valid proposals
* `UnderReview`: blind review is in progress
* `UnderArbitration`: dispute resolution and decision generation are active
* `AwaitingApproval`: execution or merge is blocked on human approval
* `BlockedByPolicy`: node is blocked by policy
* `Succeeded`: node completed its declared contract
* `FailedRecoverable`: node failed but remains retryable
* `FailedTerminal`: node can no longer progress
* `Cancelled`: node was explicitly cancelled or cancelled by session shutdown

## 4.3 Generic Transitions

### From `Pending`

Allowed transitions:

* `Ready` on `DependenciesSatisfied`
* `Cancelled` on session cancellation
* `FailedTerminal` if dependency graph is invalid

### From `Ready`

Allowed transitions:

* `Running` for `direct` and `relay` strategies on `AgentStarted`
* `AwaitingQuorum` for `curia` strategy on Curia launch
* `BlockedByPolicy` on pre-flight `PolicyBlocked`
* `Cancelled` on session cancellation

### From `Running`

Allowed transitions:

* `Succeeded` on valid expected outputs plus execution completion
* `AwaitingApproval` on blocking approval request
* `BlockedByPolicy` on runtime policy block
* `FailedRecoverable` on recoverable runtime failure
* `FailedTerminal` on unrecoverable execution or validation failure
* `Cancelled` on cancellation

### From `AwaitingApproval`

Allowed transitions:

* `Running` if approval is for a runtime continuation
* `Succeeded` if approval is the final gate and required outputs are already complete
* `FailedTerminal` on approval rejection without an alternate path
* `BlockedByPolicy` if approval resolution reveals a policy violation
* `Cancelled` on cancellation

### From `BlockedByPolicy`

Allowed transitions:

* `Ready` if block happened before agent start and policy is released
* `Running` if block happened during runtime and continuation is safe
* `FailedTerminal` on irreversible veto
* `Cancelled` on cancellation

### From `FailedRecoverable`

Allowed transitions:

* `Ready` on retry scheduling requiring full restart
* `Running` on in-place resume if runtime supports it
* `FailedTerminal` on retry exhaustion
* `Cancelled` on cancellation

### Terminal States

`Succeeded`, `FailedTerminal`, and `Cancelled` are terminal.

## 4.4 Strategy-Specific Transition Constraints

### Direct

Typical flow:

`Pending -> Ready -> Running -> Succeeded`

Optional branches:

* `Running -> AwaitingApproval`
* `Running -> BlockedByPolicy`
* `Running -> FailedRecoverable`

### Relay

Typical flow:

`Pending -> Ready -> Running -> Succeeded`

Relay-specific rule:

* node may only start when required upstream artifacts are present and valid

### Curia

Typical flow:

`Pending -> Ready -> AwaitingQuorum -> UnderReview -> UnderArbitration -> Running -> Succeeded`

Curia-specific rule:

* a Curia node cannot enter `Running` without an accepted `ExecutionPlan`

## 5. Curia Phase State Machine

Curia phases are modeled separately from the task node so the system can replay debate progress with more detail.

## 5.1 Curia States

Allowed Curia states:

* `Scatter`
* `QuorumMet`
* `BlindReview`
* `DisputeDetection`
* `Arbitration`
* `ExecutionAuthorized`
* `FailedRecoverable`
* `FailedTerminal`

## 5.2 Transitions

### `Scatter`

Allowed transitions:

* `QuorumMet` when enough valid proposals are collected
* `FailedRecoverable` when quorum is not met but retry budget remains
* `FailedTerminal` on repeated quorum failure or invalid Curia setup

### `QuorumMet`

Allowed transitions:

* `BlindReview` immediately after proposal set is frozen

### `BlindReview`

Allowed transitions:

* `DisputeDetection` when sufficient ballots are collected
* `FailedRecoverable` on partial review timeout with retry allowed
* `FailedTerminal` on invalid ballot corpus or unrecoverable review failure

### `DisputeDetection`

Allowed transitions:

* `ExecutionAuthorized` if a proposal wins without arbitration and policy allows
* `Arbitration` if dispute rules require escalation
* `FailedTerminal` if no executable outcome remains

### `Arbitration`

Allowed transitions:

* `ExecutionAuthorized` on valid `DecisionPack` and `ExecutionPlan`
* `FailedRecoverable` on transient arbitrator failure
* `FailedTerminal` on invalid or rejected decision output

### `ExecutionAuthorized`

This phase is complete when:

* `ExecutionPlan` validation succeeds
* required policy checks pass
* approval requirements are surfaced to the task node state machine

## 5.3 Curia Failure Codes

Allowed Curia-specific failure codes:

* `QuorumNotReached`
* `ProposalSchemaInvalid`
* `BlindReviewTimeout`
* `CriticalVetoTriggered`
* `DecisionPackInvalid`
* `ExecutionPlanRejected`
* `PolicyVetoUnrecoverable`

## 6. Failure Classification

## 6.1 Recoverable Failure

A failure is recoverable when all of the following hold:

* input context remains valid
* event store remains consistent
* required workspace can be reused or recreated
* retry policy allows further attempts

Typical examples:

* agent process crash
* PTY disconnect
* transient adapter failure
* senator timeout
* arbitrator unavailable

## 6.2 Terminal Failure

A failure is terminal when any of the following hold:

* policy has issued a final veto
* schema validation failure invalidates the execution path
* persistence invariants are broken
* workspace merge preconditions cannot be satisfied
* retry budget is exhausted with no fallback

Typical examples:

* invalid `ExecutionPlan`
* invalid `DecisionPack`
* unrecoverable store corruption
* forbidden path modification with no override path

## 7. Recovery Rules

## 7.1 Session Recovery

On daemon restart or client reattach:

* rebuild session state from persisted transitions
* reconstruct active task-node states
* rebind live runtime processes if still present
* otherwise transition affected nodes to `FailedRecoverable` unless terminal evidence already exists

## 7.2 Task Recovery

Node recovery must decide between:

* `resume`: continue existing runtime if process and workspace are still valid
* `retry`: restart node from a clean execution boundary
* `fail terminal`: if replayed evidence shows invariants were broken

## 7.3 Curia Recovery

Curia recovery rules:

* valid proposals and ballots are reusable after restart
* anonymous review mapping must remain stable after replay
* arbitration may be retried only if prior attempt produced no accepted decision artifact

## 8. Approval Semantics

Approvals interact with state machines as follows:

* approval requests are always explicit persisted events
* task nodes own approval wait states
* session enters `AwaitingApproval` only when approvals block overall progress
* approval grant resumes from the last safe execution boundary, not from arbitrary in-memory position
* approval rejection is terminal unless scheduler has a defined alternate branch

## 9. Policy Semantics

Policy interaction rules:

* pre-flight policy block prevents `Ready -> Running`
* runtime policy block interrupts execution before state continues
* release from policy block must record whether continuation is safe or requires restart
* policy terminal veto always wins over retry policy

## 10. Event Store Requirements

To support these state machines, the event store must persist:

* transition timestamp
* prior state
* next state
* triggering event type
* triggering actor type
* reason code
* recoverable flag when applicable
* related artifact IDs when applicable

Recommended actor types:

* `system`
* `scheduler`
* `policy`
* `agent`
* `human`

## 11. Derived State Computation

Some states are derived rather than directly authored:

* session `Succeeded` is derived from required node completion
* session `AwaitingApproval` is derived from blocking approvals with no other runnable work
* session `BlockedByPolicy` is derived from active blocking policy conditions with no other runnable work

The implementation should minimize directly writing derived session states without first evaluating task-node conditions.

## 12. Open Items

The following details still need a lower-level implementation spec:

1. exact reason-code enum set for transitions
2. whether `Paused` exists at task-node granularity or session only
3. whether Curia blind review should support partial quorum scoring
4. how long a runtime may remain detached before being marked unrecoverable
5. how merge-conflict resolution feeds back into task-node states

## 13. Immediate Follow-Up

The next implementation document should be a backend module spec covering:

1. scheduler interfaces
2. event append model
3. runtime supervisor boundaries
4. policy hook points
5. workspace lifecycle hooks

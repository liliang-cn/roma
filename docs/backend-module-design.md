# Backend Module Design

**Document Status**: Draft  
**Version**: v1  
**Last Updated**: 2026-03-10

This document defines the backend module boundaries for `romad`. It translates the architecture and state-machine specs into implementable subsystem interfaces.

The goal is to keep `romad` modular, testable, and replay-safe by constraining:

* ownership
* interface boundaries
* dependency direction
* event flow
* recovery responsibility

## 1. Design Constraints

The backend must satisfy the following constraints:

* business truth lives in persisted state, not runtime memory
* module dependencies must remain acyclic
* side effects must be isolated behind explicit interfaces
* policy enforcement must be reachable from both pre-flight and runtime paths
* replay must not require calling external agents

## 2. Module Map

`romad` v1 should be decomposed into the following modules:

* `api`
* `scheduler`
* `runtime`
* `classifier`
* `policy`
* `workspace`
* `store`
* `artifacts`
* `sessions`
* `gateway`

Optional supporting packages:

* `events`
* `domain`
* `adapters`
* `telemetry`

## 3. Dependency Direction

Recommended high-level dependency graph:

```text
api -> scheduler
api -> sessions

scheduler -> domain
scheduler -> store
scheduler -> artifacts
scheduler -> runtime
scheduler -> workspace
scheduler -> policy
scheduler -> events

runtime -> classifier
runtime -> events
runtime -> store

classifier -> domain
classifier -> events

policy -> domain
policy -> store

workspace -> store

artifacts -> domain
artifacts -> store

sessions -> store
sessions -> domain

gateway -> store
gateway -> events
gateway -> domain
```

Rules:

* `store` must not depend on higher-level orchestration modules
* `policy` must not depend on `scheduler`
* `workspace` must not depend on `runtime`
* `classifier` must not mutate scheduler state directly
* `api` should not bypass scheduler to change task execution state
* `gateway` must not bypass scheduler or policy with remote commands

## 4. Core Module Responsibilities

## 4.1 `domain`

Owns:

* value types
* enums
* domain validation helpers
* stable internal representations of schemas and state names

This package mirrors the schema and state-machine specs in code.

## 4.2 `events`

Owns:

* event type definitions
* append payload shapes
* event routing contracts
* event-to-state reduction helpers

This module should define the canonical persisted event vocabulary.

## 4.3 `store`

Owns:

* SQLite persistence
* blob storage
* migrations
* append-only event writes
* indexed reads for sessions, tasks, artifacts, and transitions

The store is not a scheduler. It persists and retrieves facts.

### Primary Interfaces

```go
type EventStore interface {
    AppendEvent(ctx context.Context, event EventRecord) error
    ListEvents(ctx context.Context, filter EventFilter) ([]EventRecord, error)
}

type SessionStore interface {
    CreateSession(ctx context.Context, sess SessionRecord) error
    GetSession(ctx context.Context, sessionID string) (SessionRecord, error)
    ListSessions(ctx context.Context, filter SessionFilter) ([]SessionRecord, error)
    UpdateSessionState(ctx context.Context, update SessionStateUpdate) error
}

type TaskStore interface {
    UpsertTask(ctx context.Context, task TaskRecord) error
    GetTask(ctx context.Context, taskID string) (TaskRecord, error)
    ListTasksBySession(ctx context.Context, sessionID string) ([]TaskRecord, error)
    UpdateTaskState(ctx context.Context, update TaskStateUpdate) error
}

type ArtifactStore interface {
    PutArtifact(ctx context.Context, envelope []byte, meta ArtifactIndexRecord) error
    GetArtifact(ctx context.Context, artifactID string) (ArtifactBlob, error)
    ListArtifacts(ctx context.Context, filter ArtifactFilter) ([]ArtifactIndexRecord, error)
}

type BlobStore interface {
    PutBlob(ctx context.Context, ref string, r io.Reader) error
    OpenBlob(ctx context.Context, ref string) (io.ReadCloser, error)
}
```

### Store Rules

* event append and state update must be transactionally consistent where possible
* artifact index writes and blob writes must expose partial-failure semantics explicitly
* store methods return facts, not derived workflow decisions

## 4.4 `artifacts`

Owns:

* artifact envelope parsing
* payload validation
* checksum verification
* schema dispatch
* artifact persistence coordination

### Primary Interfaces

```go
type Validator interface {
    ValidateEnvelope(ctx context.Context, raw []byte) (EnvelopeValidationResult, error)
    ValidatePayload(ctx context.Context, envelope ArtifactEnvelope) (PayloadValidationResult, error)
}

type Registry interface {
    ResolvePayloadSchema(kind string, schema string) (PayloadValidator, error)
}

type Service interface {
    ValidateAndStore(ctx context.Context, raw []byte) (StoredArtifact, error)
    Get(ctx context.Context, artifactID string) (StoredArtifact, error)
    List(ctx context.Context, filter ArtifactFilter) ([]StoredArtifact, error)
}
```

### Artifacts Rules

* artifact validation happens before scheduler consumes the artifact
* invalid artifacts may be persisted for audit but must be marked non-executable
* only validated execution artifacts may unblock Curia or execution flow

## 4.5 `sessions`

Owns:

* session creation
* task graph registration
* derived session-state rebuilding
* attach and replay metadata

### Primary Interfaces

```go
type Service interface {
    Create(ctx context.Context, req CreateSessionRequest) (SessionRecord, error)
    SubmitTaskGraph(ctx context.Context, sessionID string, graph TaskGraph) error
    Rebuild(ctx context.Context, sessionID string) (SessionSnapshot, error)
    Get(ctx context.Context, sessionID string) (SessionSnapshot, error)
    List(ctx context.Context, filter SessionFilter) ([]SessionSnapshot, error)
}
```

### Sessions Rules

* session state should be reduced from task facts whenever possible
* session service may not start agents directly

## 4.6 `workspace`

Owns:

* workspace allocation
* Git worktree lifecycle
* branch naming
* cleanup policies
* remount and inspection

### Primary Interfaces

```go
type Manager interface {
    PrepareReadWorkspace(ctx context.Context, req ReadWorkspaceRequest) (WorkspaceHandle, error)
    PrepareWriteWorkspace(ctx context.Context, req WriteWorkspaceRequest) (WorkspaceHandle, error)
    Inspect(ctx context.Context, workspaceID string) (WorkspaceStatus, error)
    Cleanup(ctx context.Context, workspaceID string) error
}
```

### Workspace Rules

* workspace creation must record baseline revision
* write workspaces must not point to the main working tree
* cleanup must be policy-aware and failure-tolerant
* merge is not performed by workspace manager implicitly

## 4.7 `policy`

Owns:

* static rule evaluation
* runtime interception decisions
* approval requirement derivation
* veto and release decisions

### Primary Interfaces

```go
type Broker interface {
    CheckPlan(ctx context.Context, plan ExecutionPlan, scope PolicyScope) (Decision, error)
    CheckArtifact(ctx context.Context, art StoredArtifact, scope PolicyScope) (Decision, error)
    CheckRuntimeEvent(ctx context.Context, evt RuntimeSemanticEvent, scope PolicyScope) (Decision, error)
}
```

### Decision Shape

```go
type Decision struct {
    Outcome          string   // allow | warn | require_approval | block
    ReasonCode       string
    HumanMessage     string
    Blocking         bool
    RequiredApprovals []string
}
```

### Policy Rules

* policy outputs decisions, not state transitions
* scheduler is responsible for applying policy decisions to state
* runtime may request immediate interrupt on blocking decisions

## 4.8 `classifier`

Owns:

* PTY byte-stream interpretation
* pattern matching
* semantic event extraction
* confidence labeling

### Primary Interfaces

```go
type Classifier interface {
    Consume(chunk StreamChunk) ([]RuntimeSemanticEvent, error)
    Flush(streamID string) ([]RuntimeSemanticEvent, error)
}
```

### Classifier Rules

* classifier must be side-effect free beyond emitting events
* confidence scoring must be explicit
* classifier cannot directly kill processes or mutate task state

## 4.9 `runtime`

Owns:

* agent process launch
* PTY allocation
* stdin injection
* process interruption and termination
* stream fan-out into classifier and transcript persistence

### Primary Interfaces

```go
type Supervisor interface {
    Start(ctx context.Context, req StartRequest) (RuntimeHandle, error)
    SendInput(ctx context.Context, runtimeID string, input []byte) error
    Interrupt(ctx context.Context, runtimeID string) error
    Terminate(ctx context.Context, runtimeID string) error
    Inspect(ctx context.Context, runtimeID string) (RuntimeStatus, error)
    Rebind(ctx context.Context, runtimeID string) (RuntimeStatus, error)
}
```

### Runtime Rules

* runtime module manages process truth, not workflow truth
* runtime events must be persisted or forwarded before higher-level reactions where practical
* client attach and detach must not affect runtime process liveness

## 4.10 `scheduler`

Owns:

* DAG progression
* node dispatch
* retry orchestration
* Curia orchestration
* approval suspension and resumption
* state transition decisions

The scheduler is the orchestration brain of `romad`.

### Primary Interfaces

```go
type Scheduler interface {
    StartSession(ctx context.Context, sessionID string) error
    PauseSession(ctx context.Context, sessionID string) error
    ResumeSession(ctx context.Context, sessionID string) error
    CancelSession(ctx context.Context, sessionID string) error

    ApproveNode(ctx context.Context, req ApprovalRequest) error
    RejectNode(ctx context.Context, req ApprovalRequest) error
    RetryNode(ctx context.Context, taskID string) error

    OnRuntimeEvent(ctx context.Context, evt RuntimeSemanticEvent) error
    OnRuntimeExit(ctx context.Context, evt RuntimeExitEvent) error
}
```

### Scheduler Rules

* scheduler is the only module allowed to author task execution-state transitions
* scheduler must reduce decisions from artifacts, policy, and runtime events
* scheduler must treat event-store recovery as first-class, not exceptional

## 4.11 `gateway`

Owns:

* event fan-out to remote adapters
* notification summarization
* remote command intake
* delivery retry and dead-letter handling
* endpoint capability enforcement

### Primary Interfaces

```go
type Service interface {
    RegisterEndpoint(ctx context.Context, endpoint GatewayEndpoint) error
    Deliver(ctx context.Context, evt events.Record) error
    SubmitRemoteCommand(ctx context.Context, cmd RemoteCommand) error
}

type Adapter interface {
    Type() string
    Deliver(ctx context.Context, endpoint GatewayEndpoint, notification NotificationEnvelope) error
}
```

### Gateway Rules

* gateway consumes persisted or stream facts and never becomes execution truth
* gateway may summarize events but must preserve audit references
* remote commands must re-enter `romad` through scheduler-facing control paths
* notification failure must not fail task execution

## 4.12 `api`

Owns:

* gRPC or UDS transport
* request authentication where applicable
* streaming subscriptions
* translation between transport DTOs and internal services

### API Rules

* API handlers should remain thin
* APIs should call service-layer interfaces rather than stores directly for workflow actions
* read-only inspection APIs may read from stores directly if they do not alter orchestration truth

## 5. Cross-Module Flows

## 5.1 Direct Execution Flow

1. `api` creates session and submits task graph through `sessions` and `scheduler`
2. `scheduler` evaluates dependencies and marks node `Ready`
3. `scheduler` asks `workspace` for execution workspace
4. `scheduler` asks `runtime` to start agent
5. `runtime` streams output through `classifier`
6. `classifier` emits semantic events
7. `scheduler` consults `policy` on blocking runtime events if needed
8. `artifacts` validates structured outputs
9. `scheduler` updates node and session state through `store`

## 5.2 Curia Flow

1. `scheduler` enters Curia path for target node
2. `workspace` prepares read views or isolated workspaces for senators
3. `runtime` launches parallel agent runs
4. `classifier` and `artifacts` collect proposal artifacts
5. `scheduler` evaluates quorum
6. `scheduler` triggers blind review and collects ballots
7. `scheduler` evaluates dispute rules
8. if needed, `scheduler` launches arbitrator runtime
9. `artifacts` validates `DecisionPack` and `ExecutionPlan`
10. `policy` evaluates execution eligibility
11. `scheduler` moves node to approval or execution

## 5.3 Gateway Flow

1. `gateway` subscribes to event stream or polls persisted events
2. `gateway` filters and summarizes events by endpoint subscription rules
3. `gateway` delivers `NotificationEnvelope` through an adapter
4. remote user action becomes `RemoteCommand`
5. `gateway` authenticates endpoint and command envelope
6. command is forwarded into `romad` control path
7. `scheduler` and `policy` make the final accept or reject decision

## 5.4 Recovery Flow

1. daemon starts
2. `sessions` enumerates in-flight sessions from `store`
3. `scheduler` rebuilds node states from events
4. `runtime` attempts process rebind when possible
5. unrebindable active runtimes become `FailedRecoverable`
6. clients may attach and inspect reconstructed state

## 6. Event Flow

Recommended event pipeline:

```text
runtime byte stream
-> classifier transport events
-> classifier semantic events
-> scheduler reaction
-> policy decision if required
-> state transition events
-> store append
-> subscription fan-out
```

Rules:

* raw stream persistence and semantic event persistence are separate concerns
* semantic event generation should preserve stream offsets or timestamps for audit traceability
* clients subscribe to facts, not to internal mutable objects

## 7. Transaction Boundaries

Recommended transactional boundaries:

* session creation and initial session event append
* task registration and initial task-state append
* task-state transition and matching event append
* artifact index write and validation-status update

Non-atomic but explicit multi-step flows:

* blob write followed by artifact index write
* runtime spawn followed by runtime-start event append
* worktree creation followed by workspace record persistence

When atomicity is not possible, partial-failure states must be represented explicitly and be recoverable.

## 8. In-Memory State

Allowed in-memory state:

* runtime handle registry
* active subscription registry
* scheduler work queue
* classifier stream buffers
* gateway delivery queues

Forbidden as single source of truth:

* session lifecycle status
* task lifecycle status
* accepted execution artifacts
* approval history

## 9. Error Ownership

Each module must own its own failure classification.

Examples:

* `runtime` classifies spawn failure, PTY failure, exit status
* `artifacts` classifies schema and checksum failure
* `policy` classifies allow, warn, approval, block
* `workspace` classifies worktree create, inspect, cleanup, merge-precondition failures
* `scheduler` classifies retryable versus terminal orchestration failures
* `gateway` classifies delivery failure, retry exhaustion, and dead-letter conditions

The scheduler remains responsible for converting module errors into state transitions.

## 10. Recommended Package Layout

One possible Go layout:

```text
internal/
  api/
  artifacts/
  classifier/
  domain/
  events/
  gateway/
  policy/
  runtime/
  scheduler/
  sessions/
  store/
  workspace/
```

If adapters are added:

```text
internal/adapters/
  claude/
  gemini/
  copilot/
```

## 11. Testing Strategy

Testing should follow the module boundaries:

* `domain`: pure validation tests
* `events`: reducer and compatibility tests
* `store`: migration and persistence tests
* `artifacts`: schema validation and checksum tests
* `workspace`: worktree integration tests
* `policy`: rule table tests
* `classifier`: golden stream fixtures
* `runtime`: PTY lifecycle integration tests
* `scheduler`: state-machine and orchestration tests with fake dependencies
* `gateway`: adapter contract, retry, deduplication, and command-auth tests

The scheduler should be tested against fakes for `runtime`, `policy`, `workspace`, `artifacts`, and `store`.

## 12. Build Order

Recommended implementation order:

1. `domain`
2. `events`
3. `store`
4. `artifacts`
5. `workspace`
6. `policy`
7. `runtime`
8. `scheduler`
9. `gateway`
10. `api`

This order minimizes rework because orchestration logic is built last against stable contracts.

## 13. Open Items

The following still need lower-level implementation decisions:

1. exact reducer model for deriving session state from task state
2. whether `runtime` persists raw PTY stream directly or via store-owned writer
3. whether `artifacts` owns attachment blob writes or delegates to store fully
4. whether `policy` is configured statically, via file, or via persisted rules
5. whether Curia orchestration should live inside scheduler or in a dedicated subservice
6. whether gateway consumes the live event bus, the event store, or both

## 14. Immediate Follow-Up

The next useful step after this document is to create:

1. Go interface files under `internal/...`
2. event and domain type definitions
3. a minimal `romad` bootstrap path for `CreateSession -> Direct Task -> Replay`

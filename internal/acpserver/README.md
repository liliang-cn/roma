# acpserver — ACP Integration

This package exposes ROMA agents and queue jobs over the
[Agent Client Protocol (ACP)](https://agentclientprotocol.com/get-started/architecture)
via a standard HTTP server.

## Current state

`go get github.com/coder/acp-go-sdk` could not be fetched at the time of
writing (network / approval constraint).  The package therefore ships with
**local stub types** that mirror the ACP data model.  Every location that
should delegate to the SDK is marked with a `TODO` comment.

## Intended integration with acp-go-sdk

Once the dependency is available, perform the following steps:

```bash
go get github.com/coder/acp-go-sdk
```

Then in `server.go`:

1. **Remove the stub types** (`AgentInfo`, `Thread`, `Message`,
   `CreateThreadRequest`, `ThreadStatus`) and replace them with the
   equivalent types from `github.com/coder/acp-go-sdk`.

2. **Wire the SDK server helper** — the SDK likely provides an
   `acp.NewServer` or `acp.Handler` that registers the standard ACP routes.
   Replace the manual `http.ServeMux` route registration with that helper,
   passing adapter callbacks that call the existing `listAgents`,
   `createThread`, and `getThread` logic.

3. **Map thread state** — replace `queueStatusToThread` with the SDK's
   canonical status constants if they differ.

## Architecture

```
Remote ACP Client
       │
       │  HTTP  (:8090 by default)
       ▼
 acpserver.Server
       │                  ┌────────────────────┐
       ├── GET  /acp/v1/agents ──▶ agents.Registry.List()
       │                  └────────────────────┘
       │                  ┌────────────────────┐
       ├── POST /acp/v1/threads ──▶ queue.Backend.Enqueue()
       │                  └────────────────────┘
       │                  ┌────────────────────┐
       └── GET  /acp/v1/threads/{id} ──▶ queue.Backend.Get()
                          └────────────────────┘
```

## ACP concepts mapped to ROMA

| ACP concept | ROMA equivalent         |
|-------------|------------------------|
| Agent       | `domain.AgentProfile`  |
| Thread      | `queue.Request`        |
| Message     | Prompt string in queue |

## Configuration

The ACP server is disabled by default (`ACPPort = 0`).  Pass `--acp-port`
to `romad` to enable it:

```bash
romad --acp-port 8090
```

The daemon starts the ACP HTTP listener alongside the Unix-socket API when
`ACPPort > 0`.

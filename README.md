# ROMA: Runtime Orchestrator for Multi-Agents

> "It just fires up all the coding agents at once and points them at the same problem." — someone

> "It not only does nothing, it also burns a lot of tokens." — someone else

## What is ROMA

ROMA treats multi-agent coding like a Roman state, not a chat room.

- **`romad`** is the kernel. It owns the queue, sessions, task states, policy checks, workspaces, artifacts, and recovery.
- **`roma`** is the client. You use it to submit work, inspect progress, approve plans, and debug sessions.
- Agents do not share free-form conversation as system truth. ROMA turns their outputs into structured artifacts and event records.
- Each task runs in an isolated git worktree workspace. Agents work there first, then ROMA merges changes back automatically.

ROMA supports four execution modes:

| Mode | Description |
|------|-------------|
| **Direct** | One agent executes one task |
| **Relay** | Multiple agents as a pipeline; Caesar (starter) coordinates, delegates implement |
| **Curia** | Multi-agent senate; senators propose and review anonymously, ROMA builds a `DecisionPack` + `ExecutionPlan` |
| **Graph** | DAG-based execution with dependencies, supports mixing all strategies |

---

## Install

### One-liner (Linux & macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/liliang-cn/roma/main/install.sh | sh
```

Custom install directory:

```sh
curl -fsSL https://raw.githubusercontent.com/liliang-cn/roma/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

The installer:
- Detects your OS and architecture (linux/darwin × amd64/arm64)
- Uses `go install` if Go ≥ 1.22 is available, otherwise downloads a prebuilt binary from GitHub Releases
- Creates `~/.roma/` (ROMA home directory)
- Verifies the binaries actually run after installation
- Warns if the install directory is not in `PATH`

If `~/.local/bin` is not in your PATH, add it:

```sh
# zsh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc

# bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc && source ~/.bashrc
```

### Build from source

Requires Go ≥ 1.22 and git.

```sh
git clone https://github.com/liliang-cn/roma.git
cd roma
make install        # installs to ~/.local/bin
```

---

## Quick Start

### 1. Register agents

ROMA has no built-in agents. Register whichever CLI coding tools you have installed.
For `claude`, `codex`, and `gemini`, command arguments are filled in automatically:

```sh
roma agent add claude "Claude" $(which claude)
roma agent add codex  "Codex"  $(which codex)
roma agent add gemini "Gemini" $(which gemini)

# confirm
roma agent list
```

### 2. Start the daemon

```sh
roma start
# romad started (pid=12345, log=~/.roma/romad.log)
```

### 3. Run a task

```sh
# single agent — direct mode
roma run --agent claude "add input validation to the user registration handler"

# multi-agent — claude coordinates, codex implements
roma run --agent claude --with codex "refactor the payment module and add unit tests"

# async — submit and get a job ID immediately
roma submit --agent claude "write API documentation for all public endpoints"
```

### 4. Inspect progress

```sh
roma status                        # daemon state + queue summary
roma queue list                    # all jobs
roma queue attach <job_id>         # stream live output
roma result show <session_id>      # final result summary
```

### 5. Stop the daemon

```sh
roma stop
```

---

## Usage Reference

### Daemon management

```sh
roma start [--acp-port <port>]   # start romad in background
roma stop                         # stop romad (SIGTERM, fallback SIGKILL after 10s)
roma status                       # daemon state, queue counts, sqlite stats
```

Logs are written to `~/.roma/romad.log`. PID is stored in `~/.roma/romad.pid`.

### Running tasks

```sh
roma run    --agent <id> [--with <id,...>] "<prompt>"   # run and wait
roma submit --agent <id> [--with <id,...>] "<prompt>"   # submit async, print job_id
```

### Queue management

```sh
roma queue list [--status <status>]   # list jobs
roma queue show <job_id>              # job details as JSON
roma queue attach <job_id>            # stream output in real time
roma approve <job_id>                 # approve a pending job
roma reject  <job_id>                 # reject a pending job
roma cancel  <job_id>                 # cancel a running job
```

### Agent management

```sh
roma agent list
roma agent add <id> <name> <path> [--arg <arg>] [--alias <a>] [--pty] [--mcp] [--json]
roma agent remove <id>
roma agent inspect <id>
```

### Results and history

```sh
roma result show <session_id>
roma debug session list
roma debug session show <session_id>
roma debug task list --session <session_id>
roma debug event list --session <session_id>
roma debug artifact list --session <session_id>
roma debug artifact show <artifact_id>
```

### Graph (DAG) mode

```sh
roma debug graph run --file examples/curia-test.json
```

---

## TUI Mode

Launch the interactive terminal UI:

```sh
roma          # defaults to TUI
roma tui
romatui
```

The TUI starts an embedded `romad` automatically and stops it on exit. Available slash commands:

```
/help                 show help
/status               daemon and queue status
/agent <id>           set active agent
/with <a,b,...>       set delegate agents
/run <prompt>         run task and stream output
/submit <prompt>      submit task asynchronously
/open <job_id>        open job output
/cancel [job_id]      cancel a job
/result [session_id]  show session result
```

---

## How Merge-Back Works

Agents run in isolated git worktrees under `~/.roma/workspaces/`. When an agent finishes and emits:

```
ROMA_MERGE_BACK: direct_merge | <reason>
ROMA_MERGE_FILE: path/to/changed/file
```

ROMA automatically applies the patch back to the main repository using `git apply --3way`. If there are conflicts or policy blocks, the merge is held for manual review.

---

## Development

```sh
make build    # build to bin/
make test     # run full test suite with -race
make install  # install to ~/.local/bin
```

---

## More

- Architecture and design notes: [`DESIGN.md`](./DESIGN.md)
- Agent configuration reference: [`AGENTS.md`](./AGENTS.md)
- Platform runtime notes: [`docs/running-romad.md`](./docs/running-romad.md)

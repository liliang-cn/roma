# TagIt — an open-source, self-hosted Claude Tag

> @mention an AI teammate in your team chat and it does the work — self-hosted, model-agnostic, auditable.

Like Anthropic's **Claude Tag**, TagIt lets you **@mention a coding agent in a group chat**, hand it a task, and get the result back in the thread. Unlike Claude Tag, TagIt is:

- **Open-source & self-hosted** — your code and chat never leave your machine
- **Multi-model** — Claude Code, Codex, or any CLI agent (not one vendor)
- **Multi-platform** — Feishu (飞书) and Slack, no public URL needed (long-connection / Socket Mode)
- **Multiplayer + memory** — one shared agent per channel that remembers past runs in the repo
- **Auditable** — every action is an event in a local store; agents work in isolated `git worktrees`; policy gates

Under the hood: a daemon (`tagitd`) + CLI (`tagit`) that orchestrates one or many coding agents (parallel / vote / worker-foreman) and merges the winning result back via `git apply --3way`.

---

## Install

### Let Claude Code / Codex install it for you

Paste the block below to your agent verbatim. It is written for an AI agent: exact commands, in order, with a check after each step.

```text
Install and set up TagIt (https://github.com/liliang-cn/tagit) on this machine.

1. INSTALL — prefer Homebrew; it ships prebuilt binaries and needs no Go toolchain.
     brew install liliang-cn/tap/tagit
   If `brew` is unavailable, build from source instead (needs Go >= 1.25):
     git clone https://github.com/liliang-cn/tagit.git /tmp/tagit && cd /tmp/tagit && make install
   That installs `tagit` and `tagitd` into ~/.local/bin — make sure that is on PATH.
   Check: `tagit --help` prints usage.

2. REGISTER THE CODING AGENTS THAT EXIST ON THIS MACHINE. For each of claude,
   codex, gemini, run `which <name>` first and skip the ones that are missing:
     tagit agent add claude "Claude" $(which claude)
     tagit agent add codex  "Codex"  $(which codex)
   Check: `tagit agent list` shows them.

3. START THE DAEMON. It owns all state under ~/.tagit (SQLite + git worktrees).
     tagit start
   Under Homebrew you can instead run it at login: `brew services start tagit`.
   Check: `tagit status` reports the daemon and an empty queue.

4. SMOKE TEST from inside any git repository:
     tagit run --agent claude --prompt "list the top-level files and stop"
   Check: it prints a job id, then a result.

Notes: target repos are separate from ~/.tagit — choose one per run with --cwd,
or run `tagit` from inside the repo. Do not commit anything in the target repo
unless I ask. Report the output of `tagit --help` and `tagit agent list` when done.
```

If you want the agent to *use* TagIt rather than just install it, also wire up the MCP server — see [Drive it from another agent (MCP)](#4-drive-it-from-another-agent-mcp).

### Homebrew (macOS/Linux)

```sh
brew install liliang-cn/tap/tagit     # prebuilt binary, no Go toolchain needed
brew services start tagit             # optional: run the daemon on login, auto-restart
```

### From source

```sh
git clone https://github.com/liliang-cn/tagit.git && cd tagit
make install      # → ~/.local/bin/{tagit,tagitd}   (Go ≥ 1.25)
```

---

## Use

### 1. Register the agents you have

```sh
tagit agent add claude "Claude" $(which claude)
tagit agent add codex  "Codex"  $(which codex)
tagit agent list
```

### 2. Run a task from the CLI

```sh
tagit start                                                    # start the daemon
tagit run --agent claude --prompt "add input validation to the signup handler"
tagit run --mode senate --agent codex --with claude --prompt "build X, pick the best implementation"
tagit status                                                   # daemon + queue
tagit stop
```

Modes: **rage** (one agent, worker/foreman rounds — default) · **collab** (delegates in parallel) · **senate** (propose → vote → implement → vote → merge).

### 3. @mention it in chat — the "Tag" experience

Drop bot credentials into `~/.tagit/feishu.json` (or `slack.json`):

```json
{ "app_id": "cli_xxx", "app_secret": "xxx", "bindings": [] }
```

`tagit start`, add the bot to a group, then configure and run **entirely from chat**:

```
@TagIt /bind /path/to/repo      link this channel to a repo
@TagIt /agent codex             set the agent   (also: /mode, /status, /unbind, /help)
@TagIt add input validation to the signup handler
```

It acks (**收到，开始干 🛠️**), works in an isolated `git worktree`, streams progress, and posts **✅ Done** in the thread.

- **Feishu**: a self-built app subscribing `im.message.receive_v1` over **long connection** — no public URL. Full walkthrough: **[docs/feishu-setup.md](docs/feishu-setup.md)**.
- **Slack**: an app in **Socket Mode** (`xapp-` + `xoxb-` tokens) subscribing `app_mention`, with a native `/tagit` slash command (autocompletes — `/tagit bind <repo>`, `/tagit status`, …). `@TagIt /bind …` still works as a fallback. Full walkthrough: **[docs/slack-setup.md](docs/slack-setup.md)**.

### 4. Drive it from another agent (MCP)

`tagit mcp` serves TagIt over the **Model Context Protocol** on stdio, so any MCP client — Claude Code, Codex, OpenClaw, your own agent — can delegate coding work to a TagIt team and watch it finish.

```sh
tagit start                                              # the MCP server talks to the daemon
claude mcp add tagit -- tagit mcp --cwd /path/to/repo     # Claude Code
```

Any client that takes an `mcpServers` config:

```json
{ "mcpServers": { "tagit": { "command": "tagit", "args": ["mcp", "--cwd", "/path/to/repo"] } } }
```

Tools it exposes:

| Tool | What it does |
| --- | --- |
| `tagit_submit` | Enqueue a task against a repo; returns a job id immediately |
| `tagit_job_wait` | Block until the job is terminal (or the timeout expires) |
| `tagit_job_status` · `tagit_job_list` · `tagit_job_cancel` | Inspect one job · list recent jobs · cancel |
| `tagit_result` | Final outcome of a run, by session id or job id |
| `tagit_memory_recall` · `tagit_memory_note` | Read/write the repo's cross-agent memory |

Flags: `--cwd <dir>` sets the repo used when a call omits one · `--read-only` exposes inspection tools only (submit/cancel/note disappear from `tools/list`) · `--no-memory` drops the two memory tools.

Every call goes through the same daemon pipeline as the CLI, so runs stay in the event store and risky work still stops at `awaiting_approval` for a human. `tagit_submit` has **no** policy-override argument: an MCP caller can never bypass the policy broker.

---

State lives in `~/.tagit/` (SQLite + git worktrees). Target repos are separate — pick per run with `--cwd`, or run from inside the repo.

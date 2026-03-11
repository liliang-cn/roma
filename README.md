# ROMA: Runtime Orchestrator for Multi-Agents

> “It just fires up all the coding agents at once and points them at the same problem.” — someone

> “It not only does nothing, it also burns a lot of tokens.” — someone else

## How ROMA Works

ROMA treats multi-agent coding like a Roman state, not a chat room.

- `romad` is the kernel. It owns the queue, sessions, task states, policy checks, workspaces, artifacts, and recovery.
- `roma` is the client. You use it to submit work, inspect progress, approve plans, and debug sessions.
- Agents do not share free-form conversation as system truth. ROMA turns their outputs into structured artifacts and event records.
- Each task runs in an isolated workspace when possible. Agents work there first, then ROMA decides whether a plan can be merged back.

ROMA currently supports three execution styles:

- `Direct`: one agent executes one task.
- `Relay`: multiple agents run as a pipeline, passing artifacts forward.
- `Curia`: multiple agents act as a senate. Senators produce proposals, review each other anonymously, and ROMA builds a `DecisionPack` plus an `ExecutionPlan`.

In the Curia metaphor:

- **Senators** are the proposing and reviewing agents.
- **DebateLog** is the court record of proposals, ballots, disputes, and tradeoffs.
- **Augustus** is the higher-weight arbitrator agent used when the senate cannot converge cleanly.
- **ExecutionPlan** is the only thing that should reach real apply/rollback flow.

The intended user flow is:

1. `roma run ...` or `roma submit ...`
2. `romad` schedules agents and records everything under `.roma/`
3. inspect with `roma queue ...` or `roma debug ...`
4. approve or reject when policy or plan gates require it
5. preview, apply, or roll back the resulting execution plan

## Build

Build the binaries:

```bash
make build
```

This produces:

```text
bin/roma
bin/romad
```

Install them to `~/.local/bin`:

```bash
make install
```

## Test

Run the full test suite:

```bash
make test
```

## Run `romad`

`romad` uses the current working directory as its workspace root and stores runtime state under `.roma/`.
That means there are two separate paths to think about:

- binary path: where `romad` is installed, for example `~/.local/bin/romad`
- workspace root: the directory where `romad` runs and writes `.roma/`

Run it directly:

```bash
./bin/romad
```

Check daemon state:

```bash
./bin/roma status
```

## Linux

A `systemd --user` unit template is included at [`deploy/systemd/romad.service`](./deploy/systemd/romad.service).

Install it:

```bash
make install
mkdir -p ~/.local/share/roma
mkdir -p ~/.config/systemd/user
cp deploy/systemd/romad.service ~/.config/systemd/user/romad.service
systemctl --user daemon-reload
systemctl --user enable --now romad
```

Useful commands:

```bash
systemctl --user status romad
journalctl --user -u romad -f
systemctl --user restart romad
systemctl --user stop romad
```

The unit assumes:

- binary path: `$HOME/.local/bin/romad`
- workspace root: `$HOME/.local/share/roma`

If you want `romad` to own a different workspace root, edit `WorkingDirectory=` in the unit.

## macOS

A `launchd` LaunchAgent template is included at [`deploy/launchd/com.roma.romad.plist`](./deploy/launchd/com.roma.romad.plist).

Install it:

```bash
make install
mkdir -p ~/Library/LaunchAgents
cp deploy/launchd/com.roma.romad.plist ~/Library/LaunchAgents/com.roma.romad.plist
launchctl bootout "gui/$(id -u)/com.roma.romad" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.roma.romad.plist
launchctl enable "gui/$(id -u)/com.roma.romad"
launchctl kickstart -k "gui/$(id -u)/com.roma.romad"
```

Useful commands:

```bash
launchctl print "gui/$(id -u)/com.roma.romad"
launchctl kickstart -k "gui/$(id -u)/com.roma.romad"
launchctl bootout "gui/$(id -u)/com.roma.romad"
```

The LaunchAgent assumes:

- binary path: `$HOME/.local/bin/romad`
- workspace root: `$HOME/.local/share/roma`

## Windows

The simplest default is to run `romad.exe` from the repository root:

```powershell
go build -o bin/romad.exe ./cmd/romad
Set-Location C:\path\to\ROMA
.\bin\romad.exe
```

For background execution, use Task Scheduler first:

- Program: `C:\path\to\ROMA\bin\romad.exe`
- Start in: `C:\path\to\roma-workspace`
- Trigger: `At log on`
- Restart on failure: enabled

If you specifically need a Windows service, wrap `romad.exe` with a service manager such as `nssm`.

## More

Platform-specific runtime notes also live in [`docs/running-romad.md`](./docs/running-romad.md).

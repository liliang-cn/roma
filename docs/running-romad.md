# Running `romad`

`romad` is a local daemon. It stores its own control-plane state under `$HOME/.roma`.

Keep these paths separate:

- binary path: where `romad` is installed, for example `~/.local/bin/romad`
- ROMA home: `$HOME/.roma`
- repository path: the project directory ROMA targets for execution through isolated worktrees

Build the binaries first:

```bash
make build
```

That produces:

```text
bin/roma
bin/romad
```

Install them to `~/.local/bin`:

```bash
make install
```

## Linux (`systemd --user`)

The repository includes a user unit template at `deploy/systemd/romad.service`.

Install it:

```bash
mkdir -p ~/.roma
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

This unit assumes:

- binary path: `$HOME/.local/bin/romad`
- ROMA home: `$HOME/.roma`

If you want to move ROMA's control-plane state, change `WorkingDirectory=` and keep it pointing at a dedicated ROMA home, not at a source repository.

## macOS (`launchd`)

The repository includes a LaunchAgent template at `deploy/launchd/com.roma.romad.plist`.

Install it:

```bash
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

The plist assumes:

- binary path: `$HOME/.local/bin/romad`
- ROMA home: `$HOME/.roma`

## Windows

The simplest way is to run `romad.exe` in a normal terminal:

```powershell
go build -o bin/romad.exe ./cmd/romad
Set-Location C:\path\to\ROMA
C:\path\to\ROMA\bin\romad.exe
```

For background execution, use Task Scheduler instead of a Windows service first.

Suggested Task Scheduler settings:

- Program: `C:\path\to\ROMA\bin\romad.exe`
- Start in: `C:\path\to\ROMA`
- Trigger: `At log on`
- Run whether user is logged on or not: optional
- Restart on failure: enabled

If you specifically want a Windows service, wrap `romad.exe` with a service manager such as `nssm`, but Task Scheduler is the simpler default for the current repository.

## Notes

- `romad` control-plane state lives in `$HOME/.roma`; target repositories are chosen per command via `roma run --cwd <repo>` or by running `roma` from that repository.
- Agent execution should happen in isolated worktrees under the target repository, not directly inside `$HOME/.roma`.
- If you change the binary install path, update the systemd unit or launchd plist path.
- On all platforms, check the daemon health with:

```bash
./bin/roma status
```

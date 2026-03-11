# Running `romad`

`romad` is a local daemon. It uses its current working directory as the workspace root and stores runtime state under `.roma/`.

Keep these two paths separate:

- binary path: where `romad` is installed, for example `~/.local/bin/romad`
- workspace root: the directory where `romad` runs and writes `.roma/`

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

This unit assumes:

- binary path: `$HOME/.local/bin/romad`
- workspace root: `$HOME/.local/share/roma`

If you want to run ROMA against another workspace root, edit `WorkingDirectory=`.

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
- workspace root: `$HOME/.local/share/roma`

## Windows

The simplest way is to run `romad.exe` in a normal terminal from the desired workspace root:

```powershell
go build -o bin/romad.exe ./cmd/romad
Set-Location C:\path\to\roma-workspace
C:\path\to\ROMA\bin\romad.exe
```

For background execution, use Task Scheduler instead of a Windows service first.

Suggested Task Scheduler settings:

- Program: `C:\path\to\ROMA\bin\romad.exe`
- Start in: `C:\path\to\roma-workspace`
- Trigger: `At log on`
- Run whether user is logged on or not: optional
- Restart on failure: enabled

If you specifically want a Windows service, wrap `romad.exe` with a service manager such as `nssm`, but Task Scheduler is the simpler default for the current repository.

## Notes

- `romad` currently uses the current working directory as its workspace root.
- If you change the binary install path, update the systemd unit or launchd plist path.
- On all platforms, check the daemon health with:

```bash
./bin/roma status
```

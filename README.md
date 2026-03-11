# ROMA

ROMA is a local daemon-first **R**untime **O**rchestrator for **M**ulti-**A**gents.

Key entrypoints:

- `bin/roma`: CLI client
- `bin/romad`: local daemon

Core design docs:

- [`DESIGN.md`](./DESIGN.md)
- [`docs/domain-schema-spec.md`](./docs/domain-schema-spec.md)
- [`docs/state-machine-spec.md`](./docs/state-machine-spec.md)
- [`docs/backend-module-design.md`](./docs/backend-module-design.md)

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

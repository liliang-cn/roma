# Running `tagitd`

`tagitd` is a local daemon. It stores its own control-plane state under `$HOME/.tagit`.

Keep these paths separate:

- binary path: where `tagitd` is installed, for example `~/.local/bin/tagitd`
- TagIt home: `$HOME/.tagit`
- repository path: the project directory TagIt targets for execution through isolated worktrees

Build the binaries first:

```bash
make build
```

That produces:

```text
bin/tagit
bin/tagitd
```

Install them to `~/.local/bin`:

```bash
make install
```

## Linux (`systemd --user`)

The repository includes a user unit template at `deploy/systemd/tagitd.service`.

Install it:

```bash
mkdir -p ~/.tagit
mkdir -p ~/.config/systemd/user
cp deploy/systemd/tagitd.service ~/.config/systemd/user/tagitd.service
systemctl --user daemon-reload
systemctl --user enable --now tagitd
```

Useful commands:

```bash
systemctl --user status tagitd
journalctl --user -u tagitd -f
systemctl --user restart tagitd
systemctl --user stop tagitd
```

This unit assumes:

- binary path: `$HOME/.local/bin/tagitd`
- TagIt home: `$HOME/.tagit`

If you want to move TagIt's control-plane state, change `WorkingDirectory=` and keep it pointing at a dedicated TagIt home, not at a source repository.

## macOS (`launchd`)

The repository includes a LaunchAgent template at `deploy/launchd/com.tagit.tagitd.plist`.

Install it:

```bash
mkdir -p ~/Library/LaunchAgents
cp deploy/launchd/com.tagit.tagitd.plist ~/Library/LaunchAgents/com.tagit.tagitd.plist
launchctl bootout "gui/$(id -u)/com.tagit.tagitd" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.tagit.tagitd.plist
launchctl enable "gui/$(id -u)/com.tagit.tagitd"
launchctl kickstart -k "gui/$(id -u)/com.tagit.tagitd"
```

Useful commands:

```bash
launchctl print "gui/$(id -u)/com.tagit.tagitd"
launchctl kickstart -k "gui/$(id -u)/com.tagit.tagitd"
launchctl bootout "gui/$(id -u)/com.tagit.tagitd"
```

The plist assumes:

- binary path: `$HOME/.local/bin/tagitd`
- TagIt home: `$HOME/.tagit`

## Windows

The simplest way is to run `tagitd.exe` in a normal terminal:

```powershell
go build -o bin/tagitd.exe ./cmd/tagitd
Set-Location C:\path\to\TagIt
C:\path\to\TagIt\bin\tagitd.exe
```

For background execution, use Task Scheduler instead of a Windows service first.

Suggested Task Scheduler settings:

- Program: `C:\path\to\TagIt\bin\tagitd.exe`
- Start in: `C:\path\to\TagIt`
- Trigger: `At log on`
- Run whether user is logged on or not: optional
- Restart on failure: enabled

If you specifically want a Windows service, wrap `tagitd.exe` with a service manager such as `nssm`, but Task Scheduler is the simpler default for the current repository.

## Notes

- `tagitd` control-plane state lives in `$HOME/.tagit`; target repositories are chosen per command via `tagit run --cwd <repo> --prompt "<prompt>"`, `tagit run --cwd <repo> --prompt-file <path>`, or by running `tagit` from that repository.
- Agent execution should happen in isolated worktrees under the target repository, not directly inside `$HOME/.tagit`.
- If you change the binary install path, update the systemd unit or launchd plist path.
- On all platforms, check the daemon health with:

```bash
./bin/tagit status
```

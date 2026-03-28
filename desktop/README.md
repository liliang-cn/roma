# ROMA Desktop

`desktop/` is a standalone Wails app module for a desktop UI on top of `romad`.

## Architecture

- `romad` remains the only execution/control-plane authority.
- The Wails backend wraps `github.com/liliang-cn/roma/internal/api`.
- The frontend is React + Vite and polls the daemon for live state.
- If no daemon is reachable, the desktop app starts an embedded `romad`, following the same pattern as the TUI.

## Current MVP Surface

- daemon status summary
- queue list
- run composer
- queue/job detail
- pending/final result view
- plans inbox with preview / approve / reject

## Development

Install the Wails CLI once:

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0
```

Install frontend dependencies:

```sh
cd desktop/frontend
npm install
```

Run the desktop app in dev mode:

```sh
cd desktop
GOWORK=off wails dev
```

Build the frontend bundle only:

```sh
cd desktop/frontend
npm run build
```

Build the desktop app without platform packaging:

```sh
cd desktop
GOWORK=off wails build -nopackage -tags webkit2_41
```

## Linux Notes

The Go code for `desktop/` compiles as a normal module, but a real Wails desktop build on Linux also needs system WebKit/GTK development packages.

On Ubuntu 24.04, install them with:

```sh
sudo apt-get update
sudo apt-get install -y libgtk-3-dev libglib2.0-dev libwebkit2gtk-4.1-dev
```

This distro ships WebKitGTK 4.1, so use the `webkit2_41` build tag when invoking Wails:

```sh
cd desktop
GOWORK=off wails build -nopackage -tags webkit2_41
```

Without that tag, Wails defaults to probing `webkit2gtk-4.0`, which does not exist on Noble. The missing `pkg-config` packages were:

- `gtk+-3.0`
- `gio-unix-2.0`
- `webkit2gtk-4.1`

Package names vary by distro. On Debian/Ubuntu this usually means installing the corresponding `-dev` packages before running `wails build`.

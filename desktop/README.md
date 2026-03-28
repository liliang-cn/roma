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
GOWORK=off wails build -nopackage
```

## Linux Notes

The Go code for `desktop/` compiles as a normal module, but a real Wails desktop build on Linux also needs system WebKit/GTK development packages. In this environment, `wails build` failed until these were available:

- `gtk+-3.0`
- `gio-unix-2.0`
- `webkit2gtk-4.0`

Package names vary by distro. On Debian/Ubuntu this usually means installing the corresponding `-dev` packages before running `wails build`.

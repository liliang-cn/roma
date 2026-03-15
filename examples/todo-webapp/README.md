# TODO Web App Demo

This demo came from a ROMA multi-agent coding run and was finalized locally.

## Backend

Run backend tests:

```bash
go test ./internal/tododemo/...
go test ./cmd/tododemo
```

Start the Go server:

```bash
go run ./cmd/tododemo
```

The server listens on `http://localhost:8080`.

## Frontend

Install dependencies and run tests:

```bash
cd examples/todo-webapp/ui
npm install --no-package-lock
npm test
```

Build the React app:

```bash
npm run build
```

After `dist/` exists, `go run ./cmd/tododemo` serves the built frontend alongside the JSON API.

# AGENTS.md

## Cursor Cloud specific instructions

Sub2API is a single deployable product split into two dev services: a Go API
gateway (`backend/`, entry `./cmd/server`) and a Vue 3 + Vite SPA (`frontend/`).
In production the frontend is embedded into the Go binary; in development they
run as two separate processes. It depends on PostgreSQL and Redis. General setup
lives in `DEV_GUIDE.md` and `README.md`; only non-obvious, durable notes are
captured here.

### Toolchain / services already provisioned in the VM
- Go 1.26.5 at `/usr/local/go` (repo `go.mod` requires `go 1.26.5`; the stock
  `apt` Go is too old). `go` is on PATH via `/usr/local/bin/go`.
- `golangci-lint` v2.7 at `~/go/bin` (matches CI). Run backend lint with
  `golangci-lint run ./...` from `backend/`.
- Node 22 + `pnpm` (managed by nvm; already on PATH). Use `pnpm`, never `npm`.
- PostgreSQL 16 and Redis 7 installed natively (no Docker in this VM).

### Starting services (do this at the start of a session; not in the update script)
- Start datastores: `sudo service postgresql start` and
  `sudo service redis-server start`.
- The `sub2api` Postgres role/db already exist (user `sub2api`, password
  `sub2api`, db `sub2api`, on `127.0.0.1:5432`). Redis is on `127.0.0.1:6379`
  with no password. If the role/db are ever missing, recreate them with
  `CREATE ROLE sub2api LOGIN PASSWORD 'sub2api';` + `createdb -O sub2api sub2api`.
- Run backend: `cd backend && go run ./cmd/server` (serves on `:8080`).
- Run frontend: `cd frontend && pnpm dev` (Vite on `:3000`, proxies
  `/api`, `/v1`, `/setup` to the backend at `:8080`).

### First-run setup wizard (gotcha)
- On first run (no `backend/config.yaml` and no `backend/.installed`) the backend
  starts a setup wizard instead of the API server. Complete it at
  `http://localhost:3000/` (proxied) using the datastore values above.
- IMPORTANT: after finishing the wizard, ensure `backend/config.yaml` has
  `server.port: 8080`. The wizard may persist a different port, and the frontend
  Vite proxy only talks to the backend on `:8080` — a mismatch makes the SPA
  unable to reach the API. Fix the port and restart `go run ./cmd/server`.
- `backend/config.yaml` and `backend/.installed` are gitignored. They are NOT
  committed, so a fresh VM will show the wizard again; just re-run it. Once
  present, the backend boots straight into API mode.

### Tests / build (see `Makefile`, `backend/Makefile`, `frontend/package.json`)
- Backend unit tests: `cd backend && go test -tags=unit ./...` (the `service`
  package suite is slow, ~2-3 min).
- Backend integration/e2e tests need Postgres + Redis running.
- Frontend: `pnpm lint:check`, `pnpm typecheck`, `pnpm test:run`. `router-link`
  "Failed to resolve component" warnings in vitest are benign.
- pnpm prints "Ignored build scripts: esbuild, vue-demi" — this is expected and
  does not break Vite; no `pnpm approve-builds` is needed.
- Full aggregate check: `make test` (backend tests + frontend lint/typecheck/
  critical vitest).

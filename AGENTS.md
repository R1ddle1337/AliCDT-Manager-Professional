# Repository Guidelines

## Project Structure & Module Organization

- `cmd/controller` and `cmd/relay-agent` contain the Go entry points.
- Shared Go implementation lives under `internal/` (`controller`, `relay`, `agent`, `aliyun`, `dnsprovider`, and `protocol`); package tests sit beside code as `*_test.go`.
- `frontend/src` is the Vue 3 console, organized into `components/`, `views/`, and `stores/`; Vite/Tailwind configuration is in `frontend/`.
- `deploy/` contains Go controller/Dispatcher Compose variants and systemd/Nginx assets; `scripts/` contains Agent/Dispatcher installers. `docs/` holds runbooks, while ignored `data*` directories hold runtime state.

## Build, Test, and Development Commands

- `make test` runs `go test -race ./...` and then builds the frontend.
- `make build` builds the controller, relay agent, and frontend; use `make controller`, `make agent`, or `make frontend` for one target.
- `cd frontend && npm install` installs UI dependencies; `npm run dev` starts Vite and `npm run build` creates the production bundle.
- Run the Go development stack with `CDT_ADMIN_TOKEN=... CDT_BOOTSTRAP_ENROLL_TOKEN=... docker compose -f deploy/docker-compose.go.yml up --build`.

## Coding Style & Naming Conventions

Format Go changes with `gofmt`, return contextual errors, and honor `context.Context` in I/O paths. Use `CamelCase` for exported Go identifiers. Match the Vue style: two-space indentation, semicolon-free JavaScript, PascalCase component filenames, and camelCase functions/state. No repository-wide formatter or linter is configured.

## Testing Guidelines

Go tests use the standard `testing` package and are named `Test<Behavior>` (for example, `TestControllerAgentLifecycle`). Add focused regression tests in the owning package and run `go test -race ./...` before submitting. Frontend verification is the Vite production build; no minimum coverage threshold is configured.

## Commit & Pull Request Guidelines

Use a short, imperative subject with a type prefix, such as `feat: add DNS failover`, `fix: validate credentials`, `ui: adjust relay card`, or `docs: update runbook`. Keep unrelated changes separate. PRs should explain behavior/risk, list validation commands, call out config or migration impacts, link issues when applicable, and include before/after screenshots for UI changes. Never include secrets, `.env`, or runtime data.

## Security & Configuration Tips

Keep credentials in environment variables (for example `CDT_ADMIN_TOKEN` and `CDT_BOOTSTRAP_ENROLL_TOKEN`) and treat `data*` as private. Changes to agent downloads, checksums, installers, or deployment privileges need security review and documentation.

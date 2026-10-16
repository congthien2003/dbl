# Repository Guidelines

## Agent Workflow

1. Cho phép build verify, test ở project
2. Hoàn tất mỗi phase phải đợi tôi kiểm tra, test thử app, rồi mới commit
3. Không tự ý commit, push code

Present a file-specific plan and await approval before editing. Preserve unrelated changes. Follow current command restrictions; provide commands when execution is prohibited. Use skills only when requested.

## Project Structure & Module Organization

SBDb uses Go, MyGo native UI, and PostgreSQL through pgx.

- `main.go`: startup and window lifecycle.
- `internal/ui/`: shell, explorer, tabs, dialogs, editor, and grid.
- `internal/app/`: state and workspace coordination.
- `internal/connection/`, `internal/database/`: connections, adapters, PostgreSQL, and fakes.
- `internal/storage/`: settings, profiles, and credentials.
- `internal/grid/`, `internal/mock/`: values, drafts, and datasets.
- `resources/`: assets; `docs/`: specifications, plans, and research.

Tests live beside source as `*_test.go`. Outputs `build/` and `.mygo/` are ignored.

## Build, Test, and Development Commands

Run from the repository root, when authorized:

- `go run .`: launch the desktop app.
- `go tool mygo dev`: develop with MyGo live reload.
- `go build ./...`: compile all packages.
- `go tool mygo build`: package the app using `mygo.json`.
- `go test ./...`: run configured tests.
- `go vet ./...`: run Go static checks.
- `gofmt -w <file.go>`: format changed Go files.

Use the toolchain and dependencies pinned in `go.mod`.

## Coding Style & Naming Conventions

Use `gofmt`, tab indentation, lowercase packages, PascalCase exported identifiers, camelCase unexported identifiers, and snake_case filenames. Keep database work off the UI thread; post state changes through `Window.Update`.

## Testing Guidelines

Use Go `testing`, `TestXxx` names, MyGo `ui.Tester`, and database fakes. PostgreSQL integration tests require `DBL_TEST_PG_HOST`, `DBL_TEST_PG_USER`, and `DBL_TEST_PG_DB`; see `integration_test.go` for options. Use a disposable database. Report skipped tests and distinguish automated checks from manual app testing.

## Commit & Pull Request Guidelines

History uses imperative titles (`Add ...`, `Refactor ...`) and occasional `feat:` prefixes. PRs should describe scope, link issues or plans, report checks and omissions, and include screenshots for UI changes. Await user app testing and explicit commit authorization.

## Security & Configuration

Never commit credentials or store plaintext passwords in profiles. Reuse `internal/storage/credentials.go` and preserve its stable `DBL` credential service name.

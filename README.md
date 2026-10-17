# SBDb

**A lightweight, native database client built with Go.**

**Around 50 MB RAM at runtime¹ · Native UI · PostgreSQL first**

SBDb brings connection management, database exploration, table inspection, and data editing into a focused desktop workspace. Built with Go and MyGo Native UI, it follows a simple principle: **Minimal, Fast, Native**.

Inspired by familiar DBeaver workflows, SBDb focuses on everyday database work with a small memory footprint and a clean interface.

¹ **~50 MB RAM is a development reference reported by the project owner, not an independently verified benchmark.** Actual usage varies with the operating system, active connections, open tabs, and loaded data; it is not a fixed memory limit.

## Lightweight by Design

- **Native desktop UI:** built with MyGo Native UI, without an embedded browser for the application interface.
- **Load data as needed:** paginated table reads, 100 rows per page by default, and a bounded page cache.
- **Connect on demand:** startup loads local configuration; database connections open when requested.
- **Keep work responsive:** database operations run outside the UI rendering path.
- **Stay local:** settings and connection profiles live on your machine; passwords use the operating system credential store.

The specification also targets startup under one second, smooth 60 FPS grid scrolling, and near-zero idle CPU. These are performance goals that require measurement on reference hardware.

## Features

SBDb is under active development. The current implementation includes:

- **Connection management:** saved PostgreSQL profiles, folders, connection testing, TLS options, and connect/disconnect actions.
- **Database explorer:** browse the connected database, schemas, tables, and views with lazy loading.
- **Table Inspector:** inspect data alongside columns, constraints, foreign keys, indexes, and triggers.
- **Data grid:** virtualized rows, server-side pagination, sorting, and one column filter at a time.
- **Explicit editing:** stage supported cell edits locally, then Save or Revert. Saves use transactions and check for conflicts in the values being changed. Views and tables without a suitable row identity remain read-only.
- **Tabbed workspace:** work with multiple tables and SQL editor tabs.
- **SQL workspace:** syntax highlighting, line numbers, independent PostgreSQL sessions, selection/current-statement execution, cancellation, and bounded read-only results.
- **Local SQL files:** Open, Save, and Save As, with unsaved-content prompts, external-change detection, and safe file replacement.
- **Appearance:** Light, Dark, and System themes, plus persisted window size and sidebar width.

## Database Support

| Database | Status |
|---|---|
| PostgreSQL 12+ | Connections, exploration, table reads/updates, SQL execution, and local SQL files; Phase 2.4 awaits app validation |
| MySQL / MariaDB | Planned |
| SQLite | Planned |
| Microsoft SQL Server | Planned |
| Redis | Planned: dedicated key/value explorer and editing workflows |

Windows 10/11 x64 is the primary target. macOS and Linux are secondary targets; platform validation and distribution are part of the roadmap.

## Run Locally

Use the Go toolchain declared in [go.mod](go.mod), currently **Go 1.27.2**, and run commands from the repository root. A PostgreSQL server is needed to browse real database data, but not to open the application.

```sh
go run .
```

For development with MyGo live reload:

```sh
go tool mygo dev
```

To compile all packages or package the desktop app:

```sh
go build ./...
go tool mygo build
```

MyGo packaging uses [mygo.json](mygo.json) and writes distribution output under `build/`. Platform packaging may require additional tools or downloads.

## Local Configuration

Settings and connection profiles are stored under `~/.dbl/` (`%USERPROFILE%\.dbl\` on Windows):

```text
.dbl/
├── settings.json
├── connections.json
└── queries/
```

Passwords are stored separately using the OS credential store. If secure storage is unavailable, the app can request a password for the session instead of persisting it as plaintext.

## SQL Workflow

Create a SQL tab with Ctrl+N (Cmd+N on macOS), or the Explorer's **New SQL tab** action. Choose its connection and use **Execute** or Ctrl/Cmd+Enter. A selected block takes precedence; otherwise the PostgreSQL boundary parser chooses the statement at the cursor. Strings, nested comments and dollar-quoted bodies are recognized. Select the complete block for SQL-standard `BEGIN ATOMIC` bodies; psql commands and COPY through STDIN/STDOUT are unsupported.

Each tab opens its own session on first execution and keeps it until the tab/session closes. `BEGIN`, later statements, and `COMMIT`/`ROLLBACK` can run across separate executions in that tab. Other tabs, the Explorer and table grid use separate sessions. Saving a SQL file does **not** commit database changes. A lost session requires confirmation before opening a fresh one; its transaction, settings and temporary objects are not restored.

Results are read-only snapshots, displayed in pages of 100 retained rows. Each result retains at most 1,000 rows, with a 16 MiB snapshot budget shared by the execution. Paging does not run the SQL again. Reading stops at 100,000 rows or 64 MiB of response payload, 32 result groups, 4,096 columns/result, 1 MiB metadata, or an 8 MiB protocol message. Executions time out after 60 seconds; notices are bounded. Reaching a hard limit or canceling can abort a transaction or close its session. A truncated completed result and an interrupted partial result are labeled separately. If a response is lost, the app does not claim that writes were undone or retry the SQL.

At most eight dedicated SQL sessions per profile and sixteen per app may be open/opening. Close unused SQL tabs to free slots. These limits are resource policies, not measured performance guarantees.

Use **Open SQL**, **Save SQL** (Ctrl/Cmd+S) or **Save As**. First Save asks for a filename, defaulting to `~/.dbl/queries/`; explicit locations outside that folder are allowed. UTF-8 files up to 8 MiB are supported, including BOM/CRLF; unchanged documents preserve their original bytes. File errors, canceled dialogs, and external-change conflicts keep the buffer. Closing, disconnecting, changing a tab's connection, or quitting protects unsaved contents and asks separately about open transactions.

F2/Escape continue to edit/revert table cells, and F5 refreshes the Table Inspector. F5 does not execute SQL. Ctrl+Shift+F remains unassigned.

## Roadmap

1. Validate the completed Phase 2.4 SQL/file workflow in the app and against a disposable PostgreSQL database.
2. Add MySQL/MariaDB, SQLite, and SQL Server support with database-specific behavior.
3. Add Redis key discovery, type/TTL inspection, and scoped value editing.
4. Profile memory and responsiveness, validate supported platforms, and prepare distribution packages.

The initial scope excludes visual query builders, ER diagrams, migration management, backup/restore, import/export, and AI-assisted SQL. See the specification for the complete scope.

## Project & Documentation

The application follows a modular monolith structure: UI in `internal/ui/`, application state in `internal/app/`, adapters in `internal/database/`, and local persistence in `internal/storage/`.

- [Product specification](docs/specs.md)
- [Implementation phases](docs/implementation-phases.md)
- [Motion and performance research](docs/motion-performance-research.md)
- [Contributor guidelines](AGENTS.md)

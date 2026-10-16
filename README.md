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
- **SQL editing:** syntax highlighting and line numbers. Query execution and SQL file management are planned.
- **Appearance:** Light, Dark, and System themes, plus persisted window size and sidebar width.

## Database Support

| Database | Status |
|---|---|
| PostgreSQL 12+ | Current driver: connections, exploration, table reads, and supported cell updates |
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
└── connections.json
```

Passwords are stored separately using the OS credential store. If secure storage is unavailable, the app can request a password for the session instead of persisting it as plaintext.

## Roadmap

1. Complete the PostgreSQL workflow with SQL execution, query results, cancellation, and local SQL file management.
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

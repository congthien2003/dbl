# DBL — Database Lightweight Client

**Software Requirements & Technical Specification**

- Version: 1.0.0
- Status: Approved for implementation
- Application type: Cross-platform Desktop
- Language: Go
- UI Framework: MyGo Native UI
- Architecture: Modular Monolith
- Primary target: Windows 10/11 x64
- Secondary targets: macOS, Linux
- Reference UX: DBeaver
- Project philosophy: Minimal, Fast, Native

---

# 1. Project Overview

## 1.1. Description

DBL is a lightweight, high-performance, cross-platform database management desktop application built entirely with Go and MyGo Native UI.

The application focuses exclusively on essential database operations: connection management, database exploration, table inspection, editable data grids and SQL execution.

DBL is not intended to reproduce all features of DBeaver. It aims to deliver the most frequently used workflows with minimal memory overhead, fast startup and a clean user interface.

## 1.2. Goals

**Primary goals**

1. Fast application startup and responsive UI.
2. Low idle memory and CPU consumption.
3. Familiar DBeaver-style database navigation.
4. Reliable data viewing and editing.
5. Extensible support for multiple database engines.
6. Simple, local-first configuration and storage.
7. Minimal dependencies and straightforward packaging.

**Non-functional targets**

The following are initial performance targets, not guaranteed framework benchmarks:

| Metric              | Initial target                                                 |
| ------------------- | -------------------------------------------------------------- |
| Cold startup        | Under 1 second on reference hardware                           |
| Idle RAM            | Under 100 MB without active connections                        |
| Grid page size      | 100 rows by default                                            |
| Database fetch      | Non-blocking UI                                                |
| Grid scroll         | Target 60 FPS                                                  |
| Initial SQL results | 100 rows per page                                              |
| Binary distribution | Single application executable where platform packaging permits |
| CPU idle            | Near zero when no work is running                              |

Performance must be measured on a documented reference machine and operating system. Database connection time and network query latency are excluded from application startup measurements.

## 1.3. Non-goals

The following features are explicitly outside the initial scope:

- Visual query builder.
- Database migration management.
- ER diagram rendering.
- Database backup and restore.
- Plugin system.
- AI-assisted SQL.
- Data synchronization between databases.
- Data import/export.
- SQL autocomplete or language server.
- Multi-window workspace.
- Dedicated global search via Ctrl+Shift+F.
- Cross-table or cross-database search.
- Direct row insertion/deletion in the grid.
- Editing primary keys through the grid.
- Editing SQL query result cells.
- Built-in database server or cloud synchronization.
- Advanced Redis administration and monitoring.

---

# 2. Technology Stack

| Component            | Technology                          |
| -------------------- | ----------------------------------- |
| Programming language | Go 1.27+                            |
| Desktop framework    | MyGo                                |
| UI mode              | Native UI                           |
| Database abstraction | Go `database/sql`                   |
| PostgreSQL           | pgx/v5/stdlib                       |
| MySQL/MariaDB        | go-sql-driver/mysql                 |
| SQL Server           | go-mssqldb                          |
| SQLite               | ncruces/go-sqlite3                  |
| Redis                | redis/go-redis                      |
| Settings storage     | JSON files                          |
| SQL file storage     | Local filesystem                    |
| Credentials          | OS credential store where supported |
| Logging              | Go slog                             |
| Testing              | Go testing package                  |
| Distribution         | MyGo packaging                      |

Prefer CGO-free builds and avoid introducing an ORM.

Pin dependency versions using `go.mod` and commit `go.sum`. Do not rely on `latest` dependencies in reproducible builds.

---

# 3. User Interface Specification

## 3.1. Main Window

The application uses a single main window with two primary regions:

**Left panel — Database Explorer**

Displays folders, saved connections, databases, schemas and objects.

**Right panel — Workspace**

Displays multiple tabs, each containing either a table inspector or SQL editor.

Suggested layout:

```text
┌───────────────────────────────────────────────────────────────┐
│ DBL                                                ◐ Theme   │
├────────────────┬──────────────────────────────────────────────┤
│ CONNECTIONS    │ [users ×] [orders ×] [query.sql ×] [+]       │
│                ├──────────────────────────────────────────────┤
│ ▾ Work         │ public.users                                 │
│   ▾ Local PG   │                                              │
│     ▾ public   │ [Data] [Properties]                          │
│       users    │                                              │
│       orders   │ [ Search / Filter...             ] [↻]       │
│                │                                              │
│ ▸ Personal     │ ┌────┬───────────┬──────────────────────┐    │
│                │ │ id │ name      │ email                │    │
│ + Connection   │ ├────┼───────────┼──────────────────────┤    │
│ + Folder       │ │ 1  │ Sabo      │ sabo@example.com     │    │
│                │ │ 2  │ Alice     │ alice@example.com    │    │
│                │ └────┴───────────┴──────────────────────┘    │
│                │                                              │
│                │ [Revert] [Save changes]      Page 1  >      │
├────────────────┴──────────────────────────────────────────────┤
│ Connected: Local PG                             Rows 1–100    │
└───────────────────────────────────────────────────────────────┘
```

The explorer width must be resizable.

The application should remember the most recent window size, theme and sidebar width.

Avoid secondary toolbars unless they support an explicitly scoped action.

## 3.2. Workspace Tabs

Supported tab types:

- Table Inspector
- SQL Editor

Each tab owns its own state, including active connection, selected table, filters, pagination and pending changes.

Opening an already-open table should focus the existing tab rather than create duplicates for the same connection and table identifier.

SQL editor tabs can be opened independently and may use the same connection.

Closing a tab with unsaved data or SQL content must prompt the user.

---

# 4. Functional Requirements

## FR-001: Connection Management

### Description

Users can create, edit, delete, organize and connect to multiple database instances.

### Supported operations

- Create connection.
- Edit connection.
- Rename connection.
- Delete connection.
- Test connection.
- Connect/disconnect.
- Create folder.
- Rename folder.
- Delete folder.
- Move connections between folders.

Folders must be able to contain multiple connections.

For the initial version, folder nesting is limited to one level. Connections without a folder appear at the root.

### Connection properties

Shared properties:

| Field              | Required              |
| ------------------ | --------------------- |
| ID                 | Generated UUID        |
| Display name       | Yes                   |
| Driver type        | Yes                   |
| Folder ID          | No                    |
| Host               | For network databases |
| Port               | For network databases |
| Database name      | Driver-dependent      |
| Username           | Driver-dependent      |
| Password           | Driver-dependent      |
| TLS options        | Driver-dependent      |
| SQLite file path   | SQLite only           |
| Connection timeout | Default provided      |

### Behavior

- Connections must not automatically connect at application startup.
- Connecting occurs on user request or when opening an object that requires a connection.
- A failed connection displays an actionable error.
- Connection attempts must support timeout and cancellation.
- Multiple connections may be active simultaneously.
- Deleting a connected profile must first disconnect it and handle affected workspace tabs.
- Folder deletion should offer to move its connections to the root or cancel.

### Security

Connection passwords must never be stored as plaintext in JSON.

Use native credential storage where supported. If secure storage is unavailable, prompt for the password per session rather than silently persisting it unencrypted.

---

## FR-002: Database Explorer

### Description

The left sidebar displays navigable database objects.

### Relational database hierarchy

```text
Connection
└── Database / Catalog
    └── Schema
        ├── Tables
        └── Views
```

For PostgreSQL, database selection is connection-scoped; navigating to a different database may require a separate connection.

For MySQL, the database normally acts as the schema.

For SQLite, the hierarchy can be simplified to:

```text
Connection
└── main
    ├── Tables
    └── Views
```

The explorer should provide a logical hierarchy for each engine, rather than force every engine into an identical physical hierarchy.

### Operations

- Expand/collapse nodes.
- Double-click table to open Table Inspector.
- Double-click view to open read-only Table Inspector.
- Context action to refresh a node.
- Context action to create a blank SQL tab for the selected connection.

Metadata should be loaded lazily.

Do not retrieve all tables, columns, indexes or foreign keys during initial connection.

---

## FR-003: Table Inspector

### Description

Opening a table creates a workspace tab containing two sections:

1. Data
2. Properties

### Data

Displays rows in a virtualized table with pagination.

### Properties

Displays table metadata with the following sections:

- Columns
- Constraints
- Primary keys
- Foreign keys
- Referenced tables and columns
- Incoming foreign-key references
- Indexes
- Triggers

### Column metadata

Each column should expose, when supported by the database:

- Name
- Type
- Native database type
- Nullable
- Default value
- Primary key membership
- Unique constraint membership
- Identity/autoincrement
- Ordinal position

### Constraints

Include:

- Primary key
- Unique
- Check
- Foreign key

### Indexes

Include index name, columns, uniqueness and index type when available.

### Triggers

Include name, event, timing and enabled status when available.

### Behavior

- The inspector is read-only for schema metadata.
- Missing or unsupported metadata must be clearly represented.
- Do not assume every driver exposes identical metadata.
- Refreshing metadata must not overwrite pending data edits.

---

## FR-004: Data Grid

### Description

Displays relational table data using a DBeaver-inspired spreadsheet layout.

### Requirements

- Virtualized rendering.
- Column headers.
- Horizontal and vertical scrolling.
- Column resize.
- Row selection.
- Cell selection.
- Copy selected cell value.
- Pagination.
- Reload data.
- Column sorting.
- Server-side filtering.
- Nullable value representation.
- Large-value display truncation.
- Direct cell editing.

### Pagination

Default page size: 100 rows.

Use server-side pagination.

The system must not load entire tables into memory by default.

Offset pagination may be used initially when a stable order can be established.

Prefer keyset pagination for large tables with a suitable sortable unique key.

Never assume a table has a primary key or deterministic row order.

If stable ordering cannot be established, display a warning and disable editing when unique row identity cannot be guaranteed.

### Data types

The grid must distinguish:

- NULL
- Empty string
- Number
- Boolean
- Date/time
- Text
- Binary data
- JSON/text-like values

Large values should be truncated in the grid without changing the underlying value.

Binary cells should display a safe placeholder instead of attempting to render raw bytes.

---

## FR-005: Editable Data Grid

### Description

Users can double-click editable cells, change values and save modifications to the database.

### Editing lifecycle

```text
View
  ↓
Enter Edit Mode
  ↓
Modify Value
  ↓
Validate
  ↓
Mark Cell Dirty
  ↓
Save or Revert
```

### UI behavior

- Double-click or F2 activates the cell editor.
- Enter confirms the local cell edit.
- Escape discards the uncommitted cell editor value.
- Modified cells are visually highlighted.
- Save changes commits pending edits.
- Revert changes restores original values.
- Multiple cells and rows can be modified before saving.
- Failed saves keep pending edits intact.

### Editability rules

Editable tables require a reliable unique row identifier.

Preferred identifier:

1. Primary key.
2. Non-null unique key that reliably identifies one row.

If no suitable identifier exists, the grid is read-only.

The initial version does not support editing:

- Primary-key cells.
- Generated columns.
- Identity columns.
- Binary/large-object values.
- Query result cells.
- Database views.

### Change tracking

Changes must be represented separately from loaded rows.

Each row change tracks:

- Original primary-key values.
- Original column values required for conflict detection.
- Current edited values.
- Dirty columns.
- Validation errors.

Editing a cell back to its original value removes it from the pending changes.

### Save algorithm

1. Validate all pending edits.
2. Begin database transaction.
3. Generate parameterized UPDATE statements.
4. Apply changes using original row identity.
5. Use optimistic concurrency checks.
6. Verify affected row counts.
7. Roll back on any failure.
8. Commit when all updates succeed.
9. Reload the affected page.
10. Clear pending changes only after successful commit.

### Optimistic concurrency

Where a row version field is not available, compare original column values in the UPDATE predicate to detect concurrent changes.

Database-specific NULL-safe comparisons must be implemented.

For the initial version, use conservative comparisons so modifications to the original row can cause a conflict rather than silently overwrite another user's changes.

If a database cannot reliably support optimistic concurrency for a particular column type, the adapter must report that limitation and disable unsafe editing rather than silently drop concurrency checks.

### Conflict handling

- Zero affected rows: conflict or deleted row.
- More than one affected row: unsafe update; rollback.
- Validation failure: do not execute writes.
- SQL error: rollback all writes in the Save operation.

### Pending edit protection

When changes exist:

- Closing tab requires confirmation.
- Switching tabs preserves the changes.
- Changing the active filter requires Save, Discard or Cancel.
- Changing page requires Save, Discard or Cancel.
- Refreshing data requires Save, Discard or Cancel.
- Disconnecting requires Save, Discard or Cancel.

The same protection applies when exiting the application.

A successful Save applies all pending changes in the current table tab as one transaction, subject to database transaction support.

---

## FR-006: Data Grid Search and Filtering

### Description

A single search/filter bar is displayed above the table grid.

This is distinct from the removed Ctrl+Shift+F feature.

### Behavior

The filter bar allows users to choose a column and enter a search value.

For text columns, the default operation is a contains search.

For other types, use applicable typed comparisons, initially equality.

Supported initial operators:

- Equals
- Not equals
- Contains for text
- Greater than
- Less than
- Is NULL
- Is not NULL

Filter execution occurs on the database server.

### Implementation requirements

- Debounce typed text filters, with a configurable initial delay of approximately 300 ms.
- Cancel superseded queries.
- Reset pagination on filter changes.
- Use bound SQL parameters for values.
- Quote identifiers using database-specific dialects.
- Never concatenate user input directly into SQL expressions.
- Show loading and error states.

The first version supports one active column filter at a time.

No global table search dialog, cross-column exhaustive search shortcut or cross-database search is included.

---

## FR-007: SQL Editor

### Description

Users can create SQL editor tabs, write SQL, execute queries and save SQL files locally.

### Supported operations

- New SQL tab.
- Open local SQL file.
- Edit SQL text.
- Save SQL file.
- Save As.
- Execute selected SQL.
- Execute the current statement when no selection exists.
- Display query results.
- Display affected row count.
- Display query errors.
- Cancel running query.

### SQL editor requirements

- Monospaced font.
- SQL syntax highlighting.
- Line numbers where supported.
- Horizontal/vertical scrolling.
- Unsaved file indicator.
- Active connection selector.
- Multiple independent editor tabs.

### Execution behavior

Selected SQL takes precedence over the statement at the cursor.

If no selection exists, execute the current statement using a dialect-aware statement boundary parser.

The application must not naïvely split SQL text on every semicolon because semicolons can appear in strings, comments and procedural SQL.

For unsupported complex scripting constructs, executing a selected block is the documented fallback.

Read-only SELECT results should use the standard paginated result presentation when safely supported. Result sets that cannot be paginated are bounded and presented read-only.

Write statements execute against the chosen connection.

Database transaction semantics must be respected, and unsafe automatic retries of write queries are prohibited.

### Keyboard shortcuts

| Action             | Shortcut   |
| ------------------ | ---------- |
| New SQL tab        | Ctrl+N     |
| Save SQL           | Ctrl+S     |
| Execute statement  | Ctrl+Enter |
| Edit grid cell     | F2         |
| Revert cell editor | Esc        |
| Refresh table      | F5         |

On macOS, command shortcuts should follow platform conventions where applicable.

Ctrl+Shift+F is not assigned.

---

## FR-008: SQL File Management

### Storage

Default root directory:

`~/.dbl/`

Expected structure:

```text
.dbl/
├── connections.json
├── settings.json
└── queries/
    ├── work/
    │   ├── report.sql
    │   └── debug.sql
    └── personal/
        └── test.sql
```

### Requirements

- New SQL editor starts as an unsaved document.
- First Save prompts for filename and location under the default queries directory.
- Subsequent saves overwrite the associated file.
- Save As allows creating another file.
- Existing SQL files can be opened.
- Unsaved SQL contents must be protected when closing tabs.
- File paths must be validated to avoid unintended traversal.
- Writes should use safe temporary-file replacement where appropriate.

The application does not need a dedicated file explorer for SQL files.

The connection folders and SQL filesystem folders are independent concepts.

---

## FR-009: Theme Management

### Requirements

Supported themes:

- Light
- Dark
- System

The selected theme must persist between launches.

All components must use shared semantic colors.

Modified cells, errors, focus indicators and disabled controls must remain distinguishable in both themes.

Theme changes must not reset workspace state or active database connections.

---

## FR-010: Database Driver Support

### Relational drivers

The application must support:

- PostgreSQL
- MySQL
- Microsoft SQL Server
- SQLite

All relational drivers implement shared capabilities:

- Connect/disconnect.
- List databases/schemas.
- List tables/views.
- Read table metadata.
- Fetch table rows.
- Apply filters/sorts/pagination.
- Update table cells.
- Execute SQL.
- Manage transactions.

Metadata and SQL dialect implementations are engine-specific.

### Redis

Redis uses a separate adapter because it is not relational.

Supported Redis features:

- Connect/disconnect.
- Select logical database where applicable.
- Discover keys using SCAN.
- Filter keys by name pattern.
- Show key type.
- Show TTL.
- Read values.
- Edit supported values.
- Save supported value changes.

Supported types:

- String
- Hash
- List
- Set
- Sorted Set

Redis edits are saved using Redis commands appropriate to the type.

Redis is not required to implement SQL features, table constraints, joins or relational grid pagination.

Avoid KEYS commands for general key discovery on large Redis instances.

Redis transactions and conditional writes cannot be assumed to behave identically to relational database transactions. Clearly document Redis write guarantees and avoid presenting multi-key changes as atomic when they are not.

---

# 5. System Architecture

## 5.1. Architecture Style

Use a modular monolith within one desktop process.

```text
┌──────────────────────────────────────┐
│           MyGo Native UI             │
│                                      │
│ Explorer / Grid / Inspector / Editor │
└──────────────────┬───────────────────┘
                   │
┌──────────────────▼───────────────────┐
│          Application Layer           │
│                                      │
│ Connection Manager                   │
│ Workspace Manager                    │
│ Grid Controller                      │
│ Change Tracker                       │
│ Query Executor                       │
└──────────────────┬───────────────────┘
                   │
┌──────────────────▼───────────────────┐
│           Database Layer             │
│                                      │
│ SQL Adapter     Redis Adapter        │
│ SQL Dialects    Metadata Readers     │
└──────────────────┬───────────────────┘
                   │
┌──────────────────▼───────────────────┐
│           Storage Layer              │
│                                      │
│ JSON Config / SQL Files / Keychain   │
└──────────────────────────────────────┘
```

UI components must not execute SQL directly.

Application services should own asynchronous operations, cancellation and state transitions.

Database adapters must not import MyGo UI packages.

## 5.2. Suggested Project Structure

```text
dbl/
├── main.go
├── go.mod
├── go.sum
├── mygo.json
├── internal/
│   ├── app/
│   │   ├── app.go
│   │   ├── state.go
│   │   └── actions.go
│   ├── ui/
│   │   ├── window.go
│   │   ├── explorer.go
│   │   ├── workspace.go
│   │   ├── tabs.go
│   │   ├── grid.go
│   │   ├── properties.go
│   │   ├── sql_editor.go
│   │   └── theme.go
│   ├── connection/
│   │   ├── manager.go
│   │   └── models.go
│   ├── database/
│   │   ├── adapter.go
│   │   ├── models.go
│   │   ├── dialect.go
│   │   ├── postgres/
│   │   ├── mysql/
│   │   ├── sqlserver/
│   │   ├── sqlite/
│   │   └── redis/
│   ├── grid/
│   │   ├── controller.go
│   │   ├── changeset.go
│   │   ├── validation.go
│   │   └── persistence.go
│   ├── query/
│   │   ├── executor.go
│   │   └── results.go
│   └── storage/
│       ├── config.go
│       ├── credentials.go
│       └── files.go
├── tests/
│   └── integration/
├── resources/
└── README.md
```

Keep package boundaries practical. Avoid repository/service interfaces without an actual need.

---

# 6. Core Domain Models

## 6.1. Connection Configuration

```go
type DriverType string

const (
    DriverPostgres  DriverType = "postgres"
    DriverMySQL     DriverType = "mysql"
    DriverSQLServer DriverType = "sqlserver"
    DriverSQLite    DriverType = "sqlite"
    DriverRedis     DriverType = "redis"
)

type ConnectionConfig struct {
    ID         string
    Name       string
    FolderID   string
    Driver     DriverType
    Host       string
    Port       int
    Database   string
    Username   string
    SecretRef  string
    SQLitePath string
}
```

Driver-specific options such as TLS configuration should be represented by typed options rather than unvalidated arbitrary SQL fragments.

## 6.2. Metadata Models

```go
type TableRef struct {
    Catalog string
    Schema  string
    Name    string
}

type ColumnMetadata struct {
    Name       string
    DataType   string
    Nullable   bool
    PrimaryKey bool
    Default    *string
}

type TableMetadata struct {
    Ref         TableRef
    Columns     []ColumnMetadata
    PrimaryKey  []string
    Constraints []ConstraintMetadata
    ForeignKeys []ForeignKeyMetadata
    Indexes     []IndexMetadata
    Triggers    []TriggerMetadata
}
```

Define dedicated types for constraints, foreign keys, indexes and triggers.

Preserve information about unsupported or unavailable metadata rather than treating every missing property as false.

## 6.3. SQL Adapter

```go
type SQLAdapter interface {
    Connect(
        ctx context.Context,
        cfg ConnectionConfig,
    ) error

    Close() error

    ListSchemas(
        ctx context.Context,
    ) ([]Schema, error)

    ListTables(
        ctx context.Context,
        schema string,
    ) ([]Table, error)

    DescribeTable(
        ctx context.Context,
        ref TableRef,
    ) (*TableMetadata, error)

    FetchPage(
        ctx context.Context,
        req PageRequest,
    ) (*PageResult, error)

    SaveChanges(
        ctx context.Context,
        ref TableRef,
        changes ChangeSet,
    ) error

    Execute(
        ctx context.Context,
        query string,
    ) (*QueryResult, error)
}
```

The interface is conceptual; refine result streaming and transaction handling during implementation.

Redis must use a separate capability-oriented interface.

---

# 7. Data Grid Engine

## 7.1. Grid State

Each table tab maintains:

- Connection ID.
- Table reference.
- Loaded page data.
- Current page/cursor.
- Sort definition.
- Filter definition.
- Selected cell.
- Editing cell.
- Pending changes.
- Loading status.
- Last error.

## 7.2. Fetching Strategy

1. User opens table.
2. Retrieve table metadata.
3. Establish row identity and editability.
4. Construct a paginated query.
5. Fetch one page.
6. Convert database values to typed grid values.
7. Render only visible cells.
8. Fetch another page when requested.

Do not cache entire tables.

Use bounded page caches with predictable eviction.

## 7.3. Edit State

Use separate original and draft values.

```go
type CellChange struct {
    Column   string
    Original any
    Current  any
}

type RowChange struct {
    Key     map[string]any
    Changes map[string]CellChange
}

type ChangeSet struct {
    Rows map[string]*RowChange
}
```

Row identity should use a stable canonical representation rather than display-formatted values.

NULL, empty strings, binary values and numeric types must maintain their distinctions.

Dirty tracking must not rely on formatted string comparisons.

## 7.4. Saving Changes

SQL writes must:

- Quote table/column identifiers correctly.
- Parameterize all values.
- Use verified row identity.
- Check optimistic concurrency.
- Run in a transaction when supported.
- Roll back after errors.
- Never retry non-idempotent writes automatically.

Grid changes may only be cleared after confirmed persistence.

---

# 8. Performance Requirements

## 8.1. Startup

Do not initialize database connections at application launch.

Load only:

- Settings.
- Saved connection metadata.
- Theme.
- Main window state.

Credential retrieval and network operations should occur on demand.

## 8.2. UI Thread

All blocking database calls must execute outside the UI rendering path.

Results must be returned to the UI using MyGo-supported thread-safe state update mechanisms.

The UI must remain responsive during:

- Connection attempts.
- Schema discovery.
- Large queries.
- Data saving.
- Network timeouts.

## 8.3. Memory

Keep only bounded database result sets in memory.

Release query results, tab caches and connection resources when no longer needed.

Avoid allocating new temporary buffers for every visible cell on each render when stable values can be reused.

## 8.4. Connection Pools

Initialize small pools by default.

For example:

- Maximum open connections: 2.
- Maximum idle connections: 1.

These are defaults, not hard-coded limits.

SQLite and Redis need their own connection behavior.

## 8.5. Query Cancellation

Every long-running operation should accept `context.Context`.

Canceled or obsolete query results must never overwrite newer grid state.

---

# 9. Error Handling

Use structured errors with stable categories:

- Connection failure.
- Authentication failure.
- Permission denied.
- Query syntax error.
- Query timeout.
- Query canceled.
- Metadata unavailable.
- Unsupported operation.
- Data validation error.
- Concurrent modification.
- Save failure.
- File access failure.

Error messages must be understandable and include relevant context.

Raw database errors may be displayed where useful, but must not expose passwords or stored secrets.

Save errors must retain pending edits.

---

# 10. Security Requirements

- Never persist plaintext passwords.
- Never log credentials or complete connection strings containing secrets.
- Parameterize SQL values generated by the grid.
- Quote identifiers using trusted engine-specific dialects.
- Validate folders and filesystem paths.
- Use secure transport options for network databases.
- Support connection timeouts.
- Never retry UPDATE operations automatically after ambiguous network failures.
- Avoid executing SQL on application startup.
- Preserve a clear distinction between read-only and editable data.
- Never silently write changes when a user merely leaves a cell.
- Clearly identify the active connection and database in each SQL editor.
- Respect database permissions rather than attempting privilege escalation.

---

# 11. Configuration

## 11.1. settings.json

Example:

```json
{
  "version": 1,
  "theme": "system",
  "grid": {
    "pageSize": 100,
    "maxCachedPages": 3,
    "filterDebounceMs": 300
  },
  "window": {
    "width": 1280,
    "height": 800,
    "sidebarWidth": 260
  }
}
```

## 11.2. connections.json

Example:

```json
{
  "version": 1,
  "folders": [
    {
      "id": "folder-1",
      "name": "Work"
    }
  ],
  "connections": [
    {
      "id": "connection-1",
      "name": "Local PostgreSQL",
      "folderId": "folder-1",
      "driver": "postgres",
      "host": "localhost",
      "port": 5432,
      "database": "app",
      "username": "postgres",
      "secretRef": "dbl/connection-1"
    }
  ]
}
```

The examples demonstrate serialized configuration, not the exact Go struct encoding.

Application storage files must be versioned so future migrations remain possible.

Configuration writes should be atomic where feasible.

---

# 12. Testing Strategy

## 12.1. Unit Tests

Test:

- Configuration validation.
- Connection folder operations.
- SQL dialect generation.
- Identifier quoting.
- Filter building.
- Pagination state.
- Dirty change tracking.
- Value conversion.
- Conflict detection.
- Save/revert transitions.
- SQL file persistence.

## 12.2. Integration Tests

Use isolated database instances to validate:

- PostgreSQL connection and metadata.
- MySQL connection and metadata.
- SQL Server metadata and paging.
- SQLite local-file access.
- Redis key discovery and value editing.
- SQL execution.
- Transaction rollback.
- Concurrent update conflicts.
- NULL handling.
- Composite primary keys.
- Foreign-key metadata.
- Driver-specific placeholder syntax.

Include tables containing reserved identifiers, Unicode text, JSON values and large values.

## 12.3. UI Tests

Validate:

- Connection explorer navigation.
- Tab management.
- Keyboard shortcuts.
- Editable grid lifecycle.
- Unsaved changes warnings.
- Theme switching.
- Error and loading states.
- Long-running queries without UI freezes.

## 12.4. Performance Tests

Measure:

- Cold startup.
- Idle RAM.
- Idle CPU.
- Grid scrolling.
- Initial table load.
- Page navigation.
- Schema explorer expansion.
- Large result handling.
- Repeated tab open/close memory behavior.

Track regressions between releases.

---

# 13. Implementation Roadmap

## Phase 1 — Native UI Foundation

**Deliverables**

- Initialize Go/MyGo project.
- Implement main window.
- Implement resizable sidebar.
- Implement mock connection tree.
- Implement workspace tabs.
- Implement mock virtualized grid.
- Implement theme switching.
- Validate editable-cell interaction.

**Acceptance**

The application can open quickly, switch tabs and scroll large mock datasets smoothly without using an embedded browser.

## Phase 2 — PostgreSQL MVP

**Deliverables**

- Persist connection profiles and folders.
- PostgreSQL connect/disconnect.
- Database/schema/table explorer.
- Table metadata.
- Data paging.
- Column filtering/sorting.
- Editable grid.
- Save/Revert transaction.
- SQL editor.
- Save/open local SQL files.

**Acceptance**

Users can manage a PostgreSQL connection and perform the complete core workflow without DBeaver.

## Phase 3 — Other Relational Drivers

**Deliverables**

- MySQL.
- SQLite.
- SQL Server.
- Dialect-specific metadata queries.
- Dialect-specific pagination.
- Dialect-specific updates and concurrency checks.
- Integration tests.

**Acceptance**

Core SQL functionality behaves consistently across supported relational engines, except explicitly documented engine limitations.

## Phase 4 — Redis

**Deliverables**

- Redis connection profiles.
- Key explorer.
- Key pattern filtering.
- Type and TTL inspection.
- Value display.
- Supported value editing.
- Save changes.

**Acceptance**

Users can browse and edit common Redis values without needing another client for the scoped workflows.

## Phase 5 — Stabilization and Packaging

**Deliverables**

- UI responsiveness validation.
- Memory profiling.
- CPU profiling.
- Cancellation tests.
- Security review.
- Windows build.
- macOS/Linux builds where supported.
- Installation and usage documentation.

**Acceptance**

All core user workflows pass functional and regression testing on supported platforms.

---

# 14. Definition of Done

The first full release is complete when:

- [ ] Application launches with MyGo Native UI.
- [ ] Multiple connections can be created and organized.
- [ ] Connections can be renamed, edited, tested and deleted.
- [ ] Connections are opened lazily.
- [ ] Database explorer supports supported object hierarchies.
- [ ] Table properties display columns, constraints, foreign keys, indexes and triggers where available.
- [ ] Table data displays in a virtualized paginated grid.
- [ ] Grid supports sorting and column filtering.
- [ ] Cells can be edited directly.
- [ ] Changes are tracked without immediate database writes.
- [ ] Save persists validated changes.
- [ ] Revert discards pending changes.
- [ ] Save failures do not lose local edits.
- [ ] SQL editor can execute SQL against a selected connection.
- [ ] SQL files can be opened and saved in local folders.
- [ ] Dark, light and system themes function correctly.
- [ ] PostgreSQL is supported.
- [ ] MySQL is supported.
- [ ] SQL Server is supported.
- [ ] SQLite is supported.
- [ ] Redis is supported within its dedicated feature scope.
- [ ] Database operations do not block the UI.
- [ ] Credentials are not stored in plaintext.
- [ ] Unit and integration tests pass.
- [ ] Performance targets are measured and documented.
- [ ] Application can be packaged for the primary target platform.

---

# 15. Engineering Rules for Coding Agents

These rules apply to every implementation phase.

1. Use Go and MyGo Native UI as the default and only UI stack.
2. Do not introduce React, Electron, Wails, Tauri, HTTP servers or WebView without an explicit architecture decision.
3. Keep implementations simple and idiomatic.
4. Avoid unnecessary abstraction and dependencies.
5. Never execute blocking database operations in the UI render path.
6. Use context cancellation for database operations.
7. Do not load unbounded result sets into memory.
8. Separate user-interface state from database persistence state.
9. Do not let UI components construct raw SQL.
10. Isolate engine-specific SQL in database adapters.
11. Never concatenate untrusted values into generated SQL.
12. Do not silently persist edited grid values.
13. Keep user changes intact after failed saves.
14. Add tests for all data-writing behavior.
15. Prefer targeted changes over major refactoring.
16. Do not implement features outside this specification.
17. Verify MyGo APIs against the pinned framework version before using them.
18. Record any unavoidable scope deviation before implementation.
19. Run relevant tests and builds after each phase.
20. Prioritize correctness of writes and data safety over micro-optimizations.

---

# 16. Final Product Principle

**DBL should do a small number of database tasks exceptionally well.**

The application must remain focused on:

- Connection management.
- Database navigation.
- Table inspection.
- Data viewing and editing.
- SQL execution.
- Local SQL file management.

Every proposed addition should be evaluated against three questions:

1. Is it essential to the core workflow?
2. Does it justify its implementation and maintenance cost?
3. Can it be delivered without compromising simplicity and performance?

If the answer is no, the feature should remain outside the product scope.

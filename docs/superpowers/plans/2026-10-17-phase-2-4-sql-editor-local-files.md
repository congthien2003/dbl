# Phase 2.4 — SQL Editor & Local Files Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hoàn tất SQL workflow PostgreSQL: session riêng cho từng tab, Execute selection/current statement, bounded read-only results và local SQL files có bảo vệ nội dung.

**Architecture:** Giữ pool hiện tại cho Explorer/Table Inspector; SQL tabs dùng dedicated pgx connections được đăng ký và thu hồi theo connection epoch. UI gửi actions tới app; query service quản lý leases và snapshots, PostgreSQL adapter sở hữu parsing/protocol/transaction state, storage sở hữu file identity và replacement.

**Tech Stack:** Go `1.27.2`, MyGo `v0.5.5`, pgx/v5 `v5.11.0`; Go testing, MyGo ui.Tester và PostgreSQL disposable cho integration.

**Spec:** [2026-10-17-phase-2-4-sql-editor-local-files-design.md](../specs/2026-10-17-phase-2-4-sql-editor-local-files-design.md). Người dùng đã duyệt spec, yêu cầu lập plan và chọn **Native** ngày 2026-10-17; implementation được phép, các lệnh checks và commit vẫn theo giới hạn bên dưới.

## Global Constraints

- Đọc cả spec và AGENTS.md trước execution; giữ nguyên unrelated changes và không đổi `go.mod`/`go.sum`.
- Không tự chạy build/test/lint/verify hoặc launch app. Mọi command trong plan dành cho người dùng tự chạy; không coi checklist là kết quả đã đạt.
- Không commit/push. Sau implementation chờ người dùng test app và yêu cầu commit rõ ràng; bỏ bước frequent commits mặc định của skill.
- Duyệt plan và chọn execution method trước sửa code; không tự chuyển sang Phase 3.
- Go `1.27.2`, MyGo `v0.5.5`, pgx/v5 `v5.11.0`; không thêm UI stack, engine, dependency, autocomplete, history hoặc SQL result editing.
- Database/file IO ngoài UI thread; state mutation qua Window.Update/poster. Không để resource sống chỉ nhờ callback UI chưa được nhận.
- Một SQL tab giữ một session, mỗi session chỉ chạy một database operation tại một thời điểm; không dùng table pool cho session của tab.
- Không tự BEGIN/COMMIT/retry/rewrite SQL. Đổi trang không Execute lại; command completion khác transaction commit.
- SQL/file input tối đa **8 MiB UTF-8**; file có UTF-8 BOM được hỗ trợ, không gửi BOM như một phần SQL.
- Snapshot: **100 rows/page**, **1.000 rows/result**, **16 MiB/execution**. Hard read thresholds: **100.000 rows**, **64 MiB payload/execution** kể cả discarded rows/metadata/notices.
- Các giới hạn còn lại: **32 result groups**, **4.096 columns/result**, **1 MiB metadata/execution**, **32 notices × 4 KiB/notice**, **8 MiB protocol message body**.
- Dedicated session capacity: **8/profile**, **16/app**, tính cả đang mở. Timeout mở theo profile; Execute **60 giây** sau khi session ready; cleanup **2 giây**; SQL shutdown toàn app deadline tổng **2 giây**.
- Không hứa RAM bằng snapshot limit hoặc byte-accurate wire quota. Read threshold có thể phát hiện sau một message có giới hạn; không có unbounded drain.
- Query errors/cancel/timeout/file errors giữ SQL buffer. Save SQL file không commit transaction. Credential service name `DBL` và credential policy hiện có giữ nguyên.
- Source/diff review là bằng chứng tĩnh; automated checks, PostgreSQL integration và manual app testing phải báo riêng.

## Review Focus

1. COMMIT đã gửi nhưng mất response/cancel tới muộn: giữ command evidence, báo uncertain outcome khi cần, không khẳng định chưa ghi và không retry — Tasks 4, 7, 10.
2. Tab/session tạo xong khi UI callback bị drop hoặc profile reconnect: thu hồi session/slot và không dùng result từ epoch cũ — Tasks 3, 7.
3. CRLF/BOM/Unicode và mixed line endings: cursor đúng byte offset, Open không dirty giả, Save không sửa giữ nguyên bytes — Tasks 2, 5, 6, 9.
4. Hai tab cùng Save As, symlink/hardlink hoặc file đổi bên ngoài: phát hiện cùng file/conflict, không overwrite một file chưa xác nhận — Tasks 5, 6.
5. Result chứa tên cột trùng, notice flood, một row lớn hoặc COPY mode: giữ UI identity đúng và kết thúc theo budget thay vì leak/drain vô hạn — Tasks 4, 9, 10.

---

## 1. File map và dependency

Các file bên dưới là phạm vi implementation đã duyệt. Kết quả thực hiện và điều chỉnh tổ chức tests được ghi tại §6.

| Task | Create | Modify |
| --- | --- | --- |
| 1 — Contracts/budget | `internal/database/sql_execution.go`; `internal/query/executor.go`, `executor_test.go`, `results.go`, `results_test.go` | `internal/connection/manager.go`, `manager_test.go` thêm SQL session capability accessor/test; `internal/grid/source.go` thêm optional Column.ID; `internal/database/adapter.go` chỉ cập nhật comment phân biệt table API/session API |
| 2 — Boundary parser | `internal/database/postgres/statement.go`, `statement_test.go` | Không |
| 3 — Session ownership | `internal/database/postgres/sql_session.go`, `sql_session_test.go` | `internal/database/postgres/connect.go`; `internal/connection/manager_test.go` cho accessor/lifecycle |
| 4 — Protocol/results | `internal/database/postgres/sql_execute.go`, `sql_protocol.go`, `sql_values.go`, `sql_errors.go`, `sql_execute_test.go`, `sql_protocol_test.go`, `sql_errors_test.go` | `internal/database/postgres/sql_session.go` wire execution; `internal/query/results_test.go` bổ sung outcome/source cases |
| 5 — File store | `internal/storage/files.go`, `file_identity.go`, `file_replace_windows.go`, `file_replace_other.go`, `files_test.go`, `file_replace_test.go` | Không sửa helper settings/catalog `internal/storage/atomic.go` |
| 6 — Documents/file actions | `internal/app/sql_state.go`, `sql_document.go`, `sql_files.go`, `sql_files_test.go`; `internal/ui/sql_files.go` | `internal/app/state.go`, `workspace.go`, `workspace_test.go`, `ops.go`; `internal/ui/shell.go`; `main.go` |
| 7 — Execution application | `internal/app/sql.go`, `sql_test.go`; `internal/database/fakedb/sql.go`, `sql_test.go` | `internal/app/sql_state.go`, `state.go`, `workspace.go`; `internal/database/fakedb/fakedb.go` |
| 8 — Pending actions | `internal/app/sql_guard.go`, `sql_guard_test.go` | `internal/app/state.go`, `save.go`, `connections.go`, `workspace.go`; `internal/ui/shell.go`, `dialogs.go`; existing `internal/ui/guard_test.go` |
| 9 — SQL UI | `internal/ui/sql_results.go`, `sql_results_test.go`, `sql_files_test.go` | `internal/ui/sql_editor.go`, `sql_editor_test.go`, `tabs.go`, `shell.go`, `workspace.go`, `explorer.go`, `grid.go` |
| 10 — End-to-end/handoff | `internal/database/postgres/integration_sql_test.go` | `main.go` shutdown wiring; `README.md`; `docs/implementation-phases.md`; kết quả thực hiện trong plan này |

Dependency: **1 → 2 → (3–4) → 5 → 6 → 7 → 8 → 9 → 10**. Slices 3/4 là một task group native-session/execution, cùng một review/Green checkpoint: factory cần Execute implementation để trả SQLSession hoàn chỉnh; không tạo stub unsupported chỉ để compile slice3 riêng. Tasks 2/5 có thể đọc độc lập nhưng execution mặc định nối tiếp để giữ ít work-in-progress. Không tạo scaffolding ngoài phạm vi chỉ để đủ cây thư mục.

## 2. Interfaces dùng xuyên tasks

Đặt các kiểu public sau tại file được chỉ định; implementation bodies thuộc task tương ứng. Không đưa `connection.Profile` vào package database vì package connection đang import database.

### 2.1. Database contracts — Task 1

`internal/database/sql_execution.go`:

```go
type SQLSpan struct{ Start, End int } // UTF-8 byte offsets, half-open
type SQLInput struct {
    Text string
    Cursor int
    Selections []SQLSpan
}
type SQLSelection struct { SQL string; Span SQLSpan }
type SQLSessionFactory interface {
    OpenSQLSession(ctx context.Context) (SQLSession, error)
}
type SQLSession interface {
    Select(input SQLInput) (SQLSelection, error)
    Execute(ctx context.Context, sql string) (*SQLExecution, error)
    Close(ctx context.Context) error
}
type SQLTransaction uint8 // SQLIdle, SQLInTransaction, SQLTransactionFailed, SQLSessionLost
type SQLStop uint8        // SQLCompleted, SQLFailed, SQLCanceled, SQLTimedOut, SQLLimitReached
type SQLColumn struct { Name string; TypeOID uint32; TypeName string; Kind grid.Kind }
type SQLResult struct {
    Columns []SQLColumn
    Rows [][]grid.Value
    CommandTag string
    RowsAffected int64
    HasRowsAffected, CommandComplete, Truncated bool
}
type SQLExecution struct {
    Results []SQLResult
    Notices []string
    DroppedNotices int
    Stop SQLStop
    Transaction SQLTransaction
    Ready, OutcomeUncertain bool
    Elapsed time.Duration
    Err *Error
}
```

`Execute` trả execution evidence cả khi `error != nil`; nil execution chỉ cho lỗi trước dispatch. `Ready` xác nhận đã đồng bộ tới ReadyForQuery, không là boolean committed. Unknown type dùng text/class phù hợp và type label `OID <n>`, không chạy metadata query phụ khi transaction failed. Error hiển thị nằm trong `*database.Error`; nguyên nhân không được log kèm secret/SQL buffer.

Khai báo constants ở cùng file: `SQLMaxInputBytes`, `SQLPageSize`, `SQLSnapshotRows`, `SQLSnapshotBytes`, `SQLReadRows`, `SQLReadBytes`, `SQLMaxResults`, `SQLMaxColumns`, `SQLMetadataBytes`, `SQLMaxNotices`, `SQLNoticeBytes`, `SQLMessageBytes`, `SQLTimeout`, `SQLCleanupTimeout` với đúng Global Constraints. Capacity constants thuộc query service. `SQLSession.Select` là pure boundary selection trên setting session hiện tại; không phát query. Empty selection result (`SQL == ""`) là no-op, không phải lỗi execution.

### 2.2. Query service — Tasks 1/3/7

`internal/query/executor.go`:

```go
func NewExecutor() *Executor
func (e *Executor) Open(openCtx, lifetime context.Context, profileID string, f database.SQLSessionFactory) (*Lease, error)
func (e *Executor) Close(ctx context.Context) error
// Lease implements database.SQLSession; every method keeps native-session semantics.
func (l *Lease) Select(database.SQLInput) (database.SQLSelection, error)
func (l *Lease) Execute(context.Context, string) (*database.SQLExecution, error)
func (l *Lease) Close(context.Context) error
```

Executor đồng bộ counters/registry bằng mutex; reserve trước dial, rollback reservation khi fail; Close idempotent. `openCtx` chỉ giới hạn dial, `lifetime` là connection session context dài hạn; hủy timeout dial sau success không được đóng lease mới. Lifetime cancellation, explicit close và executor shutdown đều release slot đúng một lần, kể cả completion chưa được UI nhận.

`internal/query/results.go`: `func ResultSource(r *database.SQLResult, page int) grid.Source`, page 1-based, `func PageCount(r *database.SQLResult) int` trả tối thiểu 1 cho empty result. Source chỉ expose slice của snapshot, không copy/call DB; row identity theo ordinal toàn result; columns read-only, không sortable. Task1 thêm `grid.Column.ID string` (empty fallback Name); SQL Source dùng IDs `sql:0`, `sql:1`, ... ngay từ đầu, Task9 mới đổi UI consumer để dùng IDs này.

`internal/connection/manager.go`: `func (m *Manager) SQLSessions(id string) (database.SQLSessionFactory, bool)`, dùng cùng status/adapter rules như `SQL`, không mở rộng `SQLAdapter` làm vỡ các table-only adapters/tests.

### 2.3. File store/document — Tasks 5/6

`internal/storage/files.go`, `file_identity.go`:

```go
type FileIdentity struct { Path string; info os.FileInfo }
func (id FileIdentity) Same(other FileIdentity) bool
type FileRevision struct { Identity FileIdentity; Exists bool; Digest [32]byte }
type SQLFile struct {
    Path string
    Revision FileRevision
    Raw []byte
    Text string // normalized LF, without BOM
    BOM bool
    LineEnding string
}
type SQLWriteRequest struct { Path string; Data []byte; Expected FileRevision }
type SQLFiles interface {
    Read(path string) (*SQLFile, error)
    Inspect(path string) (FileRevision, error)
    Write(req SQLWriteRequest) (*SQLFile, error)
}
func NewSQLStore(defaultDir string) *SQLStore // implements SQLFiles
func DefaultQueriesDir() (string, error)
func EncodeSQL(file *SQLFile, text string) ([]byte, error)
type FileConflict struct { Current FileRevision } // implements error
```

`Inspect` đọc/hash bounded như Read; absent target có Exists=false với canonical absolute path. `FileIdentity.Same` so canonical path theo semantics platform, rồi `os.SameFile` nếu có file info; không lowercase toàn bộ paths trên mọi OS. Symlink target được resolve/validate; hardlink alias được nhận diện bằng file identity.

`internal/app/sql_document.go`: `SQLDocument{Text, SavedText string; Revision uint64; File *storage.SQLFile; FileState OpState; FileErr error}` và private file-op token; `func (d *SQLDocument) Dirty() bool` so Text/SavedText. UI buffer dùng normalized LF; File giữ raw bytes/BOM/line-ending baseline. New dùng LF, File=nil. Application state không import MyGo.

`internal/app/sql_files.go`: `SQLDialogs` interface có `OpenSQL() (string, error)`, `SaveSQL(suggested string) (string, error)`; empty path nghĩa Cancel. `SQLServices` sở hữu `Executor *query.Executor`, `Files storage.SQLFiles`, `Dialogs SQLDialogs`, app lifetime và canonical target reservations. Constructor `NewSQLServices(files storage.SQLFiles, dialogs SQLDialogs) *SQLServices`; methods của State:

```go
func (s *State) SetSQLText(tab *Tab, text string)
func (s *State) OpenSQLFile()
func (s *State) SaveSQLFile(tab *Tab, saveAs bool, then func()) bool
func (s *State) ResolveSQLFileConflict(tab *Tab, overwrite bool)
func (s *State) CloseSQL(ctx context.Context) error
```

Task6 khai báo `SQLData` tại `internal/app/sql_state.go`: `Document SQLDocument`, `Execution *database.SQLExecution`, `State SQLExecState`, `Err *database.Error`, `Result, Page, Sequence int`, private lease/session epoch và operation token. `SQLExecState` constants: `SQLNotOpened`, `SQLOpening`, `SQLRunning`, `SQLCanceling`, `SQLFinished`. Task6 tạo `func (d *SQLData) release()` nil-safe, cancel token/close lease off-thread nếu có; Task7 điền execution controller, không tạo kiểu/method trùng. `Tab.SQL *SQLData`; `State.SQLServices *SQLServices` và `State.Post func(func())` là hooks; `post` ưu tiên hook rồi fallback Conns.Post để giữ tests hiện có.

## 3. Tasks

Từng task viết tests trước code. Các bước Red/Green là commands **người dùng tự chạy**; khi chưa có output thì ghi `Not run`, không chặn bằng yêu cầu quyền lặp lại và không tự chạy thay người dùng. Bằng chứng tĩnh phải ghi rõ giới hạn.

### Task 1: Session contracts, capacity và read-only result Source

**Files:** theo hàng Task 1 ở file map. **Consumes:** `database.Adapter`, `grid.Source`, `connection.Manager` hiện có. **Produces:** toàn bộ §2.1/§2.2, chưa có native PostgreSQL factory.

- [ ] **1.1 Tests:** `TestExecutorCapacityAndRelease` dùng fake factory/session private trong `executor_test.go`: reservation thứ 9 cùng profile hoặc thứ 17 toàn app bị từ chối trước dial; fail/close/lifetime-cancel release đúng một lần. `TestExecutorOpenContextIsNotLeaseLifetime` cancel openCtx sau success vẫn Execute được, cancel lifetime thu hồi lease. Các constants được kiểm bằng boundary behavior trong Task4, không tạo tests chỉ lặp lại giá trị khai báo.
- [ ] **1.2 Tests:** `TestResultSourcePagesNeverExecute` tạo SQLResult 250 rows, page 2 có 100 rows, RowID đầu là 100, page 3 có 50; ReadOnly=true, mọi column không Editable/Sortable; empty result giữ columns và PageCount=1. `TestSQLSessionsCapability` xác nhận disconnected/table-only adapter trả false, factory adapter connected trả true.
- [ ] **1.3 Red — người dùng:** `go test ./internal/database ./internal/query ./internal/connection -run 'TestExecutor|TestResultSourcePagesNeverExecute|TestSQLSessionsCapability'`; trước implementation dự kiến FAIL do contracts chưa có.
- [ ] **1.4 Implement:** khai báo contracts/constants; Executor reserve/open/register/close; leases delegate đầy đủ Select/Execute/Close, ownership độc lập UI delivery, reject Open sau Close; ResultSource clamp page, không thực thi DB. Thêm Column.ID, accessor type assertion riêng và comment SQLAdapter.
- [ ] **1.5 Green — người dùng:** chạy lại command 1.3; mong đợi PASS. Review signatures/ownership từ source, không đánh dấu runtime pass khi chưa nhận output.

### Task 2: PostgreSQL statement boundary parser

**Files:** `statement.go`, `statement_test.go`. **Consumes:** database.SQLInput/SQLSelection. **Produces:** `func SelectPostgresSQL(input database.SQLInput, standardStrings bool) (database.SQLSelection, error)`.

- [ ] **2.1 Tests:** table-driven `TestSelectPostgresSQL` pin selection precedence; text `SELECT 'a;b'; SELECT 2;` cursor tại `2` trả đúng `SELECT 2;`; tiếng Việt/emoji trước cursor vẫn dùng byte index đúng. Cover doubled quote, E-string, U& quoted forms, nested block comments, line comments, `$tag$...;...$tag$`, `DO $$...$$;`, cursor trên semicolon/gap/EOF, comment-only no-op, invalid offsets/UTF-8 và nhiều nonempty selections bị từ chối.

```go
got, err := SelectPostgresSQL(database.SQLInput{
    Text: "SELECT 'a;b'; SELECT 2;", Cursor: 21,
}, true)
if err != nil || got.SQL != "SELECT 2;" {
    t.Fatalf("selection=%+v err=%v", got, err)
}
```

- [ ] **2.2 Tests:** `TestSelectPostgresSQLSessionEscaping` chạy cùng input với standardStrings=true/false để pin boundary khác nhau; `TestUnsupportedScriptNeedsSelection` không đoán SQL-standard BEGIN ATOMIC body, selection whole block vẫn trả nguyên SQL; psql meta và top-level COPY STDIN/STDOUT trong selected multi-statement bị unsupported trước dispatch. Unterminated quote/comment khi không có selection báo validation/fallback.
- [ ] **2.3 Red — người dùng:** `go test ./internal/database/postgres -run 'TestSelectPostgresSQL|TestUnsupportedScriptNeedsSelection'`; dự kiến FAIL khi parser chưa có.
- [ ] **2.4 Implement:** scanner có lexical states và nested-comment depth, nhận dollar tags chỉ tại token boundary, xử lý UTF-8 byte offsets; selections rỗng không tính là selected blocks. Không phân tích SQL để quyết định read/write/paging; không concatenate rời rạc. Lexical preflight cho selected SQL chỉ chặn unsupported client modes, không split để Execute.
- [ ] **2.5 Green — người dùng:** chạy lại 2.3; mong đợi PASS. Không đưa parser dialect này sang shared fallback cho engine khác.

### Task group 3–4: Dedicated PostgreSQL sessions và bounded execution

#### Slice 3: Session ownership

**Files:** theo hàng Task 3. **Consumes:** SQLSessionFactory, Executor, parser Task 2; existing `buildConfig`, connection epoch/lifetime. **Produces:** `(*postgres.Adapter).OpenSQLSession(context.Context) (database.SQLSession, error)`; private `sqlSession` implements SQLSession, Execute được wire Task 4.

- [ ] **3.1 Tests:** `TestSQLSessionRegistryClosesUndeliveredSessions`, `TestSQLSessionOpenRacesAdapterClose`, `TestSQLSessionCloseIsIdempotent` dùng injectable private dial function `func(context.Context,*pgx.ConnConfig)(*pgx.Conn,error)` và protocol peer ở `sql_session_test.go`; tạo barrier để Close chạy trước registration/delivery, assert physical close và không còn registry entries. Không dùng sleep để đoán ordering.
- [ ] **3.2 Red — người dùng:** `go test ./internal/database/postgres ./internal/query ./internal/connection -run 'TestSQLSession|TestExecutor|TestSQLSessionsCapability'`; dự kiến FAIL cho methods chưa có.
- [ ] **3.3 Implement:** Adapter giữ private copy của pinned pgx config/profile redaction context, parent cancel context và session registry; mở bằng `pgx.ConnectConfig`, không dùng `a.db.Conn`. Registry nhận session trước return; Adapter.Close mark closed/cancel opens, snapshot sessions rồi đóng đồng thời với một cleanup deadline. Failed/stale Open đóng native connection và trả slot; không giữ lock khi network IO. `sqlSession.Select` gọi parser với ParameterStatus `standard_conforming_strings`; không query setting trong failed transaction.
- [ ] **3.4 Implement:** session chỉ một Execute; Close concurrency-safe bằng cancel active work rồi chờ owner với deadline; fallback đóng underlying transport an toàn, không gọi concurrent pgx methods. Native Close/lease Close không cần UI callback. Xóa config/session references sau cleanup, không log config/SQL/password.
- [ ] **3.5 Review ownership:** kiểm registry/dial/close code cùng slice4 trước handoff; không coi slice3 hoàn tất riêng hoặc thêm Execute stub. Green của cả group ở bước4.8. PostgreSQL thật và pool starvation được kiểm ở Task10.

#### Slice 4: Execute một lần với bounded protocol, values và command evidence

**Files:** theo hàng Task 4. **Consumes:** session/parser/contracts. **Produces:** `sqlSession.Execute`, `func classifySQLExecution(ctx context.Context, err error, secret string) *database.Error` trong `sql_errors.go`; helpers private về budgets/decoding trong `sql_protocol.go`/`sql_values.go`.

- [ ] **4.1 Tests:** protocol peer bằng net.Pipe/pgproto3 trong `sql_protocol_test.go` phát RowDescription/DataRow/CommandComplete/ReadyForQuery có kiểm đếm query messages. `TestSQLExecuteMultipleResultsAndTags`: selected block gửi đúng một Query, empty SELECT giữ columns, DDL không invented affected=0, UPDATE RETURNING có rows lẫn count. `TestSQLExecuteValues` pin NULL≠empty, bytea hex/escape binary size, numeric `9007199254740993.12345` nguyên text và unknown OID fallback.
- [ ] **4.2 Tests:** `TestSQLExecuteBoundedRead` cover limit-1/limit/limit+1 cho snapshot rows/bytes, global rows/bytes, 32 results, 4.096 columns, metadata, 8 MiB frame header; peer không cần allocate huge body để test rejected header. `TestSQLNoticeAndNotificationFlood` budget gồm notices cả discarded, giữ tối đa 32×4 KiB, không dùng pgx notification queue unbounded; `TestSQLCopyModeStops` COPY mode bất ngờ kết thúc bằng bounded cleanup.
- [ ] **4.3 Tests:** `TestSQLCancelVersusConfirmedCompletion`, `TestSQLCommitResponseLost`, `TestSQLDrainStopsAtBudget` assert execution evidence tồn tại cùng error, confirmed completion không bị đổi thành not-written, partial snapshot có Stop phù hợp, unknown commit báo uncertain, không Query retry, protocol Close không tiếp tục đọc vô hạn. Peer chặn sau cancel phải dẫn tới physical close trong cleanup deadline với tolerance test rõ.
- [ ] **4.4 Red — người dùng:** `go test ./internal/database/postgres ./internal/query -run 'TestSQLExecute|TestSQLNotice|TestSQLCopy|TestSQLCancel|TestSQLCommit|TestSQLDrain'`; dự kiến FAIL trước implementation.
- [ ] **4.5 Implement:** dùng `PgConn().Exec` simple-query, duyệt MultiResultReader/ResultReader bằng NextResult/NextRow, copy bounded values trước next row; không dùng ReadAll/Read. Đếm metadata trước lưu, truncate notices, map types không query phụ. Chỉ gán CommandComplete khi nhận command tag; terminal ReadyForQuery mới xác nhận protocol settled. SQLSTATE errors dùng categories chung nhưng message dành SQL execution, không reuse hint table metadata/save sai context; sanitize secret cả query errors.
- [ ] **4.6 Implement:** instrument plaintext reader qua pgx Config.BuildFrontend bằng frame-aware reader để charge mọi message body, kể cả discard/drain/notices; set MaxProtocolMessageBodyLen=8 MiB. Reader không giữ thêm cả result; ngưỡng read rows/bytes áp dụng cả khi driver tự drain. Limit hit cancel và cho phép session lost nếu phải ngắt reader; result giữ SQLLimitReached thay vì che thành generic network error. Threshold detection cho phép tối đa một bounded message theo spec; không đòi server response sau khi đã hết budget.
- [ ] **4.7 Implement:** dùng CancelRequestContextWatcherHandler với CancelRequestDelay=0, DeadlineDelay=2s, giữ mặc định FATAL handling; toàn cleanup cùng deadline, không cộng deadline mỗi Close. Snapshot16MiB chia cả groups, mỗi group1000 rows; hết snapshot chỉ discard trong hard read budget. Sau terminal/cancel xác nhận TxStatus I/T/E hoặc SessionLost; mọi command/status trước lỗi được giữ, không suy diễn committed từ RowsAffected.
- [ ] **4.8 Green — người dùng:** chạy lại 3.2 và 4.4 sau khi có full native SQLSession; mong đợi PASS. Review cả group3–4 một lần; peer tests là protocol simulation, không thay thế PostgreSQL integration.

### Task 5: SQL file store, encoding và replacement theo platform

**Files:** theo hàng Task 5. **Consumes:** os/path/file IO primitives; không import UI/database. **Produces:** §2.3 storage API; private `func replaceSQLFile(tmp, target string, replaceExisting bool) (replaced bool, err error)`.

- [ ] **5.1 Tests:** `TestSQLFileRoundTrip` dùng t.TempDir, assert `bytes.Equal(saved.Raw, original)` cho UTF-8 BOM, CRLF và mixed endings khi text không sửa; new/edited text serialize LF hoặc original dominant line-ending convention. `TestSQLFileBounds` check 8MiB±1, invalid UTF-8/NUL, directories/special paths, traversal khi ghép basename; Read/Inspect dùng LimitReader(max+1), không os.ReadFile rồi mới check size.
- [ ] **5.2 Tests:** `TestSQLFileIdentityAliases` canonical path, Windows path comparison, symlink/hardlink với skip khi OS/quyền không cho tạo; `TestSQLFileExternalChangeConflict` same-size/same-mtime content vẫn conflict bằng SHA-256, deleted target cũng conflict. `TestSQLFileReplacementFailureKeepsOriginal` inject writer/replace failure trước replacement, assert original bytes và cleanup temp; post-replacement error không báo chắc chắn file cũ còn nguyên.
- [ ] **5.3 Red — người dùng:** `go test ./internal/storage -run 'TestSQLFile'`; dự kiến FAIL trước implementation.
- [ ] **5.4 Implement:** canonical absolute target, reject Windows device/ADS và special file; resolve parent/symlink target, store physical identity cùng canonical path, không auto allow traversal chỉ vì Clean đã chạy. Hash bounded raw bytes, normalize BOM/CRLF để tạo Text cho editor, giữ Raw immutable cho no-edit/Undo-to-baseline save. `EncodeSQL` trả Raw cũ nếu normalized text==File.Text; edited text dùng recorded line-ending convention/BOM; check encoded bytes≤8MiB.
- [ ] **5.5 Implement:** Write compare Expected revision ngay trước replace; conflict trả FileConflict.Current để caller xin overwrite riêng. Temp cùng directory, Write/Sync/Close; Windows dùng kernel32 ReplaceFileW cho existing target, MoveFileExW không replace flag cho new target (standard syscall, không thêm x/sys). `!windows`: os.Rename cho overwrite; new target dùng exclusive link/install để không overwrite file xuất hiện sau Inspect. Không delete target/truncate fallback. Capture replaced bool và reread/reconcile khi lỗi sau replace; không trả false claim clean/unchanged. Không sửa atomic.go hoặc semantics settings/catalog.
- [ ] **5.6 Green — người dùng:** chạy lại 5.3; mong đợi PASS trên Windows target; báo OS skips. Detection external edit là best effort, không hứa khóa external processes hoặc xóa mọi TOCTOU race.

### Task 6: SQL documents, async Open/Save và native dialog adapter

**Files:** theo hàng Task 6. **Consumes:** SQLFiles/SQLDocument/SQLServices API §2.3, existing Workspace tabs/poster. **Produces:** file actions §2.3 và `func (sh *Shell) SetWindow(w *mygo.Window)` trong `internal/ui/sql_files.go` để native adapter có parent window.

- [ ] **6.1 Tests:** `TestSQLDocumentDirtyUndoAndNewEmpty` pin New empty clean, edit dirty, undo-to-baseline clean; `TestSQLOpenFocusesExistingFile` aliases focus tab, không Execute/connect. `TestSQLSaveUsesCapturedRevision` file store barrier: edit tiếp trước completion vẫn dirty, saved baseline là bản thực sự đã ghi, then callback không chạy khi revision/content chưa sạch. Dùng queuePost hiện có cho deterministic completion.
- [ ] **6.2 Tests:** `TestSQLSaveAsReservations` hai tabs cùng target kể cả alias/path trước first write phải từ chối tab thứ hai; `TestSQLSaveConflictCancelAndOverwrite` cancel giữ buffer/path, overwrite dùng Current revision và conflict lại nếu disk tiếp tục đổi. `TestSQLSaveFailureKeepsAssociation` Save As lỗi không đổi File; callback stale không update tab khác. Native dialogs được inject fake, không mở GUI trong tests.
- [ ] **6.3 Red — người dùng:** `go test ./internal/app -run 'TestSQLDocument|TestSQLOpen|TestSQLSave'`; dự kiến FAIL trước implementation.
- [ ] **6.4 Implement:** NewSQL tạo SQLData.Document; Tab.Dirty dispatch theo Kind, không dùng table Drafts cho SQL. Workspace.Close gọi SQLData release hook; file-op token kiểm tab pointer/document/op identity trước UI apply. Thêm State.Post fallback trong ops.go; NewSQLServices có app lifetime, registry và path reservations, CloseSQL cancel services/close executor mà không đợi UI delivery.
- [ ] **6.5 Implement:** Open/Dialog/Inspect/Write off-thread rồi post; reserve canonical target trước write và giữ reservation qua conflict prompt, release khi cancel/error/complete. Save clean associated file vẫn được explicit Ctrl+S; Save new empty vẫn mở dialog. Sau write success cập nhật File/saved text đúng snapshot, text mới giữ nguyên. File errors map file-access message, buffer/path cũ giữ nguyên.
- [ ] **6.6 Implement:** native adapter dùng mygo.Dialog.Open/Save gắn Parent, default ~/.dbl/queries/, suggested filename Query-N.sql, filter sql; empty result Cancel. main wiring service/store/poster trước window, SetWindow trong WhenReady. Mất home directory vẫn cho Open/Save As ở path chọn rõ; hiển thị lỗi default root, không silently fallback CWD. Không auto connect khi Open.
- [ ] **6.7 Green — người dùng:** chạy lại 6.3; mong đợi PASS; native dialogs/manual encoding được nghiệm thu Task 10.

### Task 7: Application execution, fake SQL sessions và epoch gates

**Files:** theo hàng Task 7. **Consumes:** Executor, manager accessor, SQLSession, SQLData.Document. **Produces:** `State.ExecuteSQL(tab *Tab, input database.SQLInput, allowNewSession bool) bool`, `State.CancelSQL(tab *Tab)`, `State.SetSQLResultPage(tab *Tab, result, page int)`; result index 0-based, page 1-based, SQLData mặc định Result=0/Page=1. Tái sử dụng SQLData.release từ Task6.

- [ ] **7.1 Tests:** tạo fakedb SQL sessions scripted, configurable via `func (a *Adapter) QueueSQLSession(session *SQLSession)`; fake `fakedb.SQLSession` implements Select/Execute/Close, records input, supports channels để pause, scripted `database.SQLExecution+error` và `func (s *SQLSession) CloseCount() int`; factory absent script trả unsupported để existing tests không tự chạy SQL. `TestSQLExecutionUsesIndependentTabSessions` BEGIN/next SQL giữ cùng session, tab khác lấy session khác; Execute khi busy bị từ chối, SetSQLResultPage không tăng calls.
- [ ] **7.2 Tests:** `TestSQLExecutionDropsStaleEpoch`, `TestSQLCancelKeepsTerminalStatus`, `TestSQLCloseBeforeDeliveryReleasesLease`, `TestSQLReconnectNeedsExplicitConsent`: result cũ không thay new session, canceled/timeout hiện terminal thay vì bị read gate nuốt, error kèm confirmed command evidence vẫn giữ, session lost Execute(false) không dial/dispatch, Execute(true) mở mới với warning được xác nhận.
- [ ] **7.3 Red — người dùng:** `go test ./internal/app ./internal/database/fakedb ./internal/query -run 'TestSQLExecution|TestSQLCancel|TestSQLClose|TestSQLReconnect|TestExecutor'`; dự kiến FAIL trước implementation.
- [ ] **7.4 Implement:** execute flow snapshot tab/input/revision/epoch → lazy lease Open với openCtx theo timeout profile và lifetime từ Manager.SessionContext → session.Select → empty no-op hoặc Execute context60s. Store token trước worker; result cũ/native session trả muộn bị close qua registry/lease ngay cả post không chạy. Không reuse metadata read `op.finish` để lọc mất canceled/timeout/write evidence.
- [ ] **7.5 Implement:** SQLData giữ explicit states `SQLNotOpened`, `SQLOpening`, `SQLRunning`, `SQLCanceling`, `SQLFinished`; Transaction/Stop lấy từ execution. Cancel chỉ gọi context cancel, không clear token/session ngay khi chưa có terminal. New Execute thay old snapshot; page/result switch update local source/sequence. Không regenerate SQL/refresh table data sau writes.
- [ ] **7.6 Green — người dùng:** chạy lại 7.3; mong đợi PASS. Test fake boundary selector là stub với inputs/outputs định trước, không viết lại PostgreSQL scanner trong fake.

### Task 8: Coordinated Save/Close/Quit/connection guards

**Files:** theo hàng Task 8. **Consumes:** document save, execution terminal state, existing table SaveAll/PendingConn. **Produces:** `State.RequestSQLConnection(tab *Tab, connectionID string) bool`, `State.ResolveSQLAction(choice SQLActionChoice)`; SQLActionChoice constants `SQLActionCancel`, `SQLActionStopQuery`, `SQLActionSaveDocument`, `SQLActionDiscardDocument`, `SQLActionRollback`; private pending-action coordinator với stable tab IDs/revisions.

- [ ] **8.1 Tests:** `TestSQLGuardActionMatrix` table-driven Close/Quit/SwitchConnection/Disconnect/Delete/EditProfile × running/file-saving/dirty/in-tx/failed-tx; order wait-save → stop-query → save/discard text → rollback-confirm → action. Save dialog Cancel, Save error hoặc re-dirty đều không được đóng tab; Save thành công chỉ cho tiếp tục nếu hết các guards còn lại. Discard+switch giữ text dirty, Discard+close bỏ document; transaction prompt vẫn cần sau Save file.
- [ ] **8.2 Tests:** `TestSQLGuardMixedTabsStopOnFirstFailure` SQL+table saves đúng backend, fail tab đầu không chạy action; completed file Save ở tab trước không bị undo. `TestSQLGuardRevalidatesDuringPrompt` edit thêm/switch session trong lúc prompt làm reevaluate thay vì dùng approval cũ. `TestSQLGuardRollbackDeadlineAndUnknownWrite` không tự COMMIT, stop query khác rollback; uncertain outcome còn hiển thị tới khi người dùng quyết định tiếp.
- [ ] **8.3 Red — người dùng:** `go test ./internal/app ./internal/ui -run 'TestSQLGuard|TestGuard|TestSaveAnd'`; dự kiến FAIL cho SQL guard cases mới.
- [ ] **8.4 Implement:** RequestClose/RequestQuit/RequestDisconnect/RequestDelete và luồng ConnApplyEdit trong connections.go giữ public signatures, route SQL tabs qua coordinator trước destructive continuation; revalidate target/lifetime/revision mỗi stage. Không gọi Workspace.CloseConnection hoặc existing ConfirmClose bypass SQL guards; direct internal Workspace.Close vẫn release resources để tests/forced teardown an toàn.
- [ ] **8.5 Implement:** SaveAll dispatch SQL→SaveSQLFile, table→Save; không sửa table transaction/concurrency policy. Callback Save chỉ continue khi current content clean. SQL session in tx hỏi rollback riêng; rollback/close async deadline2s; connection switch giữ document/path, clear result/session only after confirmed cleanup. UI có nhãn hậu quả riêng cho Discard và transaction; đóng dialog nghĩa Cancel. Profile/quit chỉ apply sau tất cả tabs sẵn sàng.
- [ ] **8.6 Green — người dùng:** chạy lại 8.3; mong đợi PASS; source review toàn existing table guard callers để không regression Phase 2.3.

### Task 9: SQL editor toolbar/results, duplicate columns và context shortcuts

**Files:** theo hàng Task 9. **Consumes:** app actions Tasks 6–8, result Source. **Produces:** `sqlView.syncDocument(tab *app.Tab)`; `sqlView.buildResults(c *mygoui.Context, t *mygoui.Theme, sh *Shell, tab *app.Tab)` trong sql_results.go; `func (sh *Shell) SyncSQLDocuments()` và `func (sh *Shell) RequestQuit() bool` trong shell.go (sync buffers rồi gọi State.RequestQuit); optional grid column IDs qua `grid.Column.ID string` (empty fallback Name).

- [ ] **9.1 Tests:** mở rộng ui.Tester `TestSQLTabsKeepSeparateBuffers`, `TestSQLTabUsesSQLLanguageAndLineNumbers`; thêm `TestSQLToolbarConnectionAndExecution`, `TestSQLFileActionsAndDirtyMarker`, `TestSQLResultsAreReadOnly`, `TestSQLDuplicateColumnIDs`, `TestSQLShortcutContextAndModal`. Assert Ctrl/Cmd+Enter đúng selection/byte offsets, Ctrl/Cmd+S SQL ghi file/table save giữ nguyên, F2/Escape/F5 chỉ đúng context, Ctrl+Shift+F không action, modal chặn shortcuts nền. SQL results có hai cột cùng Name nhưng IDs/width/selection tách biệt.
- [ ] **9.2 Tests:** `TestSQLBufferCRLFBOMAndInactiveDirtyGuard` dùng file BOM/CRLF, edit rồi chuyển tab và Shell.RequestQuit trước frame mới không mất dirty; assert no-edit Save exact Raw, Unicode caret selection đúng. `TestSQLQuitGuardUsedForAppAndWindowClose` gọi cùng Shell.RequestQuit boundary cho cả hai entry paths, assert dirty/running/in-tx đều giữ window/app và clean confirmed path mới cho quit. `TestSQLPartialAndUnknownOutcomePresentation` phân biệt Completed/truncated, Failed, Canceled, TimedOut, LimitReached, uncertain outcome và Transaction failed; không show result cũ như kết quả của Execute mới.
- [ ] **9.3 Red — người dùng:** `go test ./internal/ui -run 'TestSQL|TestClosingSQL|TestCtrlN|TestTableInspectorBar'`; dự kiến FAIL với các controls/states chưa có.
- [ ] **9.4 Implement:** bind existing MyGo editor Buffer thay vì viết editor mới. Buffer offsets đã là UTF-8 bytes, không rune-convert lần nữa; Buffer.String normalized LF; dùng document Raw/BOM/line-ending baseline của storage để serialize. Track Buffer.Version, sync document ngay sau editor input và tại UI/window close boundary qua hook `Shell.SyncSQLDocuments()` gọi trước RequestQuit; không quét/copy toàn bộ buffers mỗi frame.
- [ ] **9.5 Implement:** toolbar rõ profile/database/status, active connection selector qua guard, Execute/Cancel/Open/Save/Save As; splitter editor/results giữ per-view state. Result selector/pages local100, columns visible khi empty, notices/status/affected count và limit labels; busy không cho Execute tiếp, Save file độc lập query nếu guards cho phép. Grid view riêng cho SQL results; reset selection/scroll khi result/page sequence đổi.
- [ ] **9.6 Implement:** UI grid dùng Column.ID đã có từ Task1 cho TableColumn.ID/column signature (empty fallback Name), giữ Name cho display và existing table sort mapping. SQL Source IDs theo ordinal `sql:0`, `sql:1`, ...; không thay table IDs/semantics. Context dispatch shortcuts trước/đúng editor, F5 chỉ gọi ReloadTable qua existing guards; Ctrl+N/new-SQL Explorer/Table actions vẫn giữ hành vi đã có. Modal native/app có busy flag chặn background shortcuts.
- [ ] **9.7 Green — người dùng:** chạy lại 9.3; mong đợi PASS; ui.Tester không thay thế app manual test trên Windows.

### Task 10: PostgreSQL integration, shutdown, documentation và phase handoff

**Files:** theo hàng Task 10; tests trước final wiring/docs. **Consumes:** toàn bộ Tasks 1–9. **Produces:** integration_sql_test.go, user runbook trong README, trạng thái phase phản ánh bằng chứng thực tế.

- [ ] **10.1 Tests:** dùng integrationProfile(t) hiện có; mỗi test tạo schema/table riêng và cleanup bằng connection khác, không dùng production. `TestIntegrationSQLTransactionAcrossExecutes`: BEGIN→write→observer chưa thấy→COMMIT→observer thấy; error→TransactionFailed→ROLLBACK; COMMIT trên failed tx nhận đúng tag ROLLBACK. `TestIntegrationSQLSessionIsolation`: temp objects/settings giữa hai tabs tách biệt, pool MaxOpen=1 vẫn đọc metadata khi SQL sessions giữ transaction.
- [ ] **10.2 Tests:** `TestIntegrationSQLResultsAndCancel`: empty/multi-results, DO dollar quotes, RETURNING, numeric/binary, large SELECT snapshot1000, page100 không requery, pg_sleep cancellation và reuse/session-lost outcome đúng. `TestIntegrationSQLCloseRollsBack`: đóng SQL session bỏ uncommitted write và không block pool. Hard read limits chủ yếu pin bằng protocol peer; COMMIT response loss dùng peer deterministic Task4, không giả vờ integration thường tạo được race này.
- [ ] **10.3 Red — người dùng khi có DB disposable:** `go test ./internal/database/postgres -run '^TestIntegrationSQL' -count=1 -v`; trước completion dự kiến FAIL; thiếu env dự kiến SKIP và không tính PostgreSQL pass.
- [ ] **10.4 Implement final wiring:** window OnClose và application OnBeforeQuit đều gọi Shell.RequestQuit; PreventDefault khi guards còn pending, invalidate window để hiện dialog. Dùng confirmed-quit flag chỉ sau coordinator hoàn tất để resume app Quit mà không lặp prompt; không đặt flag ngay khi mở dialog. OnQuitConfirmed/OnWillQuit/OnClosed dùng idempotent shutdown. SQL service.Close và conns.CloseAll chia deadline tổng2s, chạy đồng thời thay vì tuần tự nhân deadline theo tab; không dùng UI post để giải phóng. Không cho actual window destroy đi qua khi pending save/guards chưa hoàn tất. MyGo v0.5.5 có OnBeforeQuit và preventable QuitEvent theo local pinned source; native OS termination cưỡng chế không được hứa là có prompt.
- [ ] **10.5 Update docs:** README Features/Database Support/Local Configuration thêm execution/file workflow và queries directory; hướng dẫn session/BEGIN-COMMIT-ROLLBACK, selected block fallback, limits, UTF-8 và file errors. Giữ claim ~50MB owner-reported hiện có, không benchmark mới. implementation-phases.md chỉ thêm kết quả2.4 đúng source/status; không tick manual checklist hoặc tuyên bố MVP nghiệm thu trước người dùng. Ghi changed files, known limits và checks Not run/skipped/user output trong kết quả plan này.
- [ ] **10.6 Green — người dùng:** chạy lại 10.3; mong đợi PASS với DB env đầy đủ. Sau đó chạy bộ checks/manual runbook bên dưới một lần; nếu có lỗi mới thì sửa trong scope, không tự broad/repeat verification.
- [ ] **10.7 Handoff:** source/diff review inline toàn phase theo Review Focus, giữ unresolved findings rõ; chờ user test app. Không commit/push hoặc triển khai Phase3.

## 4. Commands và manual acceptance cho người dùng

Chạy từ root repo sau khi code/tests tồn tại. Không có command nào ở đây đã được agent chạy. Khi người dùng chưa cung cấp output, Red/Green giữ Not run; không suy ra PASS từ review source.

```powershell
# Unit/UI tests; PostgreSQL integration tự skip nếu không có env.
go test ./...
go build ./...
go vet ./...

# Khi đã cấu hình DBL_TEST_PG_HOST, DBL_TEST_PG_USER, DBL_TEST_PG_DB
# cùng DBL_TEST_PG_PORT/PASSWORD/TLS nếu cần, trỏ tới DB disposable.
go test ./internal/database/postgres -run '^TestIntegrationSQL' -count=1 -v

# Người dùng test app thật.
go run .
```

Formatting khi implementation: chỉ `gofmt -w` trên các Go files đã chỉnh trong task; đây là bước format ghi file trong approved edit scope, không là bằng chứng test/build. Không chạy gofmt cả repo hoặc đổi unrelated files. Nếu người dùng cấm cả formatting commands ở lúc thực thi, cung cấp danh sách command để người dùng tự chạy.

Manual checklist:

- [ ] Connect → Explorer → đọc/sửa table → Save/Revert vẫn hoạt động; F2/Escape/F5 và guarded table actions không regression.
- [ ] Mở hai SQL tabs cùng profile, BEGIN/write/COMMIT ở một tab, ROLLBACK ở tab khác; rõ connection/database/transaction state, query không chặn grid.
- [ ] Execute selection/current statement với comments, strings/dollar quotes/Unicode; nhiều results/0 rows/RETURNING; pages không phát lại writes.
- [ ] Cancel/timeout/result limit/permission/syntax error; biết rõ completed/truncated/partial/uncertain, buffer vẫn nguyên, session lost không âm thầm reconnect.
- [ ] First Save/Save As/Open, Cancel dialog, UTF-8 BOM/CRLF, existing target Windows, external file edit, lỗi quyền/path, concurrent Save As và edit-during-save.
- [ ] Dirty SQL + running query + transaction + table drafts: Close/SwitchConnection/Disconnect/Delete/EditProfile/Quit có đủ Save/Discard/Cancel và rollback confirmation; failure dừng action.
- [ ] Repeated open/close/reconnect/quit không để session trong pg_stat_activity vượt policy sau cleanup; shutdown không nhân2s theo số tabs. Đây là manual evidence, không tự ghi PASS.

## 5. Spec coverage và tự review plan

| Spec sections | Tasks bao phủ |
| --- | --- |
| 1–3: scope, existing code, layering | File map, Global Constraints, Tasks 1/6/7/9 |
| 4: capacity/session/transaction/cleanup | Tasks 1/3/4/7/8/10 |
| 5: selection/parser/unsupported scripting | Task 2; integration/UI Tasks 9/10 |
| 6: execution/budgets/outcome/presentation | Tasks 1/4/7/9/10 |
| 7: document/file identity/replacement | Tasks 5/6/9 |
| 8: pending actions/mixed tabs | Task 8; shutdown Task 10 |
| 9: keyboard/context/editor | Task 9 |
| 10–12: file scope, acceptance, handoff | File map, Task 10, user commands, §6 |

Plan được tự rà soát bằng đọc source/spec: shared signatures thống nhất; tests gắn Review Focus ở tasks sở hữu; không để TODO/TBD hoặc implementation phụ thuộc helper chưa có task định nghĩa. Các fake/protocol fixture helpers private được tạo trong đúng test file của task, không thành production abstractions. Đây là review tài liệu, chưa phải xác nhận code/tests tồn tại hoặc chạy thành công.

## 6. Approval và execution handoff

Trạng thái: **Native đã được chọn; source implementation đã bàn giao — chờ người dùng chạy checks, kiểm tra app và nghiệm thu**. Branch `feat/phase-2-4-sql-editor-local-files`, base `938e019`. Không commit/push. Task checkboxes giữ trống vì các checkpoint Red/Green và manual acceptance chưa chạy; chúng không phải bằng chứng mọi assertion đã được bao phủ hoặc đạt.

### 6.1. Kết quả source implementation

| Nhóm | Files chính | Kết quả |
| --- | --- | --- |
| Contracts, leases và snapshots | `internal/database/sql_execution.go`, `internal/query/`, `internal/grid/source.go`, `internal/connection/manager.go` | Session capability tách khỏi table API; capacity và lifetime; kết quả chỉ đọc, page cục bộ, column IDs theo ordinal |
| PostgreSQL | `internal/database/postgres/statement.go`, `sql_session.go`, `sql_execute.go`, `sql_protocol.go`, `sql_values.go`, `sql_errors.go`, `connect.go` | Parser byte offsets; dedicated session; bounded protocol reads kể cả discarded messages; transaction/command evidence và uncertain outcome |
| Local SQL files | `internal/storage/files.go`, `file_identity.go`, `file_replace_windows.go`, `file_replace_other.go` | UTF-8/BOM/endings, revision fingerprints, file identity, safe replacement và failure handling |
| Application | `internal/app/sql*.go`, `state.go`, `workspace.go`, `connections.go`, `save.go`, `ops.go`, `internal/database/fakedb/` | Async file/query actions; epoch gate; per-tab lifetime; independent transaction state; mixed-tab guards |
| Native UI và shutdown | `internal/ui/sql*.go`, `shell.go`, `workspace.go`, `dialogs.go`, `tabs.go`, `grid.go`, `main.go` | Toolbar, native file dialogs, result selector/pages, shared window/app quit boundary và bounded cleanup |
| Documentation | `README.md`, `docs/implementation-phases.md`, spec và plan này | Workflow/limits/runbook; Phase 2.4 chưa được nghiệm thu |

Tests đã được viết cho lease capacity/lifetime, lexical boundaries, protocol limits/COPY/notice flood, command evidence khi mất Ready, cancellation, file round trips/conflicts/replacement failure, save races, tab isolation, stale epochs, guard entry points và UI column IDs/buffer/quit boundary. Có thêm PostgreSQL integration tests dùng database disposable. **Tất cả chưa chạy**; các tests này chưa chứng minh đầy đủ acceptance matrix.

### 6.2. Independent review và xử lý

Một independent read-only reviewer đã review toàn nhóm thay đổi; không phát hiện Critical. Hai Important findings đã được sửa trong source và thêm regression tests, chưa chạy:

1. Empty/rejected selection từng xóa snapshot chứa transaction status, làm mất rollback guard. `SQLData.Transaction` hiện lưu độc lập với snapshot. `TestSQLSelectionFailureKeepsTransaction` bao phủ transaction đang mở/failed và selection rỗng/lỗi.
2. Session mở xong nhưng callback UI không được nhận từng có thể sống tới khi profile/app đóng. Tab hiện sở hữu cancelable session lifetime trước khi worker mở session; đóng tab thu hồi lease dù callback bị bỏ. `TestSQLCloseBeforeDeliveryReleasesLease` bao phủ đường này.

Minor finding còn lại, **deferred**: `internal/ui/sql_results.go` return sớm khi không có result group nên có thể ẩn retained notices và số notices bị bỏ; ví dụ một DO block phát notice rồi lỗi trước result đầu tiên. Chưa sửa trong lượt xử lý Important findings của Native workflow.

Main agent đã đọc lại source các fixes, guards và lifetime paths sau review; không dispatch re-review, không coi source review là runtime validation.

### 6.3. Điều chỉnh thực hiện và bằng chứng còn thiếu

- Thực hiện inline trên feature branch tại checkout người dùng thay vì tạo worktree riêng; không có filesystem isolation. Giữ các tài liệu đã tạo và toàn bộ thay đổi chưa commit để người dùng test tại checkout này.
- Session lifecycle/protocol cases được gom trong `sql_execute_test.go`; fake/session/manager behavior được kiểm tra qua query/app tests thay vì tạo mọi test file trong file map. Một số cases dùng tên/table-driven grouping khác bản plan; không tuyên bố từng planned test name đã tồn tại.
- Bổ sung transaction state độc lập, per-tab lifetime và revalidation khi prompt cũ gặp document/session thay đổi để đáp ứng contracts đã duyệt. Không đổi dependencies hoặc phạm vi engine/UI stack.
- Chỉ chạy `gofmt -w` trên Go files đã chỉnh như bước format được phép; README giữ LF để tránh diff toàn file. Không dùng formatting làm bằng chứng correctness.
- Build, test, vet/lint, race checks, real PostgreSQL, native Windows dialogs/replacement, cancellation timing, memory measurement và manual app acceptance: **Not run** theo chỉ dẫn người dùng. Không có kết quả PASS được suy ra.
- Unknown write outcome cần acknowledgement riêng trong app; đây không phải xác nhận rollback hay khôi phục transaction.

Người dùng chạy commands và manual checklist tại §4. Integration thiếu env phải báo skipped, không tính PostgreSQL pass. Sau khi người dùng kiểm tra app, chỉ commit khi có yêu cầu rõ ràng; không chuyển Phase 3 từ bàn giao này.

# Phase 3.1 — SQLite và bước chọn loại database: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tạo kết nối SQLite qua bước chọn loại (icon + tên) rồi form theo loại, và làm việc với file SQLite cục bộ (xem, lọc, sửa ô, Save/Revert, chạy SQL) như PostgreSQL.

**Architecture:** Thêm adapter `internal/database/sqlite/` implement `database.SQLAdapter` và `database.SQLSessionFactory` bằng `database/sql` + `ncruces/go-sqlite3/driver`. `main.go` chọn connector theo `Profile.Driver`. `connection.Profile` thêm `SQLitePath`/`ReadOnly`. UI thêm bước chọn loại trước form; form đổi các ô theo loại. Giao diện chung (explorer, tab, grid, SQL editor) giữ nguyên, chỉ sửa chỗ gắn cứng PostgreSQL.

**Tech Stack:** Go 1.27.2, MyGo v0.5.5 (`ui.Tester`), `github.com/ncruces/go-sqlite3` (không CGO, người dùng đã đồng ý thêm), `database/sql`.

**Spec:** [2026-10-17-phase-3-1-sqlite-design.md](../specs/2026-10-17-phase-3-1-sqlite-design.md). Nguồn gốc: [implementation-phases.md](../../implementation-phases.md) mục 10, [specs.md](../../specs.md) FR-001/002, 6.1, 8.4.

## Global Constraints

- Go `1.27+` (`go.mod` hiện `go 1.27.2`); build **không CGO** (`CGO_ENABLED=0 go build ./...` phải qua).
- Database work ngoài UI thread; cập nhật UI qua `Window.Update`; kết quả cũ bị completion gate loại (P0-C). Mọi thao tác dài nhận `context.Context`.
- SQL sinh ra chỉ dùng bound parameters và quoting theo SQLite; **không bao giờ tự retry write**.
- Lỗi dùng category của `internal/database/errors.go` (`KindConnection`, `KindPermission`, `KindFileAccess`, `KindSyntax`, `KindTimeout`, `KindCanceled`, `KindConflict`, `KindSave`...), không lộ nội dung nhạy cảm.
- Không lưu mật khẩu/token cho SQLite; không đụng credential store với loại này.
- Không dùng `rowid` làm định danh dòng: theo `TableMetadata.ResolveIdentity()` hiện có.
- Giữ nguyên giá trị loaded/display/draft; không so sánh qua chuỗi hiển thị.
- Chỉ mở file có sẵn: DSN dùng `mode=rw` hoặc `mode=ro`, không bao giờ `rwc`.
- Không đổi `journal_mode` của file người dùng.
- Theo [AGENTS.md](../../../AGENTS.md): **không commit/push khi chưa được yêu cầu**; ở mỗi Checkpoint dừng để người dùng tự test app. Được chạy `go build`, `go vet`, `go test`, `gofmt -w`. Thay đổi dependency ngoài `ncruces/go-sqlite3` phải hỏi.
- Các bước "Commit" thông thường được thay bằng Checkpoint; chỉ commit khi người dùng yêu cầu.

## Review Focus

Năm trường hợp spec ngầm đòi hỏi mà người dùng dễ gặp; mỗi dòng có test trong task chủ:

1. Đường dẫn file có Unicode, dấu cách, `#`, `?`, `%` mở đúng file (Task 4).
2. File không phải SQLite, file 0 byte, file bị khoá bởi tiến trình khác: lỗi rõ ràng hoặc mở được database rỗng, không treo (Task 4).
3. Ô chứa giá trị khác kiểu khai báo (chữ trong cột INTEGER, số nguyên > 2^53, blob) hiển thị đúng và Save không báo xung đột giả (Task 6, 7).
4. `CREATE TRIGGER … BEGIN … END;` chứa dấu chấm phẩy, comment, chuỗi và quoted identifier: chọn đúng statement tại con trỏ (Task 8).
5. Bảng không khoá chính, bảng `WITHOUT ROWID`, cột generated/hidden, view: sửa được hay chỉ đọc đúng lý do (Task 5, 7).

## File Structure

| File | Trách nhiệm |
| --- | --- |
| `go.mod`, `go.sum` | Thêm `github.com/ncruces/go-sqlite3` (Task 1) |
| `internal/connection/models.go` | `SQLitePath`, `ReadOnly`, `Target()`, `Drivers`, validate theo loại |
| `main.go` | `connectDriver` chọn connector theo driver |
| `internal/app/connections.go` | `affectsSession` thêm `SQLitePath`, `ReadOnly` |
| `internal/database/models.go`, `changes.go` | `ColumnMetadata.IntBits`, `TableMetadata.ReadOnly` |
| `internal/database/sqlite/` | Adapter mới (xem từng task) |
| `internal/ui/driver_picker.go` (mới), `icons.go`, `connform.go`, `sql_editor.go`, `explorer.go` | Bước chọn loại, form theo loại, nhãn hiển thị |
| `internal/app/explorer_data.go` | Nhãn database của SQLite |

---

### Task 1: Dependency và kiểm tra build không CGO

**Files:**
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces: module `github.com/ncruces/go-sqlite3` import được (`.../driver`, `.../embed`) và build với `CGO_ENABLED=0`.

- [ ] **Step 1: Thêm module.** Chạy `go get github.com/ncruces/go-sqlite3@v0.35.6`; ghi phiên bản thực tế pin vào đầu `internal/database/sqlite/connect.go` (comment).
- [ ] **Step 2: Smoke test** `internal/database/sqlite/smoke_test.go`: `TestDriverOpensInMemory` mở `file::memory:` bằng `sql.Open("sqlite3", ...)`, import `_ "github.com/ncruces/go-sqlite3/driver"` và `_ ".../embed"`, chạy `SELECT sqlite_version()` và expect không lỗi.
- [ ] **Step 3: Chạy** `go test ./internal/database/sqlite/ -run TestDriverOpensInMemory -v` với `CGO_ENABLED=0`. Expected: PASS. Chạy thêm `go build ./...` và `go vet ./...`.
- [ ] **Step 4: Nếu build/test lỗi, DỪNG và báo người dùng** (không đổi driver, không bật CGO). Nếu qua, giữ file smoke test cho tới Task 4 rồi xoá (nó được thay bằng test thật).

---

## Phần A — Bước chọn loại database

### Task 2: Danh sách loại và placeholder icon

**Files:**
- Modify: `internal/connection/models.go`, `internal/ui/icons.go`
- Create: `internal/ui/driver_picker.go`
- Test: `internal/connection/models_test.go`, `internal/ui/driver_picker_test.go`

**Interfaces:**
- Produces:
  - `var Drivers = []DriverType{DriverPostgres, DriverSQLite, DriverMySQL, DriverSQLServer, DriverRedis}` trong `internal/connection/models.go` (thứ tự hiển thị).
  - `func driverIcon(d connection.DriverType) *mygoui.SVG` trong `internal/ui/icons.go`: PostgreSQL → `postgresql.svg`, MySQL → `my-sql.svg`, các loại còn lại → `database-alt.svg`. Một `switch` duy nhất để thay icon thật chỉ là đổi một dòng.
  - `func (sh *Shell) driverPickerView(c *mygoui.Context, t *mygoui.Theme, f *connForm) (picked connection.DriverType, ok bool)` trong `driver_picker.go`: lưới ô icon + `Driver.Label()`; loại `!Supported()` vẽ mờ kèm nhãn "Soon" và không bấm được.

- [ ] **Step 1: Test** `TestDriversListsEveryDriverOnce` (đủ 5 loại, không trùng, `Drivers[0] == DriverPostgres`).
- [ ] **Step 2: Test** `internal/ui/driver_picker_test.go`, theo kiểu `internal/ui/connections_test.go`/`helpers_test.go`: `TestPickerShowsAllDriverNames` (có text "PostgreSQL", "SQLite", "MySQL", "SQL Server", "Redis"); `TestPickerDisablesUnsupportedDrivers` (bấm "MySQL" không đổi `f.step`; có text "Soon"); `TestPickerCancelClosesDialog`.
- [ ] **Step 3: Chạy test, xác nhận FAIL.** `go test ./internal/connection/ ./internal/ui/ -run "Drivers|Picker" -v`.
- [ ] **Step 4: Cài đặt** `Drivers`, `driverIcon`, `driverPickerView`. Bố cục lưới (số cột, kích thước ô) do người triển khai chọn.
- [ ] **Step 5: Chạy test lại.** Expected: PASS.

### Task 3: Nối bước chọn vào luồng "New connection"

**Files:**
- Modify: `internal/ui/connform.go`, `internal/ui/explorer.go` (không đổi điểm gọi; chỉ kiểm tra)
- Test: `internal/ui/connections_test.go`

**Interfaces:**
- Consumes: `driverPickerView` (Task 2).
- Produces: `connForm.driver connection.DriverType`, `connForm.step formStep` với `type formStep uint8` gồm `stepPickDriver`, `stepFields`. `openConnForm("", folderID)` bắt đầu ở `stepPickDriver`; `openConnForm(editID, "")` vào thẳng `stepFields` với `driver = existing.Driver`. Nút "Back" ở `stepFields` của kết nối mới quay về `stepPickDriver`, giữ nguyên giá trị đã gõ nếu chọn lại cùng loại. Ô "Driver" ở form chỉ đọc khi sửa.

- [ ] **Step 1: Test** `TestNewConnectionStartsWithPicker`, `TestPickingPostgresOpensTheFormWithDefaults` (port "5432", TLS mặc định), `TestEditSkipsPickerAndLocksDriver`, `TestBackReturnsToPickerKeepingInput`.
- [ ] **Step 2: Chạy, xác nhận FAIL.**
- [ ] **Step 3: Cài đặt** trong `openConnForm` và `connFormView` (`connform.go:68`, `:240`): `connFormView` chia theo `f.step`. Thay ô Driver bị khoá cứng (`connform.go:271-274`) bằng nhãn của `f.driver`. `profile()` dùng `f.driver` thay vì `connection.DriverPostgres` (`connform.go:133`).
- [ ] **Step 4: Chạy** `go test ./internal/ui/ -v` (toàn package để bắt hồi quy form PostgreSQL). Expected: PASS.

**Checkpoint A — dừng cho người dùng test:** mở "New connection" thấy lưới loại với placeholder, PostgreSQL vào được form như cũ, loại chưa hỗ trợ bị mờ, Back/Cancel hoạt động, sửa kết nối không qua bước chọn. SQLite lúc này chưa bấm được (chưa `Supported`), sẽ mở ở Task 4.

---

## Phần B — Hồ sơ, connector và form SQLite

### Task 4: Mô hình hồ sơ, connector và kết nối SQLite

**Files:**
- Modify: `internal/connection/models.go`, `internal/app/connections.go:181`, `main.go:153`
- Create: `internal/database/sqlite/connect.go`, `internal/database/sqlite/errors.go`, `internal/database/sqlite/testdb_test.go`
- Test: `internal/connection/models_test.go`, `internal/storage/connections_test.go`, `internal/app/connections_test.go`, `main_test.go`, `internal/database/sqlite/connect_test.go`

**Interfaces:**
- Produces trong `connection`:
  - `Profile.SQLitePath string \`json:"sqlitePath,omitempty"\``, `Profile.ReadOnly bool \`json:"readOnly,omitempty"\``.
  - `const FieldPath = "path"`.
  - `func (d DriverType) Supported() bool` → true cho `DriverPostgres` và `DriverSQLite`.
  - `func (p Profile) Target() string` → PostgreSQL: `"<db> (<user>@<host>)"` như `ui/sql_editor.go:56` đang ghép; SQLite: tên file (`filepath.Base`).
  - `Validate()` rẽ theo `Driver`: SQLite bắt buộc `Name`, `SQLitePath` không rỗng, không kiểm Host/Port/Database/Username/Pool/TLS; PostgreSQL giữ nguyên luật hiện có. `ValidateFiles()` với SQLite báo `FieldPath` khi đường dẫn không tồn tại, là thư mục hoặc không đọc được.
  - `Normalized()` với SQLite xoá `Host`, `Port`, `Database`, `Username`, `SecretRef`, `TLS`, `Pool` (không lưu giá trị vô nghĩa); vẫn đặt `ConnectTimeoutSec` mặc định.
- Produces trong `main.go`: `func connectDriver(ctx context.Context, p connection.Profile, password string) (database.Adapter, error)` — `switch p.Driver`: `DriverPostgres` → `postgres.Connect`, `DriverSQLite` → `sqlite.Connect`, còn lại → `*database.Error{Kind: KindUnsupported}`. `NewManager(..., connectDriver, post)`.
- Produces trong `sqlite`: `func Connect(ctx context.Context, p connection.Profile, password string) (database.Adapter, error)`; `type Adapter struct` (giữ `*sql.DB`, `path`, `readOnly`); `func buildDSN(path string, readOnly bool) string` (URI `file:` với phần path được escape, `mode=rw|ro`, `_pragma=busy_timeout(5000)`, `_pragma=foreign_keys(1)`); `func classify(ctx context.Context, err error, doing string) *database.Error`. Pool: `SetMaxOpenConns(query.MaxSessionsPerProfile+2)`, không đổi `journal_mode`. Nếu mở `mode=rw` thất bại vì quyền ghi, thử lại `mode=ro` và `Adapter.readOnly = true`.
- `testdb_test.go`: `func newTestDB(t *testing.T, ddl ...string) string` tạo file trong `t.TempDir()` bằng driver, chạy DDL, trả đường dẫn.

- [ ] **Step 1: Test mô hình** (`models_test.go`): `TestSQLiteProfileValidates` (chỉ cần tên + path), `TestSQLiteProfileRequiresPath`, `TestSQLiteNormalizedClearsNetworkFields`, `TestPostgresValidationUnchanged`, `TestTargetForEachDriver`, `TestValidateFilesSQLitePathMissing/IsDirectory`. `storage/connections_test.go`: `TestSQLiteProfileRoundTripsAndOldFilesLoad` (file JSON cũ không có `sqlitePath` vẫn đọc được). `app/connections_test.go`: `TestEditingSQLitePathEndsSession` (đổi `SQLitePath` hoặc `ReadOnly` kích hoạt `affectsSession`).
- [ ] **Step 2: Test kết nối** (`connect_test.go`): `TestConnectOpensExistingFile`; `TestConnectRefusesMissingFile` (lỗi `KindFileAccess` hoặc `KindConnection`, file **không** được tạo); `TestConnectNotASQLiteFile` (file văn bản → lỗi rõ ràng); `TestConnectEmptyFileOpensAsEmptyDatabase`; `TestConnectUnicodeAndSpecialPaths` (thư mục tên `dữ liệu #1 ?%` tạo bằng `t.TempDir()` + `filepath.Join`); `TestConnectReadOnlyBlocksWrites`; `TestConnectLockedFileReturnsBusyError` (một connection khác giữ `BEGIN EXCLUSIVE`, `Connect`+`Ping` trả lỗi trong thời hạn, không treo); `TestCloseReleasesFile` (sau `Close` xoá được file trên Windows); `TestConnectHonorsCanceledContext`. `main_test.go`: `TestConnectDriverRoutesByDriver`.
- [ ] **Step 3: Chạy, xác nhận FAIL.** `go test ./internal/connection/ ./internal/storage/ ./internal/app/ ./internal/database/sqlite/ . -v`.
- [ ] **Step 4: Cài đặt** theo Interfaces. Ánh xạ lỗi: không có file → `KindFileAccess` "File not found"; `SQLITE_CANTOPEN`/permission → `KindPermission`; `SQLITE_NOTADB` → `KindConnection` "Not a SQLite database file"; `SQLITE_BUSY`/`LOCKED` → `KindConnection` "Database is locked by another process" (Hint gợi ý đóng chương trình khác); ctx → `KindCanceled`/`KindTimeout`. Dùng `Profile.Target()` ở `ui/sql_editor.go:56`.
- [ ] **Step 5: Chạy test lại.** Expected: PASS. `go build ./...`, `go vet ./...` sạch. Xoá `smoke_test.go` của Task 1.

### Task 5: Form SQLite

**Files:**
- Modify: `internal/ui/connform.go`, `internal/ui/sql_editor.go:56`
- Test: `internal/ui/connections_test.go`

**Interfaces:**
- Consumes: `connForm.driver`/`step` (Task 3), `Profile.SQLitePath`/`ReadOnly`, `FieldPath` (Task 4).
- Produces: `connForm.path string`, `connForm.readOnly bool`; `func (sh *Shell) browseSQLitePath(f *connForm)` — gọi `mygo.Dialog.Open` trong goroutine (theo mẫu ở `internal/ui/sql_files.go`), filter "SQLite (*.db, *.sqlite, *.sqlite3)" và "All files", trả đường dẫn qua `Window.Update`, bỏ qua nếu form đã đóng. `profile()` rẽ theo `f.driver`: SQLite không parse `port`/`timeout` (sửa lỗi hiện tại ở `connform.go:122-126`).

- [ ] **Step 1: Test:** `TestSQLiteFormShowsOnlyFileFields` (có "Name", "Folder", "File", "Browse…", "Open read-only"; không có "Host", "Port", "Username", "Password", "TLS"); `TestSQLiteFormSaveBuildsProfile` (`Driver == DriverSQLite`, `SQLitePath`, `ReadOnly`, `Host == ""`); `TestSQLiteFormMissingFileShowsError` (lỗi trên ô File); `TestSQLiteTestButtonUsesManagerTest` (dùng fake connector như `helpers_test.go`); `TestBrowseSetsPathOnlyWhileFormOpen`; `TestEditSQLiteLoadsPathAndReadOnly`.
- [ ] **Step 2: Chạy, xác nhận FAIL.**
- [ ] **Step 3: Cài đặt** nhánh ô theo loại trong `connFormView`; PostgreSQL giữ nguyên toàn bộ ô và thứ tự. SQLite không có Password/TLS/Timeout.
- [ ] **Step 4: Chạy** `go test ./internal/ui/ -v`. Expected: PASS.

**Checkpoint B — dừng cho người dùng test:** chọn SQLite → form file → Browse → Test connection → Save; lỗi file không tồn tại/không phải SQLite/bị khoá hiện trong form; sửa/xoá kết nối; PostgreSQL không đổi. Explorer chưa cần liệt kê bảng (Task 6).

---

## Phần C — Adapter SQLite

### Task 6: Metadata và explorer

**Files:**
- Create: `internal/database/sqlite/catalog.go`, `internal/database/sqlite/values.go`
- Modify: `internal/database/models.go`, `internal/database/changes.go`, `internal/app/explorer_data.go:72`, `internal/ui/explorer.go:211-221`
- Test: `internal/database/sqlite/catalog_test.go`, `internal/database/models_test.go`, `internal/database/changes_test.go`, `internal/app/workspace_test.go`

**Interfaces:**
- Produces trong `database`: `ColumnMetadata.IntBits int` (0 = suy từ `NativeType` như hiện nay; `checkNumber` ưu tiên giá trị này); `TableMetadata.ReadOnly string` (khác rỗng = lý do kết nối cấm ghi) và `Editable()` trả `false, ReadOnly` khi có.
- Produces trong `sqlite`: `func (a *Adapter) ListSchemas(ctx) ([]database.Schema, error)` trả đúng `[{Name:"main"}]`; `ListTables(ctx, schema)` đọc `sqlite_master` (tables và views, bỏ `sqlite_%`); `DescribeTable(ctx, ref)` dùng `PRAGMA table_xinfo`, `foreign_key_list`, `index_list`/`index_info`, trigger từ `sqlite_master`; mỗi section lỗi ghi `SectionFailed` riêng, riêng columns lỗi thì cả call lỗi. `func classOf(declared string) (database.ColumnClass, int)` theo luật affinity của SQLite: chứa `INT` → `ClassNumber`, `IntBits=64` (kể cả `BOOLEAN`/`BOOL` → `ClassNumber`, không dùng `ClassBool`); `CHAR|CLOB|TEXT` → `ClassText`; `BLOB` → `ClassBinary`; `REAL|FLOA|DOUB|NUMERIC|DECIMAL` → `ClassNumber`; `DATE|TIME` → `ClassDateTime`; `JSON` → `ClassJSON`; rỗng/khác → `ClassOther`. `Generated` lấy từ cột `hidden` của `table_xinfo`; cột `hidden` kiểu virtual-table nội bộ bị bỏ. `ReadOnly` đặt khi `Adapter.readOnly`.
- `func toValue(class database.ColumnClass, v any) grid.Value`: `int64`/`float64` → `KindNumber` với text chuẩn (số nguyên giữ mọi chữ số), `string` → kind theo class nhưng **text lưu trong cột số vẫn là `KindText`** để phân biệt `1` và `'1'`, `[]byte` → `KindBinary` với `Size`, `nil` → `KindNull`.
- `app.Tree`: `t.Database` cho SQLite là `filepath.Base(p.SQLitePath)`; node `main` được mở sẵn lần đầu như nút database ở `explorer.go:211-216`.

- [ ] **Step 1: Test:** `TestListSchemasIsMainOnly`; `TestListTablesSkipsInternalAndMarksViews`; `TestDescribeTableCompositePrimaryKey`; `TestDescribeTableForeignKeysAndIncomingReferences`; `TestDescribeTableGeneratedAndHiddenColumns`; `TestDescribeTableWithoutRowidAndNoPrimaryKey` (không PK → `Editable()` false với lý do, không dùng rowid); `TestClassOfAffinityTable` (bảng giá trị: `"INTEGER"`, `"BIGINT"`, `"BOOLEAN"`, `"VARCHAR(20)"`, `"DATETIME"`, `"BLOB"`, `""`, `"DECIMAL(10,2)"`); `TestReservedAndQuotedIdentifiers` (tên bảng có `"` và dấu cách, Unicode); `TestToValueKeepsStorageClass`; `TestReadOnlyAdapterMarksTablesReadOnly`. `changes_test.go`: `TestCheckNumberUsesIntBits` (INTEGER 64-bit chấp nhận 9223372036854775807). `workspace_test.go`: `TestSQLiteTreeShowsMainWithTables`.
- [ ] **Step 2: Chạy, xác nhận FAIL.**
- [ ] **Step 3: Cài đặt** theo Interfaces; bám mẫu `postgres/catalog.go`.
- [ ] **Step 4: Chạy** `go test ./internal/database/... ./internal/app/ ./internal/ui/ -v`. Expected: PASS.

### Task 7: Đọc trang, lọc, sắp xếp

**Files:**
- Create: `internal/database/sqlite/sql.go`, `internal/database/sqlite/page.go`
- Test: `internal/database/sqlite/sql_test.go`, `internal/database/sqlite/page_test.go`

**Interfaces:**
- Produces: `quoteIdent(string) string` (dấu `"`, nhân đôi `"`); `quoteTable(ref database.TableRef) string` (`"main"."name"`); `buildPageQuery(req database.PageRequest) (pageQuery, error)` cùng hình dạng với `postgres/sql.go:63` (SQL, Args, Size, Reverse, cột binary); `filterClause` dùng `CAST(<col> AS TEXT) LIKE ? ESCAPE '\'` thay cho `::text ILIKE`, placeholder `?`, kích thước binary bằng `length(CAST(<col> AS BLOB))`; `(*Adapter).FetchPage(ctx, req) (*database.PageResult, error)` quét mọi cột vào `any`, đọc giới hạn `Size+1` hàng, đóng rows trước khi return.

- [ ] **Step 1: Test:** `TestQuoteIdentEscapesQuotes`; `TestBuildPageQueryOffsetAndKeyset` (đối chiếu các case của `postgres/sql_test.go`); `TestFilterOperatorsBindParameters` (không giá trị nào nằm trong SQL text); `TestLikeEscapesWildcards` (`%`, `_`, `\` trong ô lọc); `TestFetchPageMoreFlag`; `TestFetchPageMixedTypeValues` (cột INTEGER chứa `'abc'`, số > 2^53, blob, NULL hiển thị đúng); `TestFetchPageHonorsCancel`; `TestFetchPageSortOnNullableColumn`.
- [ ] **Step 2: Chạy, xác nhận FAIL.**
- [ ] **Step 3: Cài đặt** bám `postgres/sql.go`, `page.go`; điểm khác dialect ở Interfaces.
- [ ] **Step 4: Chạy** `go test ./internal/database/sqlite/ -v`. Expected: PASS. `go vet ./...`.

**Checkpoint C — dừng cho người dùng test:** kết nối SQLite hiện `main` → tables/views → mở bảng xem, phân trang, lọc, sắp xếp, chế độ chỉ đọc; Properties đúng; PostgreSQL không hồi quy.

### Task 8: Save/Revert

**Files:**
- Create: `internal/database/sqlite/save.go`
- Test: `internal/database/sqlite/save_test.go`

**Interfaces:**
- Produces: `(*Adapter).SaveChanges(ctx context.Context, req database.SaveRequest) error` — gọi `database.ValidateChanges` trước; một transaction (`BeginTx`), `UPDATE … SET col = ? WHERE <khoá> = ? AND <cột đổi>` kiểm giá trị cũ; mỗi điều kiện giá trị cũ: `CAST(col AS TEXT) IS ? AND typeof(col) IN (...)` với tập `typeof` suy từ `grid.Value.Kind` của `Original` (`KindNumber` → `'integer','real'`; text-like → `'text'`; `KindNull` → `col IS NULL`). 0 hàng khớp → `KindConflict`; >1 → unsafe; cả hai rollback toàn bộ. Hủy ctx chỉ có tác dụng trước `COMMIT`; sau `COMMIT` lỗi → `*database.SaveError` "kết quả không xác định" (theo `postgres/save.go` `commitGate`/`classifySave`). Giá trị mới bind theo class cột (`ClassNumber` → `int64`/`float64`, `ClassText` → `string`). Không retry. Adapter `readOnly` → `KindPermission`/`KindValidation` "read-only connection".

- [ ] **Step 1: Test:** `TestSaveUpdatesOneRow`; `TestSaveConflictWhenValueChanged`; `TestSaveDistinguishesIntegerOneFromTextOne` (ô cũ `'1'` kiểu text, DB đang giữ integer `1` → conflict); `TestSaveRollsBackAllOnFailure`; `TestSaveCompositeKey`; `TestSaveRejectsTextInIntegerColumn` (chặn bởi `ValidateChanges`, không ghi gì); `TestSaveNullToValueAndBack`; `TestSaveCancelBeforeCommit`; `TestSaveLockedDatabaseReturnsBusyAndKeepsEdits`; `TestSaveReadOnlyAdapterRefuses`.
- [ ] **Step 2: Chạy, xác nhận FAIL.**
- [ ] **Step 3: Cài đặt** bám mẫu `postgres/save.go`.
- [ ] **Step 4: Chạy** `go test ./internal/database/... ./internal/app/ -v`. Expected: PASS.

### Task 9: SQL editor — bộ tách statement và phiên thực thi

**Files:**
- Create: `internal/database/sqlite/statement.go`, `internal/database/sqlite/sql_session.go`, `internal/database/sqlite/sql_execute.go`
- Modify: `internal/ui/sql_editor.go` (chỉ nếu còn chỗ gắn PostgreSQL), `internal/ui/sql_files_test.go` (không đổi nếu không cần)
- Test: `internal/database/sqlite/statement_test.go`, `internal/database/sqlite/sql_session_test.go`

**Interfaces:**
- Produces: `func SelectSQLiteSQL(in database.SQLInput) (database.SQLSelection, error)` — cùng ngữ nghĩa với `postgres.SelectPostgresSQL` (`statement.go:23`): ưu tiên vùng chọn, không thì statement tại con trỏ; scanner tôn trọng `'…'` (nhân đôi `''`), `"…"`, `` `…` ``, `[…]`, `-- …`, `/* … */`, và khối `CREATE [TEMP] TRIGGER … BEGIN … END` (không cắt ở `;` bên trong cho tới `END`). `(*Adapter).OpenSQLSession(ctx) (database.SQLSession, error)` dùng `db.Conn(ctx)` riêng cho mỗi phiên; `SQLSession.Select/Execute/Close` giữ các hằng số và trạng thái `SQLIdle|SQLInTransaction|SQLTransactionFailed|SQLSessionLost` như `postgres/sql_session.go`; trạng thái giao dịch lấy từ autocommit của kết nối (`conn.Raw` → `Raw().GetAutocommit()`; xác nhận API trong tài liệu driver). Kết quả đọc có giới hạn bằng hằng số `internal/database/sql_execution.go:10-24`; không lặp write để lấy trang tiếp. Lỗi cú pháp → `KindSyntax`; `SQLITE_BUSY` → lỗi "database bị khoá" có thông báo rõ.

- [ ] **Step 1: Test statement:** `TestSelectionPrefersSelectedText`; `TestStatementAtCursorWithStringsAndComments`; `TestStatementAtCursorQuotedIdentifiersWithSemicolons`; `TestCreateTriggerBlockIsOneStatement`; `TestCursorAtBoundaryBetweenStatements`; `TestEmptyOrCommentOnlyInput`.
- [ ] **Step 2: Test phiên:** `TestExecuteSelectIsBoundedAndReadOnly`; `TestExecuteWriteReportsAffectedRows`; `TestBeginInOneRunCommitInNext` (trạng thái `SQLInTransaction` → `SQLIdle`); `TestFailedStatementInTransactionState`; `TestOpenTransactionBlocksSaveWithClearBusyError` (Save từ adapter khi phiên giữ giao dịch ghi → lỗi "database bị khoá", không treo); `TestCancelStopsLongQuery`; `TestCloseRollsBackOpenTransaction`; `TestSessionLostWhenFileRemoved` (nếu nền tảng cho phép).
- [ ] **Step 3: Chạy, xác nhận FAIL.**
- [ ] **Step 4: Cài đặt** bám cấu trúc `postgres/sql_session.go` và `sql_execute.go`, thay phần giao thức PostgreSQL bằng `database/sql`.
- [ ] **Step 5: Chạy** `go test ./internal/database/... ./internal/query/ ./internal/ui/ -v`. Expected: PASS.

### Task 10: Hồi quy toàn bộ và tài liệu

**Files:**
- Modify: `docs/implementation-phases.md` (mục 10, thêm đoạn "Kết quả triển khai Phase 3.1" cùng mẫu mục 9)
- Test: toàn repo

- [ ] **Step 1:** `gofmt -w` các file Go đã đổi; `go build ./...` (cả khi `CGO_ENABLED=0`), `go vet ./...`, `go test ./...`. Expected: sạch. Test PostgreSQL integration báo skip nếu thiếu `DBL_TEST_PG_*`; ghi rõ trong báo cáo.
- [ ] **Step 2:** Cập nhật `docs/implementation-phases.md`: trạng thái "chờ người dùng nghiệm thu", ghi những gì **chưa** kiểm (manual app, PostgreSQL integration nếu không chạy).
- [ ] **Step 3:** Đưa người dùng checklist mục 9 của spec để tự test app. Không commit.

**Checkpoint D — nghiệm thu cuối do người dùng:** toàn bộ checklist spec mục 9, gồm hồi quy PostgreSQL end-to-end.

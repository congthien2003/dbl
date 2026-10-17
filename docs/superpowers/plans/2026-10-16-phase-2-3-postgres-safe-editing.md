# Phase 2.3 — PostgreSQL Safe Editing

Trạng thái: **ĐÃ TRIỂN KHAI — chờ người dùng nghiệm thu** (xem mục 7). Plan được người dùng duyệt với một thay đổi: concurrency chỉ kiểm các cột được sửa. Người dùng yêu cầu "triển khai phase 2.3" và trả lời "Không có gì" cho câu hỏi mở đầu của brainstorming, nên **plan này do agent viết từ source thực tế**, không qua vòng hỏi đáp và không qua builder-check. Các điểm đánh dấu **[call]** là quyết định của agent, cần người dùng xem lại. Theo AGENTS.md: duyệt plan → mới sửa file; hết phase chờ người dùng nghiệm thu rồi mới commit; không tự commit/push.

Nguồn: [docs/specs.md](../../specs.md) FR-004 (sửa), FR-005, mục 7.3–7.4, 9, 10, 12; [implementation-phases.md](../../implementation-phases.md) mục 8 (Phase 2.3), P0-B (B10–B12, B20), P0-C (operation protocol, Save transaction).

## 0. Hiện trạng đã đọc (căn cứ của plan)

| Thành phần | Hiện có | Hệ quả cho 2.3 |
| --- | --- | --- |
| `grid.Drafts` (`internal/grid/draft.go`) | map `CellRef{RowID, Col}` → `Value`; đặt lại giá trị gốc thì xóa draft; `Revert()` | Dùng lại. Thiếu: duyệt theo thứ tự, lỗi validation, `Clear` sau commit |
| Editor ô (`internal/ui/grid.go`) | F2/double-click mở `TextInput`; Enter → `commit` (chỉ ghi draft); Escape → `cancel`; chỉ mở khi `Column.Editable`. Test: `grid_edit_test.go` | Giữ nguyên hành vi; chỉ bật `Editable` thật + thêm chặn khi đang Saving/Stale |
| `TableData` (`internal/app/tabdata.go`) | `grid.Source`; `ReadOnly()` luôn `true`; `buildColumns` không bao giờ đặt `Editable`; `RowID(row)=row+1` | Phải tính editability từ metadata; `ReadOnly()` thật |
| `Tab.Dirty()` | `Drafts.Len() > 0` | Đã nối với `RequestClose`, `RequestQuit`, `RequestDisconnect`, `RequestDelete`, `SaveProfile`(ApplyEdit) — nhưng các hộp thoại chỉ có **Cancel / Discard**, chưa có **Save** |
| `SQLAdapter` (`internal/database/adapter.go`) | ListSchemas/ListTables/DescribeTable/FetchPage; comment: "SaveChanges … arrive with Phases 2.3" | Thêm `SaveChanges` |
| `postgres.Adapter` | `db *sql.DB` (pgx stdlib, pool); `FetchPage` đọc cột bằng `"col"::text`, bytea bằng `octet_length` | `SaveChanges` dùng `db.BeginTx`; vì giá trị đã đọc dưới dạng `::text`, predicate concurrency so sánh cũng bằng `::text` → so đúng cái người dùng đã thấy |
| `TableMetadata` | `Editable()`, `ResolveIdentity()` (PK, rồi unique index không partial/không expression, mọi cột NOT NULL), `ColumnMetadata.{Identity,Generated,PrimaryKey,Class}` | Đủ dữ liệu để chặn cột; chưa có hàm quyết định theo cột |
| `app/ops.go` | `opSlot` (begin/stop/finish) + completion gate theo `epoch` + `ctx.Err()` | Dùng lại cho Save; thêm `saveOp` |
| `fakedb` | Adapter in-memory, có `Hook` | Thêm `SaveChanges` có mô phỏng conflict/lỗi/commit-không-xác-định |
| Phase doc, mục 8 | Liệt kê `internal/grid/{changeset,validation,persistence,controller}.go` | **Lệch có chủ ý [call]:** `database` đã import `grid` nên `ChangeSet`/validation đặt ở `internal/database`; điều phối ở `internal/app`. `grid` chỉ mở rộng `Drafts` |

Các lỗ hổng phát hiện khi đọc (plan xử lý ở mục 2):

1. `RowID(row)=row+1` là vị trí trong page, và `finishPage`/`finishMetadata` có thể **thay `Page` dưới chân draft** (bấm Reload rồi sửa ngay khi request còn chạy; Retry; refresh metadata đổi layout; một guard bị sót). Nếu changeset được dựng từ `Page.Rows` lúc Save thì edit sẽ được gửi tới *hàng khác* nằm ở cùng chỉ số, và vì giá trị gốc dùng để kiểm concurrency cũng đọc từ hàng mới nên kiểm tra vẫn **qua** → ghi nhầm hàng. Plan sửa tận gốc bằng **snapshot hàng gốc + RowID ổn định** (mục 2.3); các guard chỉ còn là lớp phòng thủ thứ hai.
2. `filterBar` tự áp filter sau 300 ms khi gõ → hộp thoại Save/Discard/Cancel bật giữa lúc đang gõ sẽ cướp focus.
3. `qerrors.go` chưa phân loại SQLSTATE `23xxx` (ràng buộc), `40001`/`40P01` (serialization/deadlock): sẽ rơi vào nhánh `KindSyntax` — sai cho Save.
4. `database/sql` tự rollback khi context của `BeginTx` bị hủy, kể cả lúc `COMMIT` đang chạy → kết quả không xác định nếu cancel đến muộn (P0-C: sau khi gửi `COMMIT` không được cancel).
5. Dialog đóng tab/thoát/disconnect đã có nhưng không có đường "Save rồi tiếp tục".

## 1. Phạm vi và quyết định của tôi

Phạm vi đúng mục 8 của implementation-phases: identity, chặn cột, change tracking, editor, validate, Save transaction, concurrency, conflict, bảo vệ pending edits. **Không làm:** INSERT/DELETE row (ngoài spec FR-005), sửa ô của SQL result (2.4), driver khác, retry tự động, sửa nhiều tab trong một transaction.

- **[call] Cột được sửa** = cột của *bảng* (không phải view/foreign table) có identity đã xác minh, và cột đó: không thuộc `Meta.Identity.Columns` (PK **và** unique-key dùng làm identity — đổi chúng là đổi identity), không `Generated`, không `IdentityAlways`/`IdentityByDefault`, không `ClassBinary` (ô binary không sửa được, nhưng cột binary **không** làm cả bảng chỉ đọc — xem dòng concurrency). `IdentitySerial` (default `nextval`) **được** sửa vì không phải identity column theo PostgreSQL. `ClassOther` (array, geometric, enum tùy biến…) **được** sửa qua text vì server tự parse; concurrency vẫn so được bằng `::text`.
- **[call] Bảng không sửa được** nếu bất kỳ cột identity nào không `KeysetSafe()` (không đọc lại rồi so sánh chính xác được). Lý do hiển thị cho người dùng, lấy từ `Editable()`; không đoán.
- **[user, 2026-10-16] Concurrency chỉ kiểm các cột được sửa** (cộng với khóa identity), không so cả hàng. Predicate của mỗi `UPDATE` = identity (`"k" = $n`, dùng index) + giá trị gốc của từng cột đang được ghi: `"c"::text IS NOT DISTINCT FROM $n`. Hệ quả: người khác sửa cột *khác* của cùng hàng không gây conflict và không bị ghi đè (UPDATE chỉ đặt các cột đã sửa); chỉ khi chính cột đang ghi bị đổi (hoặc hàng bị xóa/đổi khóa) mới báo conflict. Vì cột `bytea` không sửa được nên không bao giờ nằm trong predicate ⇒ **không cần giới hạn riêng cho bytea**, bảng có cột bytea vẫn sửa được các cột khác (thay cho phương án chỉ-đọc ở bản nháp trước).
- **[call] Chuỗi so sánh:** trong transaction Save chạy `SET LOCAL extra_float_digits = 1` để chuỗi float so sánh luôn là dạng ngắn-nhất-chính-xác; nếu session đọc dùng giá trị khác thì chỉ sinh conflict giả (hướng an toàn), không bao giờ bỏ sót.
- **[call] Identity dùng `"k" = $n`, không `::text`** để giữ index.
- **Đã kiểm bằng source (không phải runtime):** pgx v5.11.0 (`extended_query_builder.go: chooseParameterFormatCode`, `pgtype/pgtype.go: planEncode`) gửi Go `string` ở **text format cho mọi OID** bằng `encodePlanStringToAnyTextFormat`, còn `nil` → NULL; profile không đặt `QueryExecMode` nên dùng mặc định `cache_statement` (server mô tả kiểu tham số rồi tự parse text). Vì vậy `SET "c" = $n` và `"k" = $n` với `int4/numeric/jsonb/bool/uuid/array` không cần cast tường minh. Việc server chấp nhận từng chuỗi cụ thể vẫn là `Not run` đến khi có integration test.
- **[call] Một `UPDATE` cho mỗi hàng đã sửa** (gộp các ô của hàng vào một `SET`), theo thứ tự hàng trong page. `RowsAffected()`: `0` → conflict/deleted; `>1` → unsafe; mỗi trường hợp rollback toàn bộ.
- **[call] Đặt NULL = `Shift+Delete` trên ô đang chọn** (không đang mở editor; cột nullable). Xóa hết chữ trong editor cho **chuỗi rỗng**, không phải NULL (giữ phân biệt NULL/empty theo mục 7.3). Hiện gợi ý trong status bar khi có ô chọn.
- **[call] Timeout Save = 60 s** (`SaveTimeout`), vì profile chưa có cấu hình query timeout. Hủy được **trước** `COMMIT`.
- **[call] Sau commit thành công mà reload lỗi:** xóa pending edits (đã lưu), giữ page cũ nhưng đánh dấu `Stale` + banner "Saved. The table could not be reloaded; the values shown are from before the save." và **khóa sửa** tới khi reload thành công. Không bao giờ để người dùng gửi lại edits đã commit.
- **[call] Khi tab có edits (lớp phòng thủ thứ hai, lớp chính là snapshot ở mục 2.3):** (a) Editor bị khóa khi `Saving`, `Stale` hoặc đang `Loading`; (b) kết quả page/metadata đến muộn **không được thay `Page`** nếu tab đã dirty; (c) refresh metadata chỉ áp dụng nếu layout, identity, strategy và editability không đổi — nếu đổi thì bỏ kết quả và báo "The table structure changed. Save or discard your edits, then refresh again."; (d) filter bar chuyển sang **áp thủ công** (Enter / nút Apply) thay vì debounce.
- **[call] Nhãn lỗi theo hàng:** lỗi server khi `UPDATE` hàng gắn vào các ô dirty của hàng đó, khóa theo `RowID` ổn định (không đoán cột).
- **Không làm ở phase này (giới hạn đã biết, ghi vào "Kết quả" khi xong):** không kiểm tra quyền `UPDATE` theo cột trước khi Save (lỗi `42501` hiện ra lúc Save và rollback toàn bộ); trigger/RLS/RULE làm `UPDATE` bị bỏ qua sẽ hiện như conflict — thông báo nói rõ khả năng đó; `extra_float_digits` khác mặc định có thể làm so sánh float báo conflict (an toàn); bảng kế thừa có khóa trùng giữa cha/con sẽ báo "unsafe" và rollback.

## 2. Thiết kế

### 2.1 Mô hình thay đổi (`internal/database`, không import UI)

```go
type KeyValue   struct{ Column string; Value grid.Value }
type CellChange struct{ Column string; Original, Current grid.Value }
type RowChange  struct {
    RowID int64       // RowID ổn định của hàng (để gắn lỗi); KHÔNG phải chỉ số trong page
    Key   []KeyValue  // identity gốc, thứ tự Meta.Identity.Columns
    // không còn Where: conflict check dùng Cells[i].Original của chính các cột được ghi
    Cells []CellChange
}
type ChangeSet struct{ Rows []RowChange }

type SaveRequest struct{ Meta *TableMetadata; Changes ChangeSet }
```

- `SQLAdapter` thêm `SaveChanges(ctx, SaveRequest) error`.
- Lỗi Save là `*database.SaveError` (bọc `*Error`): `Outcome` = `NotApplied` (đã rollback, edits giữ) hoặc `Unknown` (lỗi khi/sau khi gửi `COMMIT`), `Row` = hàng gây lỗi hoặc `-1`, `Conflict` phân biệt `0 rows` / `>1 rows`. `errors.As` lấy được `*Error` bên trong; kiểu `KindConflict`, `KindValidation`, `KindSave`, `KindTimeout`, `KindCanceled`, `KindPermission` dùng sẵn.
- `TableMetadata.CanEditColumn(name) (bool, reason)` là **nguồn duy nhất** cho "cột này sửa được không" (UI, validation và adapter cùng dùng). `Editable()` giữ nguyên.
- `ValidateChanges(meta, cs) []Issue{Row, Column, Message}` — kiểm cục bộ, **trước khi mở transaction**, không chạm DB: cột còn sửa được; NULL vào cột NOT NULL; số nguyên đúng dạng/khoảng cho `int2/int4/int8`; số thập phân đọc được (`NaN`, `Infinity` cho phép); bool theo bộ literal của PostgreSQL; JSON/JSONB hợp lệ (`encoding/json`); text không chứa byte `0x00`. Những kiểm khác (date, uuid, enum…) để server xác nhận trong transaction và quy lỗi `22xxx` về `KindValidation`. Có issue → **không phát SQL ghi nào**.

### 2.2 PostgreSQL (`internal/database/postgres`)

- `save.go`: `buildUpdate(meta, RowChange) (sql string, args []any)` — `UPDATE "s"."t" SET "a" = $1, "b" = $2 WHERE "k" = $3 AND "c"::text IS NOT DISTINCT FROM $4 …`; NULL truyền `nil`; chỉ tên đã quote và placeholder trong chuỗi SQL.
- `SaveChanges`: `ValidateChanges` → `db.BeginTx` → từng `UPDATE` (`ExecContext`) kiểm `RowsAffected` → `Commit`. Mọi lỗi trước `COMMIT` → `Rollback` (defer) và `Outcome=NotApplied`.
- **Điểm không-quay-lại:** tx chạy trên `txCtx := context.WithCancel(context.WithoutCancel(ctx))`; một goroutine chuyển tiếp hủy của `ctx` sang `txCtx` **chỉ cho tới lúc bắt đầu `COMMIT`**. Sau đó cancel/timeout của người gọi không đụng tới commit (sửa lỗ hổng số 4). Lỗi từ `Commit()` ⇒ `Outcome=Unknown`, không retry.
- `qerrors.go`: thêm `classifySave` — `23xxx` → `KindValidation` ("ràng buộc … bị vi phạm"); `40001`/`40P01`/`55P03` → `KindConflict` ("thử lại thủ công", không retry); `25006` → `KindPermission`; `42501` → `KindPermission`; còn lại dùng `classifyQuery` (sửa lỗ hổng số 3).

### 2.3 Điều phối (`internal/app`)

- **RowID ổn định + snapshot hàng gốc (sửa lỗ hổng số 1).**
  - `TableData.RowID(row)` không còn là `row+1`: với bảng có identity, nó là số nguyên cấp bởi một bộ nội suy theo tab (`map[canonicalKey]int64`, tăng dần, sống cùng tab) từ `Page.Keys[row]` — cùng một hàng thật luôn có cùng `RowID` dù ở page/lần reload nào, và hai hàng khác nhau không bao giờ trùng. Bảng không identity không sửa được nên giữ nguyên cách cũ. `grid.Drafts`/`CellRef` không đổi.
  - Lần đầu một hàng có draft, `State.SetDraft(tab, row, col, v)` chụp **snapshot** của hàng đã tải: `RowSnapshot{Key []KeyValue; Values []grid.Value}` (identity + mọi cột) vào `TableData.snapshots[RowID]`, rồi mới `Drafts.Set`. "Giá trị gốc" để so dirty (sửa về giá trị gốc bỏ draft) là giá trị **trong snapshot**, không phải `Page` hiện tại. Khi hàng hết draft thì xóa snapshot. `Revert`/commit thành công xóa tất cả snapshot.
  - `ChangeSet` được dựng **chỉ từ snapshot + draft**, không đọc `Page` lúc Save. Page bị thay (dù do lỗi nào) cũng không thể làm edit đi nhầm hàng; hàng không còn trong page vẫn lưu đúng hàng gốc theo khóa của nó, hoặc báo conflict nếu hàng đã bị xóa.
  - `RowErrors` khóa theo `RowID` (`map[int64]string`), không theo chỉ số.
  - Ràng buộc: `Page.Keys` chỉ có khi identity đã xác minh; không có identity ⇒ bảng chỉ đọc nên không có draft.
- `TableData`: `EditState` tính khi có metadata (`editable bool`, `reason string`); `buildColumns` đặt `Editable` theo `CanEditColumn`; `ReadOnly()` = không cột nào sửa được; thêm `SaveState OpState`, `SaveErr *database.SaveError`, `Stale bool`, `RowErrors map[int64]string`, `snapshots`, `saveOp opSlot`.
- `save.go` (mới): `State.Save(tab, then func())` — dựng `ChangeSet` từ snapshot + `Drafts` (identity và giá trị gốc của các cột được ghi lấy từ **snapshot hàng đã tải lúc sửa lần đầu**, không từ giá trị hiển thị, không từ `Page` hiện tại), validate, rồi chạy worker; hoàn tất qua `s.post` với completion gate chuẩn (tab còn, `epoch` khớp, `op` hiện hành, `ctx.Err()==nil`).
  - Thành công: `Drafts.Clear()`, xóa `RowErrors`, `fetchPage(navReload)`; reload lỗi ⇒ `Stale=true` (xem mục 1); chạy `then` sau khi commit xác nhận.
  - `NotApplied`: giữ nguyên edits, hiện lỗi/lỗi hàng; **không** chạy `then`.
  - `Unknown`: giữ edits, banner "The result of the save is not known. Reload to check before saving again.", không tự retry, không chạy `then`.
  - `Canceled`/mất session: giữ edits (cancel có hiệu lực chỉ trước `COMMIT`).
- `SaveAll(tabs, then)`: lưu tuần tự từng tab, dừng ở lỗi đầu tiên; `then` chỉ chạy khi tất cả thành công. Dùng cho đóng-tab/disconnect/xóa profile/thoát.
- `guard.go` (mới): `PendingEdit{Tab TabID; Reason string; Run func()}` + `ErrEditsPending`. `SetSort`, `SetFilter`, `FirstPage/NextPage/PrevPage`, `ReloadTable` kiểm `tab.Dirty()`: nếu dirty và chưa được quyết định thì đặt `PendingEdit` và trả `ErrEditsPending` thay vì chạy. `ResolvePendingEdit(choice)`: **Save** → `Save(tab, Run)`; **Discard** → `Drafts.Revert()` rồi `Run`; **Cancel** → bỏ. Mọi action thay thế `Page` (kể cả đổi sort — mục 8.7) đi qua đây.
- `RequestClose`/`RequestQuit`/`RequestDisconnect`/`RequestDelete`/`SaveProfile(ApplyEdit)`: giữ nguyên điều kiện hỏi, bổ sung đường **Save**: `SaveAll(dirtyTabs, tiếp-tục-việc-cũ)`.
- Tab đang `Saving`: các action trên bị từ chối với thông báo "Saving…"; riêng đóng tab / disconnect / xóa profile / thoát hiện cảnh báo bổ sung "A save is still running; closing now may leave its result unknown" (đúng P0-C: không đóng im lặng).
- `finishPage`/`finishMetadata`: áp dụng quy tắc (b), (c) của mục 1 khi `tab.Dirty()`.

### 2.4 UI (`internal/ui`)

- `grid.go`: `startEdit` từ chối khi `Saving`/`Stale`/`Loading`; ô dirty tô `Palette.Dirty`; ô có lỗi tô `Palette.Error` + tooltip; `Shift+Delete` đặt NULL; tooltip lý do cho ô không sửa được (từ `CanEditColumn`). `commit` đi qua `State.SetDraft` (có snapshot) khi `tab.Data != nil`, giữ `Drafts.Set` trực tiếp cho nguồn không phải database (test Phase 1). **Sửa lỗi nhỏ:** khi ô gốc là NULL, giá trị mới lấy `Kind` từ cột (`src.Columns()[col].Kind`) thay vì cố định `KindText` như hiện nay.
- `workspace.go` (UI): thanh công cụ có **Save** (`Ctrl+S`, chỉ khi tab dirty và không Saving), **Revert** (hiện chỉ cho `Data == nil` → bật cho cả `TableData`), "N unsaved edit(s)", spinner "Saving…", lý do read-only (nhãn "Read-only: …" thay chữ "Read-only" trơn), banner lỗi Save / `Stale` / `Unknown`.
- `shell.go` + `dialogs.go`: hộp thoại ba nút **Cancel / Discard / Save** cho `PendingEdit`, `PendingClose`, `PendingQuit`, `PendingConn` (khi `DirtyTabs>0`); chữ thay đổi theo tình huống. "Save" bắt đầu `SaveAll` và ẩn hộp thoại; lỗi hiện trên tab.
- `filterbar.go`: khi `tab.Dirty()` không debounce; thêm nút **Apply** (và Enter) gọi `SetFilter`; `ErrEditsPending` không đặt `refused`; Cancel/Discard khôi phục text về filter đang áp.
- `tabs.go`: chấm `●` trước tên tab dirty (đang có `tab.Dirty()` ở dòng 91 — kiểm tra đã hiển thị gì trước khi thêm).
- `theme.go`: không đổi (`Palette.Dirty`, `Palette.Error` đã có).
- `main.go`: không đổi (`OnClose → RequestQuit`, `OnQuitConfirmed` đã có; "Save and exit" gọi `OnQuitConfirmed` sau `SaveAll`).

## 3. Bảng file

| File | Việc |
| --- | --- |
| `internal/database/changes.go` (+ `changes_test.go`) | `KeyValue/CellChange/RowChange/ChangeSet/SaveRequest/SaveError`, `ValidateChanges` |
| `internal/database/models.go` | `TableMetadata.CanEditColumn` |
| `internal/database/adapter.go` | `SaveChanges` trong `SQLAdapter`; sửa comment |
| `internal/database/postgres/save.go` (+ `save_test.go`) | `buildUpdate`, `SaveChanges`, điểm không-quay-lại |
| `internal/database/postgres/qerrors.go` (+ test) | `classifySave` |
| `internal/database/postgres/integration_save_test.go` | integration (cần `DBL_TEST_PG_*`) |
| `internal/database/fakedb/fakedb.go` | `SaveChanges` in-memory: conflict, `>1`, lỗi giữa batch, commit-không-xác-định, `Hook("SaveChanges")` |
| `internal/grid/draft.go` (+ test) | duyệt có thứ tự, `Clear`, lỗi theo ô |
| `internal/app/tabdata.go` | `Editable` theo metadata, `ReadOnly()`, trạng thái Save/Stale, quy tắc finishPage/finishMetadata |
| `internal/app/save.go` (+ `save_test.go`) | `Save`, `SaveAll`, build changeset, completion gate |
| `internal/app/guard.go` (+ test) | `PendingEdit`, `ErrEditsPending`, resolve; nối vào SetSort/SetFilter/paging/Reload |
| `internal/app/ops.go` | `SaveTimeout` |
| `internal/app/state.go`, `connections.go`, `workspace.go` | đường Save cho close/quit/disconnect/delete/apply-edit; `Tab.Saving()` |
| `internal/ui/{grid,workspace,shell,dialogs,filterbar,tabs}.go` + tests | như mục 2.4 |

## 4. Kiểm tra

**Tự động (tôi chạy được theo AGENTS.md):** `gofmt -l`, `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...`.

Unit (không cần DB):

- `buildUpdate`: quoting (reserved, Unicode, dấu `"`), NULL, thứ tự placeholder, `IS NOT DISTINCT FROM`, không có giá trị người dùng trong chuỗi SQL.
- `CanEditColumn`/`Editable`: view, bảng không identity, PK, unique-identity, generated, identity always/by-default, serial, binary (ô không sửa được, bảng vẫn sửa được), key không `KeysetSafe`.
- **Chống ghi nhầm hàng:** sửa ô ở hàng A, rồi thay `Page` (sort/reload/lỗi giả lập bypass guard) sao cho hàng khác nằm ở chỉ số cũ ⇒ changeset vẫn mang khóa/giá trị của hàng A; `RowID` của cùng một hàng giữ nguyên qua reload; sửa về giá trị gốc (theo snapshot) bỏ dirty và snapshot.
- `ValidateChanges`: từng nhánh; validation lỗi ⇒ adapter fake **không** nhận lệnh ghi nào.
- `Drafts`: sửa về giá trị gốc bỏ dirty (đã có), thứ tự duyệt, `Clear`.
- `app` với `fakedb`: Save thành công xóa draft **chỉ sau** commit; conflict (0 hàng) / unsafe (>1) / lỗi giữa batch ⇒ edits còn nguyên, không cập nhật một phần; commit-không-xác-định ⇒ giữ edits, không retry (đếm số lần gọi); cancel trước commit; completion muộn sau đóng tab / đổi connection / op cũ bị gate loại; reload sau commit lỗi ⇒ `Stale`, edits đã xóa, editor khóa; page/metadata đến muộn không thay page dirty; refresh metadata đổi layout bị bỏ khi dirty.
- Guard: mỗi action (`SetSort`, `SetFilter`, `First/Prev/Next`, `Reload`, đóng tab, disconnect, xóa profile, apply-edit, thoát) × (Save thành công / Save lỗi / Discard / Cancel).
- UI (Tester): F2/double-click/Enter/Escape trên bảng PostgreSQL giả; ô PK/binary/generated không mở editor; `Shift+Delete`; `Ctrl+S`; Revert; ba nút ở mọi hộp thoại; filter bar thủ công khi dirty; hiển thị `Stale`/`Unknown`.

**Integration (`integration_save_test.go`, chỉ chạy khi có `DBL_TEST_PG_*`, schema `dbl_it_<random>` tự tạo/xóa):** nhiều ô/hàng, composite key, unique-key fallback, NULL ↔ empty, revert về giá trị gốc, hai session cùng hàng (conflict), hàng bị xóa, lỗi ràng buộc ở hàng thứ k ⇒ rollback toàn bộ, `numeric`/`timestamptz`/`jsonb`/`uuid`/`bool`/enum/array round-trip, Unicode và tên reserved, BEFORE UPDATE trigger bỏ qua hàng. **Hiện môi trường không có biến `DBL_TEST_PG_*` ⇒ sẽ ghi `Not run` nếu không có instance cô lập.**

**Thủ công (người dùng, checklist mục 8 của phase doc):** nhiều ô/hàng, hai session, mất mạng giữa Save, từng nhánh Save/Discard/Cancel ở mọi action bảo vệ, commit thành công nhưng reload lỗi, thử trên cửa sổ thật (giao diện chỉ mới được kiểm qua ảnh render của Tester).

## 5. Rủi ro lớn nhất

1. **SQL ghi chỉ được kiểm bằng unit test về dạng text** nếu không có PostgreSQL cô lập. Cách pgx mã hóa tham số `string` đã được xác nhận qua source (mục 1), nhưng việc server chấp nhận từng giá trị (đặc biệt `jsonb`, `numeric`, `timestamptz`, array) và `::text IS NOT DISTINCT FROM` chưa chạy trên server thật. Đây là lý do `integration_save_test.go` là deliverable, không phải tùy chọn.
2. **Conflict check chỉ trên cột được ghi** nên không phát hiện người khác sửa cột *khác* của cùng hàng — chủ ý (user chọn): UPDATE chỉ đặt các cột đã sửa nên không ghi đè gì của họ. Không bảo vệ các ràng buộc giữa các cột (ví dụ hai cột phải khớp nhau) — ràng buộc CHECK của server vẫn bắt lúc Save.
3. **Điểm không-quay-lại của `COMMIT`** phụ thuộc cách `database/sql` + pgx xử lý context; cần test fake (cancel sau khi vào commit) và test thật.

## 6. Thứ tự thực hiện đề xuất

1. `database`: changes + `CanEditColumn` + `ValidateChanges` + `SaveChanges` trong interface + `fakedb` (test xanh).
2. `postgres`: `buildUpdate`, `classifySave`, `SaveChanges` (+ unit); viết integration.
3. `grid.Drafts` mở rộng.
4. `app`: editability trong `TableData`, `Save`/`SaveAll`, completion gate, quy tắc finishPage/finishMetadata.
5. `app`: guard + nối vào các action + đường Save của close/quit/disconnect/delete/apply-edit.
6. `ui`: grid/workspace/dialogs/filterbar/tabs.
7. `gofmt`, `go vet`, `go test`, `CGO_ENABLED=0 go build`; cập nhật mục "Kết quả triển khai" của file này; dừng chờ nghiệm thu.

## 7. Kết quả triển khai

**Đã làm** (chưa commit):

- `database`: `changes.go` (`ChangeSet/RowChange/CellChange/KeyValue/SaveRequest/SaveError/SaveOutcome`, `ValidateChanges`); `TableMetadata.CanEditColumn/HasEditableColumn`, `Editable()` chặn thêm bảng có cột khóa không so sánh chính xác được; `SQLAdapter.SaveChanges`.
- `database/postgres`: `save.go` (`buildUpdate`, `SaveChanges` một transaction, `commitGate` — điểm không-quay-lại của `COMMIT`, `commitFailed`, `classifySave` cho `23xxx`/`40001`/`40P01`/`55P03`/`25006`/`42501`).
- `database/fakedb`: `SaveChanges` có conflict/`>1` hàng/atomic và `UnknownCommit`.
- `grid`: `Drafts.Refs/HasRow`; `Column.Reason`.
- `app`: `RowID` ổn định + snapshot hàng gốc (`SetDraft`, `RevertEdits`, `changeSet` dựng chỉ từ snapshot + draft); `Save/SaveAll/finishSave` (kết quả ghi luôn được áp dụng khi tab còn, kể cả khi context đã hết); `Stale` sau commit; `guard.go` (`PendingEdit`, `ErrEditsPending`, `ErrSaving`, `ResolvePendingEdit`) nối vào sort/filter/first/prev/next/reload; page/metadata đến muộn không thay page đang có edit; `SaveAndClose`, `SaveAndQuit`, `SaveAndConfirmConn`; `PendingConn.SavingTabs`.
- `ui`: Save (`Ctrl+S`) / Revert, đếm edit, "Saving…", banner lỗi Save / `Unknown` / `Stale`, ô lỗi tô đỏ, `Shift+Delete` = NULL, dòng lý do "Read-only: …" và "Cannot edit this cell: …"; hộp thoại ba nút cho `PendingEdit`, đóng tab, thoát, disconnect/xóa profile/apply-edit; filter bar áp thủ công (Enter / Apply) khi có edit và khôi phục text khi Cancel.

**Lệch so với plan:**

- Concurrency chỉ trên các cột được sửa (người dùng yêu cầu) ⇒ `RowChange` không có `Where`; bảng có cột `bytea` **vẫn sửa được** các cột khác (không còn chỉ-đọc).
- Tiêu đề hộp thoại đổi để không trùng nhãn "Unsaved changes" của dấu ● trên tab: `Unsaved edits` (hành động thay page), `Close tab with unsaved edits?`, `Exit with unsaved edits?`. Nút Save: `Save and continue`, `Save and close`, `Save and exit`, `Save changes first`.
- Khi chuỗi Save (đóng/thoát) dừng ở một tab lỗi, chỉ chọn tab đó (banner của tab nói lý do); không đặt `State.Notice`.
- Không thêm dấu chấm mới trên tab: `●` "Unsaved changes" đã có từ Phase 1.
- Phase doc liệt kê `grid/{changeset,validation,persistence,controller}.go`: không tạo (xem mục 0).

**Đã chạy:** `go vet ./internal/...`, `go test -count=1 ./internal/...` (tất cả xanh, gồm test mới ở `database`, `database/postgres`, `grid`, `app`, `ui`), `CGO_ENABLED=0 go build ./internal/...`.

**Chưa chạy (`Not run`):**

- `integration_save_test.go` và mọi SQL ghi với PostgreSQL thật (môi trường không có `DBL_TEST_PG_HOST/USER/DB`). Mới xác nhận bằng source rằng pgx v5.11.0 gửi `string` ở text format cho mọi kiểu; chưa xác nhận từng giá trị với server.
- `go build ./...` / `go vet .` cho package gốc: cần `mygo_windows_amd64.syso` do `go tool mygo build` tạo (đã ignore trong git; thiếu từ trước phase này).
- Thử trên cửa sổ thật của MyGo (giao diện chỉ mới được kiểm qua `ui.Tester`).
- Kiểm tra thủ công theo checklist mục 8 của phase doc.

**Giới hạn đã biết:** không kiểm quyền `UPDATE` theo cột trước khi Save (lỗi quyền hiện lúc Save, rollback toàn bộ); trigger/RLS/RULE làm `UPDATE` bị bỏ qua hiện như conflict (thông báo nói rõ khả năng đó); conflict check chỉ trên cột được ghi nên không phát hiện người khác sửa cột *khác* của cùng hàng (chủ ý); bảng kế thừa có khóa trùng giữa cha/con sẽ báo "unsafe" và rollback; chuỗi so sánh dùng `::text` theo cách trang được đọc (float so với `extra_float_digits = 1` trong transaction Save, chỉ có thể sinh conflict giả).


# Phase 2.2 — PostgreSQL Read Workflow

Trạng thái: **ĐÃ TRIỂN KHAI — chờ người dùng nghiệm thu** (xem mục 4). Người dùng yêu cầu "triển khai phase 2.2" và bỏ qua bước hỏi đáp của brainstorming; **plan này do agent viết từ source thực tế và chưa được người dùng duyệt riêng** (implementation-phases mục 1 yêu cầu duyệt plan). Các điểm đánh dấu **[call]** là quyết định của agent, cần người dùng xem lại khi nghiệm thu. Chưa commit; hết phase chờ người dùng nghiệm thu (AGENTS.md).

Nguồn: [docs/specs.md](../../specs.md) FR-002, FR-003, FR-004 (đọc), FR-006, FR-010, mục 6.2–6.3, 7.1–7.2, 8, 9, 10, 12; [implementation-phases.md](../../implementation-phases.md) mục 7 (Phase 2.2), P0-C (operation protocol).

## 0. Phạm vi và quyết định của tôi (người dùng chưa duyệt từng điểm)

Phạm vi đúng mục 7 của implementation-phases: explorer lazy thật, Table Inspector Data/Properties, paging server-side, sort, một column filter, giá trị typed, row identity/editability **chỉ tính, grid vẫn chỉ đọc**. Không làm: sửa/lưu ô (2.3), SQL editor chạy query (2.4), driver khác.

Các quyết định (đánh dấu **[call]** = tôi tự chọn, người dùng có thể đổi):

- **[call] PostgreSQL ≥ 12.** Truy vấn metadata dùng `pg_catalog` (không phụ thuộc quyền SELECT trên bảng; `information_schema` chỉ liệt kê object có quyền). Dùng `attgenerated` (12+), `indnkeyatts` (11+).
- **[call] Một database mỗi connection.** Explorer: Connection → Database (đúng `profile.Database`) → Schema → Tables / Views. Không liệt kê `pg_database` vì đổi database cần connection khác (spec FR-002). Ẩn schema `pg_*` và `information_schema`.
- **[call] Giá trị được lấy dưới dạng text do server định dạng** (`"col"::text`), riêng `bytea` lấy `octet_length("col")` (không tải nội dung binary). Nhờ đó numeric/timestamp/uuid không mất độ chính xác qua float64/time.Time; `Kind` của ô lấy từ metadata (typcategory/typname). Session **không** pin `DateStyle`/`IntervalStyle` (startup parameter có thể bị pgbouncer từ chối): chuỗi ngày giờ theo cấu hình server (mặc định ISO).
- **[call] Strategy phân trang** (ghi rõ trên UI): `keyset` khi không sort và bảng có row identity (PK, hoặc unique index không partial/không expression, mọi cột NOT NULL) mà mọi cột khóa sắp xếp được; ngược lại `offset` với `ORDER BY <sort> , <identity>` (tie-breaker unique). Không có identity: không thêm tie-breaker, hiển thị cảnh báo "không có thứ tự ổn định". Chỉ có First/Prev/Next (không "Last", không nhảy trang, không `COUNT(*)`); `More` suy ra bằng cách đọc `size+1` dòng. Tổng số dòng hiển thị là ước lượng `reltuples` khi có.
- **[call] Filter dùng tham số kiểu text** (`"col" = $1`; pgx gửi `string` ở text format, server suy kiểu tham số theo cột). Operator theo `ColumnClass`; `Contains` = `"col"::text ILIKE $1` với giá trị đã escape `\ % _`. Giá trị sai kiểu → lỗi `22P02` được phân loại `KindValidation`, dữ liệu cũ giữ nguyên.
- **[call] Timeout truy vấn cố định** (metadata 30 s, page 30 s) vì profile chưa có cấu hình query timeout; hủy được bất cứ lúc nào.
- **[call] Editor ô của Phase 1 giữ nguyên code** và chỉ bật khi `Source.Columns()[i].Editable`; nguồn PostgreSQL luôn trả `Editable=false` ở 2.2 (grid chỉ đọc). Phase 2.3 thay nguồn dữ liệu và change tracking, không viết lại editor.
- **Cache:** LRU tối đa 4 trang/tab, xóa khi đổi sort/filter/reload, khi refresh metadata làm đổi layout/khóa/strategy, và khi một trang không còn gì phía trước nhưng mang số > 1 (dữ liệu đổi giữa chừng).
- **Debounce filter:** 300 ms trong view bằng `c.Now()`/`c.After` (chạy trên đồng hồ của Tester), rồi gọi action đồng bộ của `State`; đổi cột/operator áp dụng ngay. Guard bằng generation, hủy khi đóng tab.
- **Giới hạn đã biết:** một ô text cực lớn vẫn được tải nguyên (cắt chỉ ở hiển thị); bị chặn bởi page 100 dòng.

## 1. Kiến trúc

```
ui (explorer, grid, properties, filter bar)         -- không chạm SQL
  └─ app.State: OpenTable, Reload, Next/Prev, SetSort, SetFilter, RefreshNode…
       ├─ app.TableData (per tab): meta, page, gen, op, cache, filter draft
       ├─ app.ExplorerData (per connection+epoch): schemas, tables
       └─ connection.Manager: SQL(id) adapter, Epoch(id), SessionContext(id)
database: SQLAdapter + models + QueryPlan + identity + ColumnClass   (không import UI)
database/postgres: catalog queries, SQL builder (dialect), value conversion, error classification
grid: Value/Kind (đã có), page cache
```

Completion gate (P0-C): mọi worker kết thúc bằng `State.Post(fn)`; `fn` chỉ áp dụng khi tab còn, `connection epoch` khớp, `op.id` và `gen` khớp, `ctx.Err()==nil`; ngược lại bỏ kết quả. `Workspace.Close` hủy op của tab. Disconnect hủy `SessionContext` của connection → mọi truy vấn đang chạy dừng.

## 2. File dự kiến

| File | Nội dung |
| --- | --- |
| `internal/database/adapter.go` | thêm `SQLAdapter` (ListSchemas, ListTables, DescribeTable, FetchPage) |
| `internal/database/models.go` | `Schema`, `Table`, `ObjectKind`, `ColumnClass`, `ColumnMetadata`, `Section[T]`, `Constraint/ForeignKey/Index/Trigger`, `TableMetadata`, `RowIdentity` |
| `internal/database/query.go` | `Sort`, `Filter`, `FilterOp`, `Operators(class)`, `PageRequest`, `PageResult`, `Cursor`, `PlanQuery` |
| `internal/database/postgres/{adapter,catalog,sql,values,query_errors}.go` | thực thi |
| `internal/grid/cache.go` | page cache LRU |
| `internal/connection/manager.go` | `SQL`, `Epoch`, `SessionContext` |
| `internal/app/{tabdata,explorer_data}.go`, `workspace.go`, `state.go` | op protocol, state |
| `internal/ui/{explorer,grid,properties,filterbar,workspace,shell}.go` | UI |
| `internal/grid/source.go` | `grid.Source` + `grid.Column`: grid UI đọc qua interface; `TableData` (thật) và `mock` (chỉ test) cùng implement |
| `internal/mock/` | **giữ** làm nguồn giả cho test editor/guard Phase 1; không còn được dùng bởi explorer hoặc luồng mở bảng thật |

## 3. Kiểm tra

- Unit (chạy được không cần DB): SQL builder & quoting, `PlanQuery`, identity, class mapping, filter escape, value conversion, cache, op gate (stale/cancel/close tab/đổi connection/timeout giữ dữ liệu cũ), explorer lazy data, UI (Tester) với fake adapter.
- Integration (chỉ chạy khi có `DBL_TEST_PG_*`, xem `internal/database/postgres/integration_test.go`): metadata, paging keyset/offset, filter, NULL/empty/json/bytea/numeric/Unicode/reserved identifiers. **Chưa chạy trong phiên triển khai nếu không có instance cô lập**; kết quả được ghi ở mục "Kết quả triển khai".
- `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...`.

## 4. Kết quả triển khai

**Đã làm** (chưa commit):

- `database`: `SQLAdapter`, metadata có `Section` (phân biệt không áp dụng / không đủ quyền / lỗi), `ColumnClass`, `PlanQuery` (keyset khi không sort và có identity, ngược lại offset + tie-breaker), `PageRequest.Validate`, `ResolveIdentity` (không đoán khóa).
- `database/postgres`: truy vấn `pg_catalog` (schema, bảng/view, cột, constraint, FK đi/đến, index, trigger), SQL builder chỉ chứa tên đã quote và placeholder, `FetchPage` (đọc `size+1` dòng, `octet_length` cho bytea), phân loại lỗi truy vấn.
- `connection.Manager`: `SQL`, `Epoch`, `SessionContext`, `Post`; disconnect hủy mọi truy vấn của session.
- `app`: `TableData` (grid.Source, op slot + completion gate, cache LRU 4 trang, sort, filter, paging), `ExplorerData` (lazy theo node, tree gắn với epoch của session).
- `ui`: explorer connection → database → schema → Tables/Views (placeholder Loading/lỗi/trống, Refresh từng node), Table Inspector (Data/Properties), filter bar có debounce 300 ms, sort bằng header, thanh phân trang ghi rõ strategy và cảnh báo thiếu khóa, trạng thái loading/lỗi/rỗng.
- `grid.Source` + `mock.Dataset.Source()` giữ nguyên test editor/guard của Phase 1; nguồn PostgreSQL luôn read-only.

**Đã chạy:** `go vet ./...`, `CGO_ENABLED=0 go build ./...`, `go test ./...` đều qua (unit + UI với `fakedb`).

**Chưa chạy (`Not run`):** `integration_read_test.go` và toàn bộ SQL catalog/page với PostgreSQL thật — cần instance cô lập qua `DBL_TEST_PG_*` (test tự tạo schema `dbl_it_<random>` và xóa khi xong). Đây là rủi ro lớn nhất của phase: chuỗi SQL chỉ mới được kiểm bằng unit test về dạng text.

**Giới hạn đã biết:** chỉ First/Previous/Next (không nhảy trang, không COUNT); một ô text rất lớn vẫn được tải nguyên; PostgreSQL ≥ 12; chỉ liệt kê database của connection; schema `pg_*`/`information_schema` bị ẩn.

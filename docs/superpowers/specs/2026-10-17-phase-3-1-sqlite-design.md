# Phase 3.1 — SQLite và bước chọn loại database: thiết kế

- Ngày: 2026-10-17.
- Nguồn: [implementation-phases.md](../../implementation-phases.md) mục 10, [specs.md](../../specs.md) FR-001, FR-002, mục 6.1, 8.4.
- Trạng thái: bản nháp chờ người dùng duyệt. Chưa có code nào được viết cho phase này.

## 1. Mục đích

Phase 2.1–2.4 đã cho DBL làm việc đầy đủ với PostgreSQL: quản lý kết nối, explorer, xem và sửa ô, chạy SQL, lưu file SQL. Phase 3.1 cho phép làm đúng những việc đó với một **file SQLite cục bộ**.

Người dùng chỉ cần xem bảng, sửa giá trị trong ô và chạy SQL, tức là hành vi giống PostgreSQL. Vì vậy phase này không tạo trải nghiệm mới cho SQLite. Nó thêm một adapter mới cho các hợp đồng chung đã có, cộng với phần giao diện để tạo được kết nối SQLite.

## 2. Phạm vi

Có ba phần, làm theo đúng thứ tự này:

1. **Bước chọn loại database.** Khi tạo kết nối mới, trước khi thấy form, người dùng chọn loại database trong một lưới ô gồm icon và tên loại. Icon chính thức do người dùng gửi sau; bây giờ dùng placeholder.
2. **Form kết nối theo loại.** Sau khi chọn loại, form hiện các ô phù hợp loại đó. PostgreSQL giữ nguyên như hiện tại. SQLite có form riêng, gọn hơn.
3. **Adapter SQLite.** Dùng `ncruces/go-sqlite3`, hiện hierarchy `main` với tables và views, đọc/sửa/Save/Revert và chạy SQL như PostgreSQL.

## 3. Mục tiêu

- Tạo, sửa, test và xoá được hồ sơ SQLite. Mở được file SQLite có sẵn từ explorer.
- Xem bảng (phân trang, filter, sort), sửa ô và Save/Revert an toàn trên SQLite, theo cùng hợp đồng `SQLAdapter` như PostgreSQL.
- Chạy SQL trong SQL tab trên kết nối SQLite: chạy phần chọn hoặc statement tại con trỏ, kết quả chỉ đọc có giới hạn, hủy được.
- Giao diện chung (explorer, tab, grid, SQL editor, lưu file SQL) hoạt động với SQLite mà không làm hỏng luồng PostgreSQL.
- Giới hạn của engine được hiển thị rõ trong giao diện. Khi không an toàn để ghi thì mở ở chế độ chỉ đọc và nói rõ lý do.

## 4. Không làm (non-goals)

- **Cloudflare D1.** D1 là SQLite chạy trên Cloudflare, chỉ truy cập qua REST API của Cloudflare. Đây là một cách kết nối khác, cần bổ sung vào spec trước, nên làm sau phase này như một bước riêng. Phase này chỉ cần để phần dùng chung không chặn việc đó (xem mục 7).
- **Tạo file `.db` mới từ DBL.** Chỉ mở file có sẵn; đường dẫn chưa có file thì báo lỗi, không tự tạo. Người dùng đã đồng ý.
- **Kết nối MySQL, SQL Server, Redis.** Các loại này hiện trong bước chọn nhưng chưa dùng được.
- **Icon chính thức** của các loại database. Người dùng sẽ gửi sau.
- Mọi mục ở spec 1.3 (import/export, backup, sửa khoá chính trong grid, sửa ô trong kết quả query...).

## 5. Ràng buộc

- Tuân theo [AGENTS.md](../../../AGENTS.md): lập plan theo từng file, chờ duyệt rồi mới sửa; không commit hay push khi chưa được yêu cầu; hoàn tất mỗi phần phải chờ người dùng tự test app. Được phép chạy `go build`, `go vet`, `go test`.
- Database work chạy ngoài UI thread và đẩy kết quả về qua `Window.Update`; chặn kết quả cũ bằng completion gate như đã mô tả ở P0-C.
- Người dùng **đã đồng ý** thêm dependency `github.com/ncruces/go-sqlite3` vào `go.mod` ngay từ đầu phase, nên cả ba phần đều thử được trên app. Phiên bản được ghi nhận trong tài liệu Phase 0 là `v0.35.6`. Trước khi pin phải kiểm tra lại phiên bản và build không CGO. Nếu build lỗi thì **dừng và báo người dùng**, không tự đổi sang driver khác hay bật CGO. Mọi thay đổi dependency ngoài driver này vẫn cần hỏi riêng.
- Không lưu mật khẩu hay token cho SQLite. Không thêm gì vào credential store cho loại này.
- Lỗi dùng category chung theo spec mục 9; không lộ nội dung nhạy cảm.

## 6. Các quyết định của người dùng

- Hồ sơ SQLite tạo bằng cách chọn loại SQLite, rồi chọn file `.db` bằng hộp thoại mở file. Sau đó hiện trong explorer như kết nối PostgreSQL, với mục `main` chứa tables và views.
- Chọn loại database bằng ô **icon + tên loại**. Icon placeholder trước, icon thật gửi sau.
- Thứ tự làm: bước chọn loại (placeholder) trước, form nhập thông tin sau.
- Form SQLite gồm tên, folder, đường dẫn file kèm nút "Browse…" và checkbox "Open read-only". Không có host, port, username, password, TLS hay pool.
- Test connection cho SQLite kiểm tra file tồn tại, đọc được và đúng là file SQLite. Lỗi như không tồn tại, không có quyền hay bị khoá báo ngay trong form.
- Chỉ mở file có sẵn, không tạo mới.
- Làm SQLite file cục bộ trước; Cloudflare D1 để sau.
- **Sửa ô được kiểm tra theo kiểu khai báo của cột**, như PostgreSQL: cột `INTEGER` chỉ nhận số nguyên, gõ chữ vào thì báo lỗi. Ô đang chứa giá trị khác kiểu khai báo (SQLite cho phép) vẫn hiển thị đúng giá trị thật. Muốn lưu giá trị khác kiểu thì dùng SQL. Lọc và sắp xếp dùng kiểu khai báo của cột như hiện nay.
- Thêm dependency `ncruces/go-sqlite3` ngay từ đầu phase (xem mục 5).

## 7. Thiết kế

### 7.1. Bước chọn loại database

Trong luồng "New connection" (từ các điểm gọi hiện có trong `internal/ui/explorer.go`), hộp thoại mở ra bước 1 trước:

- Một lưới các ô, mỗi ô là icon và tên: PostgreSQL, SQLite, MySQL, SQL Server, Redis.
- Loại dùng được (PostgreSQL, SQLite) bấm vào để sang bước 2.
- Loại chưa dùng được (MySQL, SQL Server, Redis) hiện mờ, kèm nhãn ngắn kiểu "Soon", không bấm sang bước 2.
- Có nút Cancel. Không cần nút Save ở bước này.
- Bước 2 có nút "Back" quay về bước 1, giữ nguyên những gì đã gõ nếu quay lại chọn đúng loại đó.

**Sửa một kết nối đã có** bỏ qua bước 1 và vào thẳng form; loại database hiện dưới dạng chỉ đọc và không đổi được.

**Placeholder icon (quyết định của tôi).** `resources/` đã có `postgresql.svg` và `my-sql.svg`. Dùng hai file này cho PostgreSQL và MySQL, các loại còn lại dùng icon `database-alt.svg` chung, mỗi loại trong một danh sách ánh xạ loại → icon để thay icon thật chỉ là đổi một dòng. Icon vẽ bằng `currentColor` theo quy ước của `internal/ui/icons.go`.

### 7.2. Mô hình hồ sơ

Spec 6.1 đã có trường `SQLitePath`. `connection.Profile` thêm `SQLitePath` và cờ `ReadOnly` (chỉ có nghĩa với SQLite). `Driver.Supported()` thêm SQLite. `Validate()` kiểm theo loại:

- PostgreSQL: giữ nguyên các luật hiện có.
- SQLite: bắt buộc tên và `SQLitePath`; không yêu cầu host, port, username, timeout hay pool. Các trường này được bỏ qua và không được lưu giá trị vô nghĩa vào `connections.json`.

Hồ sơ cũ không có `SQLitePath` phải đọc được như trước. `Normalize()` không được làm mất hồ sơ thuộc loại chưa hỗ trợ.

### 7.3. Form theo loại

`connForm` giữ loại đang chọn. Các ô hiển thị và thứ tự theo bảng ở mục 6. Giá trị mặc định của một kết nối PostgreSQL mới không đổi. Nút Browse chạy `mygo.Dialog.Open` trong goroutine (không gọi trong view), lọc `.db`, `.sqlite`, `.sqlite3` và "All files", rồi đưa đường dẫn về qua `Window.Update`.

### 7.4. Adapter SQLite

Package `internal/database/sqlite/`, implement `SQLAdapter` và `SQLSessionFactory` giống `internal/database/postgres/`:

- Hierarchy: `ListSchemas` trả đúng một schema `main`; `ListTables` liệt kê tables và views từ `sqlite_master`, bỏ qua bảng nội bộ `sqlite_*`.
- Metadata: cột, kiểu khai báo, nullability, default, primary key (gồm composite), foreign key và incoming reference, index, trigger, theo khả năng của engine. Phần nào không đọc được thì báo trạng thái riêng của phần đó, như PostgreSQL.
- Giá trị: SQLite gán kiểu theo từng giá trị (dynamic typing). Adapter phải đọc giá trị thật của ô và hiển thị trung thực, không suy từ kiểu khai báo của cột.
- Quoting identifier và placeholder theo SQLite; mọi giá trị người dùng đi qua bound parameter.
- Phân trang, filter, sort theo cùng `PageRequest` như PostgreSQL.
- Save: một transaction, kiểm tra giá trị cũ trước khi ghi, xung đột và dòng trùng khớp nhiều hơn một đều rollback. Hủy chỉ được trước `COMMIT`; không tự retry write.
- Cấu hình connection riêng cho SQLite (một file cục bộ, không áp dụng pool kiểu network database), tôn trọng `ReadOnly`, và xử lý file bị khoá bởi tiến trình khác bằng lỗi rõ ràng hơn là treo UI.
- Đóng connection và giải phóng handle file khi disconnect, kể cả khi đóng tab hay thoát app.

### 7.5. SQL tab

Dùng lại executor và kết quả của Phase 2.4. Phần riêng cho SQLite là bộ tách statement và xác định statement tại con trỏ theo cú pháp SQLite (chuỗi, comment, quoted identifier, và `CREATE TRIGGER … BEGIN … END` chứa dấu chấm phẩy). Ngoài ra là cách đếm số dòng bị ảnh hưởng và lỗi cú pháp theo driver.

### 7.6. Giữ cho D1 làm được sau này

Phần SQLite-dialect (quoting, parser, truy vấn metadata, ánh xạ giá trị) nên tách khỏi phần mở file bằng driver, để một nguồn truy cập khác có thể dùng lại sau này. Phase này **không** viết client D1, không thêm field cho D1 và không đưa D1 vào lưới chọn loại.

## 8. Tiêu chí hoàn thành

- Tạo kết nối SQLite qua hai bước, test được, lưu được; sửa và xoá được; mật khẩu không liên quan.
- Mở file SQLite trong explorer, xem, lọc, sort, phân trang; sửa ô rồi Save/Revert đúng như PostgreSQL.
- Chạy SQL trên SQLite, hủy được; file SQL cục bộ hoạt động như cũ.
- Luồng PostgreSQL không bị hồi quy.
- `go build ./...`, `go vet ./...`, `go test ./...` chạy sạch; các test cần PostgreSQL thật được báo là skip nếu thiếu biến môi trường. Phần kiểm tra thủ công trên app do người dùng làm và nghiệm thu, agent không tự coi là đã nghiệm thu.

## 9. Checklist người dùng kiểm tra (dự kiến)

- [ ] Bước chọn loại: icon placeholder, loại chưa hỗ trợ bị mờ, Back/Cancel, sửa kết nối bỏ qua bước chọn.
- [ ] File hợp lệ, không có quyền, bị khoá, không phải SQLite, đường dẫn Unicode; cleanup khi disconnect.
- [ ] Khoá chính composite, NULL, cột generated, view, tên reserved, rollback khi lỗi.
- [ ] Selection/statement tại con trỏ, phân trang/filter/sort; hồi quy PostgreSQL.

## 10. Những gì để người triển khai quyết định

- Cách phát hiện và hiển thị chế độ chỉ đọc khi file hoặc thư mục không ghi được (ngoài checkbox của người dùng).
- Bảng không có khoá chính, bảng `WITHOUT ROWID`, và việc có dùng `rowid` làm định danh dòng hay không: mặc định theo đúng quy tắc hiện có của PostgreSQL về khi nào được sửa; chỉ khác nếu có bằng chứng và phải báo lại.
- Cách xử lý database được `ATTACH` và schema `temp`: mặc định chỉ hiện `main`.
- Cách đặt chế độ journal và busy timeout phù hợp với một ứng dụng desktop mở file do người dùng chọn.
- Bố cục chi tiết của lưới chọn loại (số cột, kích thước ô).

## 11. Những lựa chọn tôi đã tự quyết (người dùng chưa xác nhận)

- Loại chưa hỗ trợ hiện mờ kèm nhãn "Soon" thay vì ẩn đi.
- Sửa kết nối bỏ qua bước chọn loại và không cho đổi loại.
- Placeholder: dùng `postgresql.svg` và `my-sql.svg` có sẵn, các loại còn lại dùng icon database chung.
- Bước 2 có nút "Back".
- Checkbox "Open read-only" cho SQLite.
- Mặc định chỉ hiện schema `main`.
- Lưu `SQLitePath` và `ReadOnly` trong `Profile`.
- SQL tab trên SQLite giữ cùng mô hình phiên và giao dịch như PostgreSQL (`BEGIN` ở lần chạy này, `COMMIT` ở lần sau). Nếu giao dịch đang mở giữ khoá file làm Save hoặc đọc bảng bị chặn (`SQLITE_BUSY`), app báo lỗi "database đang bị khoá" rõ ràng, không treo UI, và có test cho tình huống này.
- Kiểm tra xung đột khi Save so sánh giá trị cũ theo đúng loại lưu trữ thật của giá trị đó (ví dụ số nguyên `1` khác chuỗi `'1'`), không so sánh qua chuỗi đã format. Cách làm cụ thể do người triển khai chọn.
- Nếu `ncruces/go-sqlite3` không build được không CGO thì dừng và báo người dùng.

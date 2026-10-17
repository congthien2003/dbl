# Phase 3.2 — MySQL/MariaDB: thiết kế

- Ngày: 2026-10-17.
- Nguồn: [implementation-phases.md](../../implementation-phases.md) mục 11, [specs.md](../../specs.md) FR-001–FR-008, FR-010, mục 6–10, 12.2.
- Plan: [2026-10-17-phase-3-2-mysql.md](../plans/2026-10-17-phase-3-2-mysql.md).
- Trạng thái: đã được người dùng duyệt.

## 1. Mục đích

Phase 3.1 đã cho DBL làm việc với SQLite. Phase 3.2 thêm engine network relational đầu tiên ngoài PostgreSQL. Người dùng làm cùng các việc như với PostgreSQL trên server MySQL hoặc MariaDB: quản lý kết nối, duyệt database và bảng, xem/lọc/sắp xếp/phân trang, sửa ô rồi Save/Revert, chạy SQL.

Phase này không tạo trải nghiệm mới. Nó thêm một adapter cho các hợp đồng `database.SQLAdapter` và `database.SQLSessionFactory` sẵn có, cùng vài điều chỉnh nhỏ ở form và explorer.

## 2. Phạm vi

1. Hồ sơ MySQL trong bước chọn loại sẵn có, form mạng dùng chung với PostgreSQL (port mặc định 3306, Database tùy chọn).
2. Adapter `internal/database/mysql/` dùng `github.com/go-sql-driver/mysql` v1.10.1.
3. Explorer theo mô hình database-as-schema.
4. Metadata, paging, filter, sort, Save, SQL tab theo dialect MySQL.

## 3. Không làm (non-goals)

- MySQL 5.7 hoặc cũ hơn, MariaDB dưới 10.6: vẫn kết nối nhưng hiện cảnh báo "chưa kiểm thử", không có nhánh xử lý riêng.
- Lệnh client `DELIMITER`, `SOURCE`, `USE` qua menu. `USE` vẫn chạy được như một statement trong SQL tab.
- SSH tunnel, socket Unix/named pipe, client certificate, xác thực plugin ngoài những gì driver hỗ trợ sẵn.
- Mọi mục ngoài phạm vi ở spec 1.3.

## 4. Quyết định của người dùng

- **Database tùy chọn.** Ô Database chỉ là database mặc định cho SQL tab và được mở sẵn trong explorer. Explorer liệt kê mọi database user thấy được như schema, ẩn `information_schema`, `performance_schema`, `mysql`, `sys`.
- **Phiên bản hỗ trợ chính thức:** MySQL 8.0+ và MariaDB 10.6+.
- **Dependency:** thêm `github.com/go-sql-driver/mysql`.
- **Integration test:** chạy trên container Docker MySQL 8.4 và MariaDB 11.4. Giữ container sau khi xong.

## 5. Thiết kế

### 5.1. Kết nối

- Cấu hình dựng bằng `mysql.NewConfig()` và `mysql.NewConnector`, không qua chuỗi DSN. Mật khẩu không bao giờ nằm trong một chuỗi có thể lọt vào thông báo lỗi.
- Các cờ cố định:
  - `ParseTime=false`
  - `InterpolateParams=false`: giá trị luôn là bound parameter.
  - `MultiStatements=false`: SQL tab tự tách statement.
  - `ClientFoundRows=true` cho Save: affected rows là số dòng **khớp** điều kiện, không phải số dòng thực sự đổi.
  - `AllowAllFiles=false`, `AllowCleartextPasswords=false`, `AllowOldPasswords=false`, `AllowFallbackToPlaintext=false`.
- TLS map theo bốn mode chung, không có `preferred` và không fallback về plaintext:
  - `disable`: không TLS.
  - `require`: mã hoá, không kiểm chứng chỉ.
  - `verify-ca`: kiểm chuỗi chứng chỉ, không kiểm hostname.
  - `verify-full`: kiểm chuỗi và hostname.
- Khi TLS tắt, driver lấy public key RSA của server để xác thực `caching_sha2_password`. Mode `disable` vốn đã không chống được MITM, nên điều này không làm giảm thêm mức an toàn.

### 5.2. Explorer

- Node database của kết nối hiện `host:port`.
- Mỗi database là một schema (`TableRef{Schema: db, Name: table}`).
- Database mặc định được mở sẵn khi kết nối.

### 5.3. Giá trị

- Trang dữ liệu đọc mỗi cột không phải binary dưới dạng `CAST(col AS CHAR)`. Chuỗi hiển thị vì thế luôn là cách server tự in giá trị, giống hệt kết quả của SQL tab (text protocol), dù truy vấn có bound parameter hay không.
- Binary (`binary`, `varbinary`, `blob*`, `bit`) chỉ đọc độ dài.
- Số nguyên có `IntBits` (8/16/24/32/64) và cờ `Unsigned` mới trong `ColumnMetadata`. `TINYINT(1)` là số, không phải boolean.

### 5.4. Save

- Một transaction trên một connection riêng, chạy với `sql_mode` strict để server không âm thầm cắt hay ép giá trị. `sql_mode` được khôi phục trước khi trả connection về pool.
- So sánh giá trị cũ chính xác và NULL-safe. Điều kiện so sánh bytes của dạng text mà server in ra (`CAST(col AS CHAR)` ép sang binary) với text người dùng đã thấy. Cách này không bị collation `_ci` hay PAD SPACE bỏ qua thay đổi chữ hoa/khoảng trắng, và không báo xung đột giả vì khác charset.
- 0 dòng khớp là xung đột, nhiều hơn 1 dòng là không an toàn; cả hai rollback toàn bộ.
- Bảng có engine không hỗ trợ transaction (theo `information_schema.ENGINES.TRANSACTIONS`) thì chỉ đọc, kèm lý do.

### 5.5. SQL tab

- Mỗi tab có một pool riêng một connection.
- Tách statement theo cú pháp MySQL:
  - Chuỗi có backslash escape, trừ khi bật `NO_BACKSLASH_ESCAPES`.
  - `#`, `-- `, `/* */`, `/*! */`.
  - Thân `BEGIN … END` của routine, trigger và event.
- Dòng `DELIMITER` bị từ chối kèm hướng dẫn chọn khối để chạy.
- Trạng thái giao dịch lấy từ cờ `SERVER_STATUS_IN_TRANS` mà server gửi trong mỗi gói OK/EOF (driver giữ cờ này).
- Cancel gửi `KILL QUERY` từ connection khác, để giữ được phiên và transaction.
- Số dòng ảnh hưởng theo nghĩa thông thường của MySQL (số dòng đổi).

## 6. Tiêu chí hoàn thành

- Tạo, test, sửa, xoá kết nối MySQL; duyệt database và bảng; xem, lọc, sort, phân trang; sửa và Save/Revert; chạy SQL và hủy được.
- PostgreSQL và SQLite không hồi quy.
- `go build`, `go vet`, `go test` sạch (trừ hai test đã lỗi từ trước). Integration test chạy trên MySQL 8.4 và MariaDB 11.4.
- Người dùng tự test app và nghiệm thu.

## 7. Checklist người dùng kiểm tra

- [ ] Connect/TLS, database/schema navigation, metadata và lỗi quyền.
- [ ] Unicode/collation, precision, NULL, composite keys, filter operators và pagination.
- [ ] Conflict, rollback, affected-row semantics, transaction support (MyISAM chỉ đọc), MySQL 8.x và MariaDB 10.6+.
- [ ] SQL tab: statement tại con trỏ hoặc vùng chọn, Cancel, BEGIN/COMMIT, procedure chạy bằng cách chọn khối.

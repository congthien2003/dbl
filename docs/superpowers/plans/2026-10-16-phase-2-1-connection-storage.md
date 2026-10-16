# Phase 2.1 — Connection & Storage

Trạng thái: **ĐÃ TRIỂN KHAI — chờ người dùng nghiệm thu** (spec và plan đã được duyệt 2026-10-16; xem mục "Kết quả triển khai" cuối file). Chưa commit.

**Review độc lập:** do tool `advisor` (model mạnh hơn, đọc toàn bộ transcript) thực hiện, **không phải** role `reviewer` đóng gói được dispatch (môi trường không có agent type này) — cần người dùng chấp nhận. Verdict vòng 1: **Blocked** (B1, B2). Disposition: B1 → S1; B2 → S4 và S2 (đã đối chiếu lại `pgx/conn.go:254`, `pgconn/config.go:700-725` và `config.go:884-1010`); N1 → S8; N2 → S2; N3, N4 → S5; N5 → S7; N6 → S1/S6. Sau sửa, S1/S4 được main tự đối chiếu với source, **chưa được review độc lập lần hai**.

Nguồn: [docs/specs.md](../../specs.md) FR-001, FR-010 (phần connection), mục 5.1, 6.1, 8.1, 8.2, 8.4, 8.5, 9, 10, 11.2, 12; [docs/implementation-phases.md](../../implementation-phases.md) mục 3, mục 6 (Phase 2.1), P0-B/P0-C/P0-D (E27–E30, E37, PR5).

## 0. Phạm vi, quyền và bằng chứng

**[S0.1] Phạm vi.** Chỉ Phase 2.1. Không làm Phase 2.2 (explorer database/schema/table thật, Properties, đọc dữ liệu), 2.3, 2.4, driver khác, Redis. Hết Phase 2.1 người dùng kiểm tra và test app rồi mới quyết định commit (AGENTS.md). **Agent không commit, không push.**

**[S0.2] Quyền chạy lệnh.** AGENTS.md và memory của người dùng cho phép agent tự chạy `go vet`, `go test`, `go build` (kể cả `CGO_ENABLED=0 go build`) trong repo. Điều này thay thế câu "agent không tự chạy build/test" ở `implementation-phases.md` mục 1 và ở Phase 1 plan. **Vẫn cần hỏi trước:** `go get`/`go mod tidy` thêm dependency (có truy cập mạng), đổi toolchain. Test tích hợp với PostgreSQL thật chỉ chạy khi có biến môi trường `DBL_TEST_PG_*`; không có thì `t.Skip`, và báo cáo là `Not run` trừ khi người dùng tự chạy với instance cô lập.

**[S0.3] Dependency mới (cần người dùng cho phép `go get`).** `github.com/jackc/pgx/v5` **v5.11.0** (tag mới nhất trên Go proxy ngày 2026-10-16, `go 1.25.0` < `go 1.27.2` của module). Kéo theo `pgpassfile`, `pgservicefile`, `puddle/v2`, `x/sync`, `x/text`. Không thêm dependency khác (UUID dùng `crypto/rand`; stdlib Go 1.27.2 không có package uuid — đã kiểm tra `GOROOT/src`).

**[S0.4] Bằng chứng đã có (đọc source pgx v5.11.0 tải từ proxy vào thư mục tạm, chưa vào repo).**
- `pgconn.Config` phải được tạo bằng `ParseConfig`, nếu không `ConnectConfig` panic (`pgconn/pgconn.go:146`, `config.go:113`). Không thể tự dựng `Config` từ đầu.
- `ParseConfig` **luôn gộp** biến môi trường `PG*` (`parseEnvSettings`, `config.go:710-722`), `.pgpass` (khi password rỗng, `config.go:609-617`), service file, và mặc định `sslmode=prefer` (fallback sang không TLS, không xác thực chứng chỉ, `config.go:894-905,943`). Vì vậy builder của DBL phải chủ động chặn các nguồn ngầm này (xem S4).
- `ParseConfigError` che password theo kiểu best-effort (`errors.go:137-148`); `ConnectError` chỉ in `user`/`database` (`errors.go:67-75`).
- `stdlib.OpenDB(pgx.ConnConfig, ...OptionOpenDB) *sql.DB` (`stdlib/sql.go:241`).
Mọi kết luận về runtime (Credential Manager, `Window.Update` khi thoát, pgx cancel/timeout thực tế) là `Not run` cho tới khi có test/ người dùng chạy.

## 1. Hành vi cần đạt (acceptance)

- **A1** Tạo/sửa/đổi tên/xóa connection profile; tạo/đổi tên/xóa folder một cấp; chuyển connection giữa folder và root. Kết quả được lưu và khôi phục sau khi mở lại app.
- **A2** Xóa folder có connection: hỏi "chuyển connection về root" hoặc "Cancel". Không có nhánh xóa luôn connection.
- **A3** Test connection, Connect, Disconnect chạy ngoài UI thread; UI không đơ khi connect/timeout. Hủy được khi đang connect/test. Nhiều connection hoạt động cùng lúc, session/pool không dùng chung.
- **A4** Password không bao giờ nằm trong `connections.json`, log, thông báo lỗi, hay chuỗi kết nối. Có credential store thì lưu ở đó; không có thì giữ trong bộ nhớ theo session và nói rõ cho người dùng.
- **A5** Khởi động không connect, không gọi credential store, không truy cập mạng (chỉ đọc `settings.json` và `connections.json`).
- **A6** Sửa/xóa/ngắt kết nối profile đang kết nối xử lý các tab của connection đó **trước** khi hủy tài nguyên; lỗi và hủy giữa chừng không để lại pool/goroutine/kết nối rò rỉ.
- **A7** Lỗi có category theo spec mục 9, thông điệp hữu ích, đã loại password.

## 2. Spec theo khối

### [S1] Lưu trữ `connections.json`
- File `~/.dbl/connections.json`, `version: 1`, ghi nguyên tử (cùng cơ chế temp+sync+rename của `settings.json`; dùng chung helper). Schema mở rộng spec 11.2 bằng kiểu có type: `tls {mode, rootCert}`, `connectTimeoutSec`, `pool {maxOpen, maxIdle}`. Giá trị 0/thiếu → mặc định (TLS theo S4, timeout 10 s, pool 2/1).
- File **không phân tích được** (JSON hỏng) → đổi tên thành `connections.json.bad`, chạy với danh sách rỗng, báo thông báo. File version mới hơn **hoặc bất kỳ lỗi đọc nào khác "không tồn tại"** (khóa file, thiếu quyền, lỗi I/O) → **chế độ chỉ đọc**: không dùng danh sách rỗng để ghi đè; hiển thị phần đọc được (nếu có), chặn mọi thao tác tạo/sửa/xóa/chuyển với thông báo rõ, không bao giờ ghi vào file. (Khác `SettingsStore.Load`, vốn coi mọi lỗi đọc là "dùng mặc định" và vẫn ghi sau đó — với connections việc đó xóa mất toàn bộ profile đã lưu.) Test riêng cho lỗi đọc (ví dụ đường dẫn là thư mục) chứng minh file không bị ghi.
- `folderId` trỏ tới folder không tồn tại → coi như ở root khi tải (lưu lại ở lần ghi sau). Driver lạ (hand-edit) → giữ nguyên trong file, hiển thị "unsupported", không connect được; **Edit bị chặn** (form sẽ ghi đè driver thành PostgreSQL), Rename/Move/Delete vẫn được.
- Ghi file chạy ngoài UI thread (snapshot mới nhất thắng, ghi tuần tự), flush đồng bộ khi thoát. Lỗi ghi **hiện ra UI** (banner/status) và giữ trạng thái trong bộ nhớ để thử lại ở lần ghi kế; `settings.json` hiện chỉ ghi log nên không tái dùng nguyên văn.
- Validation (khi Save form): tên bắt buộc (cắt khoảng trắng, ≤ 128 ký tự), host (không chứa dấu phẩy, không phải đường dẫn tuyệt đối), port 1–65535, database, username bắt buộc; timeout 1–300 s; pool maxOpen ≥ 1, 0 ≤ maxIdle ≤ maxOpen; TLS mode hợp lệ; `rootCert` (nếu có) phải là đường dẫn file đọc được khi dùng verify-*. ID là UUID v4 sinh bằng `crypto/rand`, không lấy từ tên.

### [S2] Credentials
- Dùng `mygo.Secrets.Service("DBL")` — **service cố định, không dùng service mặc định** (mặc định gắn với `identifier` `com.example.sbdb`, sẽ đổi ở Phase 5 và khác nhau giữa `mygo dev`/`go run`/bản cài, làm mất password đã lưu). Key = `SecretRef` = `dbl/<profile-uuid>`, không dùng tên profile.
- `SecretRef != ""` nghĩa là profile **có dùng password**. `SecretRef == ""` → connect không password.
- Chỉ gọi Secrets từ worker goroutine (Secrets có thể chặn khi hệ thống hỏi mở khóa; trên main thread nó bơm sự kiện UI trong lúc chờ, view có thể bị gọi lại). Giao diện `CredentialStore{Get,Set,Delete}` được inject; test dùng fake, không chạm Credential Manager, và có test xác nhận khởi động gọi credential store **0 lần**.
- Lưu: ghi `connections.json` trước, rồi `Set` secret. `Set` lỗi hoặc `errors.ErrUnsupported` → password giữ trong bộ nhớ theo session cho profile đó (xóa khi profile bị xóa/đổi password/thoát app) và UI báo "password chỉ lưu cho phiên này".
- Connect: `Get` thất bại/không có/không hỗ trợ mà `SecretRef != ""` → kiểm tra bộ nhớ session → nếu chưa có, hộp thoại hỏi password (không ghi đĩa; không có checkbox "lưu").
- Form sửa: **không bao giờ đọc secret để điền form**. Ô password trống = giữ nguyên; nhập giá trị mới = thay; checkbox "Remove saved password" (chỉ hiện khi `SecretRef != ""`) = xóa secret và đặt `SecretRef = ""`. Xóa profile xóa luôn secret (nếu lỗi xóa: báo nhưng vẫn xóa profile).
- **Nguồn password của Test connection trên form sửa:** ô có nhập → dùng giá trị đó; ô trống, "Remove" không tick và `SecretRef != ""` → worker lấy password đã lưu (store, rồi bộ nhớ session; không có thì kết quả "cần password" và form báo nhập); "Remove" tick → không password. Test không bao giờ ghi secret.

### [S3] Mô hình dữ liệu và validation
- `internal/connection`: `DriverType` (đủ 5 hằng của spec 6.1, chỉ `postgres` kết nối được ở 2.1), `Profile` (`ID, Name, FolderID, Driver, Host, Port, Database, Username, SecretRef, TLS, ConnectTimeout, Pool`), `Folder{ID, Name}`, `Catalog` (folders + profiles theo thứ tự) với các thao tác thuần: add/rename/delete profile, add/rename folder, delete folder (move-to-root), move profile. Folder một cấp. Không ràng buộc tên duy nhất.

### [S4] Kết nối PostgreSQL (`internal/database/postgres`)
- `database/sql` + `pgx/v5/stdlib` (`stdlib.OpenDB(pgx.ConnConfig)`). Builder gọi **`pgx.ParseConfig`** (không phải `pgconn.ParseConfig`: `pgx.ConnConfig` có guard `createdByParseConfig` riêng, `conn.go:254`). Cách duy nhất được chọn: **(a) dựng chuỗi kết nối bằng `net/url` từ các trường không bí mật của profile** — `host, port, dbname, user, sslmode, sslrootcert` cùng `sslcert=`, `sslkey=` rỗng và các khóa ghim còn lại — để `configTLS` có sẵn của pgx (bao gồm kiểm tra chain của verify-ca và `ServerName` của verify-full lấy từ host) tạo `TLSConfig`; **chỉ sau đó** mới đặt `Config.Password` bằng `Password` (kể cả rỗng, để loại giá trị từ `.pgpass`) và khẳng định `Fallbacks` rỗng. Password **không bao giờ** nằm trong chuỗi. Không tự dựng `tls.Config`.
- Host không được chứa dấu phẩy (tạo multi-host fallback) hay là đường dẫn tuyệt đối (unix socket, bỏ qua TLS): validate ở S1 và ở builder.
- **Môi trường libpq bị bỏ qua có chủ đích:** `ParseConfig` gộp 24 biến trong `parseEnvSettings` (`PGHOST, PGPORT, PGDATABASE, PGUSER, PGPASSWORD, PGPASSFILE, PGAPPNAME, PGCONNECT_TIMEOUT, PGSSLMODE, PGSSLKEY, PGSSLCERT, PGSSLSNI, PGSSLROOTCERT, PGSSLPASSWORD, PGSSLNEGOTIATION, PGTARGETSESSIONATTRS, PGSERVICE, PGSERVICEFILE, PGTZ, PGOPTIONS, PGMINPROTOCOLVERSION, PGMAXPROTOCOLVERSION, PGCHANNELBINDING, PGREQUIREAUTH`), và `PGSERVICE/PGSERVICEFILE` không thể ghim bằng khóa chuỗi (khóa `service` rỗng vẫn kích hoạt đọc service file). Vì vậy `main` gọi `postgres.IgnoreLibpqEnvironment()` **một lần ở đầu process** để `os.Unsetenv` toàn bộ danh sách này (DBL không bao giờ dùng chúng — cấu hình chỉ đến từ profile); chuỗi có thêm các khóa ghim (`target_session_attrs=any`, `connect_timeout`, `channel_binding`, `require_auth`, `min/max_protocol_version`, `sslnegotiation`) làm lớp thứ hai. Test: đặt giá trị rác cho cả 24 biến, gọi `IgnoreLibpqEnvironment` rồi builder; kỳ vọng không lỗi parse và mọi trường lấy từ profile. Test riêng: khi `.pgpass` giả có mục khớp, `Password` vẫn là password của DBL.
- TLS là kiểu có type: `disable | require | verify-ca | verify-full` (+ `rootCert` tuỳ chọn); `prefer`/`allow` không được cung cấp, không có fallback im lặng sang plaintext. **Mặc định cho profile mới: do người dùng quyết (xem câu hỏi duyệt).** Lỗi "server không hỗ trợ TLS" phải gợi ý đổi TLS mode (dù mặc định nào). Client certificate: ngoài phạm vi (`sslcert/sslkey` bị ghim rỗng nên `~/.postgresql/*` không được đọc — cần xác nhận bằng test, U6).
- Pool: `SetMaxOpenConns`/`SetMaxIdleConns` theo profile (mặc định 2/1, cấu hình được trong JSON).
- Timeout: áp cả `ConnectTimeout` lẫn context deadline cho dial + `PingContext`. Connect chỉ thành công sau `PingContext` thành công.
- **Test connection** dùng `*sql.DB` riêng, không đăng ký vào manager, luôn `Close()` trên mọi đường (thành công, lỗi, hủy, đến muộn).

### [S5] Manager và async (`internal/connection` + `internal/app`)
- Manager sở hữu: trạng thái từng connection (`Disconnected | Connecting | Connected | Failed(err)`), pool, cancel func, password theo session. Mọi thay đổi state chạy trên UI thread; worker chỉ giữ dữ liệu thuần.
- `internal/app` không import UI: manager nhận `post func(func())` được inject (main gán `win.Update`; test dùng hàng đợi giả). Hợp đồng completion gate theo P0-C: khóa `connectionID + attemptID`; kết quả chỉ được áp dụng khi app chưa thoát, profile còn tồn tại, attempt còn là attempt hiện hành, `ctx.Err()==nil`. **Thành công đến muộn** (sau cancel/edit/delete/disconnect/quit) → đóng pool ngay, không đăng ký.
- Thoát app: hủy mọi attempt và đóng mọi pool đồng bộ (có giới hạn thời gian), **không** dựa vào `post` (hàm bị bỏ khi loop dừng — E28). Vì `main.go` hiện flush settings ở cả ba đường (`OnWillQuit`, `OnClosed`, `OnQuitConfirmed`) do thứ tự hook sau `win.Destroy` chưa chắc, việc đóng pool và flush `connections.json` đi cùng ba đường đó, idempotent (gọi nhiều lần an toàn).
- **"Cần password"** là kết quả riêng của attempt (không phải lỗi): attempt kết thúc → mở hộp thoại hỏi password; nhập xong mới bắt đầu attempt **mới**; Cancel hộp thoại → về `Disconnected`. Kết quả hộp thoại cũng đi qua gate (profile có thể bị xóa/sửa khi hộp thoại đang mở).
- PR5 (hoãn từ Phase 1): test bắt buộc cho kết quả đến sau khi đích đã mất (profile xóa, tab đóng, attempt bị thay).
- Không tự retry. Một connection tối đa một attempt đang chạy; Connect khi đang Connecting/Connected là no-op; Disconnect khi đang Connecting = Cancel.

### [S6] UI
- Explorer chuyển từ `mock.Catalog()` sang cây app-level (folder → connection) lấy từ `Catalog`, dựng lại khi catalog đổi. Connection ở 2.1 là nút lá (database/schema/table thật thuộc 2.2). Mỗi connection hiện trạng thái (ngắt / đang kết nối / đã kết nối / lỗi). Double-click/Enter trên connection = Connect (no-op nếu đã kết nối hoặc đang kết nối); Disconnect/Cancel qua context menu.
- Nút cuối sidebar "+ Connection", "+ Folder" (theo layout spec 3.1). Context menu: Connection → Connect/Disconnect/Cancel, Test, Edit…, Rename…, Move to ▸, New SQL tab, Delete…; Folder → New connection here, Rename…, Delete…. Hộp thoại dùng `ui.Modal`, ô password dùng `TextInput.Password()`, Select/Checkbox của MyGo. Form có: Name, Folder, Driver (chỉ PostgreSQL), Host, Port, Database, Username, Password, TLS mode (+ CA path khi verify-*), Timeout; lỗi validation hiện cạnh từng ô; nút Test connection (có Cancel khi đang chạy, hiện kết quả/ lỗi inline) / Cancel / Save.
- Status bar: thay "Mock data — no database connected" bằng trạng thái của connection đang chọn/tab hoạt động (`Connected: <name>`, `Connecting…`, lỗi). Lỗi connect cũng hiện ở hàng của connection.
- Cây mock bị gỡ khỏi UI (tuỳ quyết định của người dùng). Gói `internal/mock` và `Workspace.OpenTable` giữ lại để test grid/tab của Phase 1 tiếp tục chạy; `newTestShell`, `explorer_reveal_test` và các test dùng `local-pg` sẽ được viết lại (**thay đổi test có chủ đích**).

### [S7] Profile đang được dùng
- **Delete luôn hỏi xác nhận** (xóa secret không hoàn tác được), nêu cả số tab bị đóng và edit chưa lưu sẽ mất.
- Disconnect, và Edit đổi host, port, database, username, TLS, timeout, pool hoặc password trên connection đang `Connecting`/`Connected` (kể cả khi **không** có tab): hộp thoại xác nhận "áp dụng và ngắt kết nối" (nêu số tab và edit chưa lưu nếu có); đồng ý thì: hủy attempt → đóng tab của connection → đóng pool → áp dụng thay đổi (không tự kết nối lại). Bảo vệ nội dung SQL/grid chi tiết thuộc 2.3/2.4 nhưng luồng này gọi cùng `Tab.Dirty()`.
- Chỉ đổi tên / chuyển folder: không reconnect, không đóng tab (tên tab hiển thị tên connection khi cần được cập nhật).
- Xóa folder chỉ chuyển connection về root (A2), nên không đóng tab hay ngắt kết nối.

### [S8] Lỗi (`internal/database/errors.go`)
- `database.Error{Kind, Message, Err}` với đủ category spec mục 9. 2.1 phân loại được: authentication (SQLSTATE `28P01`, `28000`), permission denied (`42501`), database không tồn tại (`3D000`, kind connection failure), timeout (deadline/`net` timeout), canceled (`context.Canceled`), connection failure (dial/DNS/TLS), file access (CA cert, JSON). Thông điệp giữ host/port/database/user, **luôn loại password** (thay đúng chuỗi password bằng `•••` trước khi hiển thị/log; **bỏ qua khi password rỗng** vì `strings.ReplaceAll(s, "", x)` chèn giữa mọi rune; password rất ngắn, dưới 4 ký tự, che theo toàn bộ lần xuất hiện nhưng không được là lý do để che cả thông điệp) — có test cho password rỗng, ngắn, có ký tự đặc biệt/Unicode: không lỗi nào, kể cả `ParseConfigError`, chứa password. Không log chuỗi kết nối.

### [S9] Verification
- Agent chạy: `go vet ./...`, `go test ./...`, `go build ./...`, `CGO_ENABLED=0 go build ./...` (khẳng định pgx không kéo CGO).
- Unit/UI test (agent chạy): catalog/folder ops, validation, store (atomic, corrupt, newer-version read-only, dangling folder, round-trip), credential flow với fake (startup 0 call, unsupported → session, secret theo UUID, xóa theo profile), manager (stale/late success/cancel/delete/quit/test-closed, đóng resource), builder PostgreSQL (hostile env, mỗi TLS mode, không DSN chứa password), classify lỗi, redaction, UI bằng `ui.Tester` (form, validation, confirm flows).
- Integration (env-gated `DBL_TEST_PG_*`): connect, sai password, database không tồn tại, TLS không hợp lệ, timeout (listener nhận mà không trả lời), cancel. `Not run` nếu không có instance.
- Người dùng (theo checklist Phase 2.1 trong `implementation-phases.md`): CRUD, kết nối thật, sai password/TLS/timeout/cancel, nhiều connection, sửa/xóa profile đang dùng, mở lại app, JSON/log không chứa secret, fallback khi không có credential store.

## 3. Không thuộc phạm vi (non-goals)

Liệt kê database/schema/table, Properties, đọc dữ liệu, SQL execute, lưu file SQL, driver MySQL/SQLite/SQL Server/Redis, client certificate, SSH tunnel, import/export profile, đồng bộ cloud, "lưu password" từ hộp thoại hỏi password, đổi `identifier` của app, gói/ký ứng dụng.

## 4. Rủi ro và điều chưa biết

- **U1** Credential Manager với service `DBL` trên máy thật, và hành vi khi không có store: `Not run`; test dùng fake.
- **U2** `Window.Update` sau khi cửa sổ đóng/khi thoát: chỉ source (E27–E28) cộng test với post giả; hành vi runtime thật do người dùng quan sát (xóa/ngắt khi đang connect).
- **U3** Cách gắn context menu lên hàng của `ui.Outline` và sự kiện chuột phải/phím menu — cần đọc API ở bước plan; chưa có bằng chứng runtime.
- **U4** Hành vi pgx stdlib khi cancel/timeout giữa chừng — xác minh bằng test tích hợp (listener giả) và đọc source khi dependency đã nằm trong repo.
- **U5** Tương tác focus của `ui.Modal` + `TextInput`/`Select`: Tester kiểm được logic, bàn phím/focus thật là `Not run`.
- **U6** Việc ghim `sslcert=`/`sslkey=` rỗng có thật sự ngăn pgx đọc `~/.postgresql/*` không (giá trị mặc định thư mục home được nạp trong `defaultSettings`, `config.go:430-436`; `configTLS` ở `config.go:884-1010` đọc file khi cả hai khác rỗng) — plan phải có test xác nhận bằng thư mục home giả; nếu không ghim được thì dùng `ParseConfigOptions`/ghi đè như phương án dự phòng đã nêu cho người dùng.
- Rủi ro đã biết: churn API MyGo `v0.x` (pin `v0.5.5`); `ConnectError` in `user`/`database` trong thông điệp (chấp nhận, không phải bí mật).

---

# PLAN — đã duyệt (2026-10-16)

Spec ở trên đã được người dùng duyệt (2026-10-16) với các quyết định: **TLS mặc định `require`**; **gỡ cây mock khỏi UI**; **cho phép agent chạy `go get github.com/jackc/pgx/v5@v5.11.0` và `go mod tidy`**; chấp nhận advisor thay cho role `reviewer`. Tất cả task do **main session** làm inline; không dùng worker (các task chung hợp đồng và cần phán đoán).

## P-A. Điểm làm rõ/refine so với spec (cần duyệt cùng plan)

1. **Disconnect không có tab thì không hỏi** (người dùng đã chủ động bấm); có tab thì hỏi. Edit/Delete vẫn hỏi như S7 (đây là làm rõ S7, vốn gộp Disconnect vào danh sách "luôn hỏi").
2. "Test connection" ở context menu dùng password đã lưu/session; kết quả hiện ở status bar (không thêm toast). Test trong form hiện inline.
3. `Port`/`Timeout` nhập bằng `TextInput` (chuỗi) rồi parse, không dùng `NumberInput` (nhận `float64`).
4. Trạng thái mở/đóng folder trong explorer không được lưu (không có trong spec 11).
5. Refactor nhỏ code Phase 1 để tái dùng: tách `writeFileAtomic` khỏi `SettingsStore.Save`; tổng quát `SettingsSaver` thành `Saver[T]` (giữ `NewSettingsSaver` và test cũ). Test Phase 1 hiện có bảo vệ hai thay đổi này.
6. Zero value `app.State{}` phải tiếp tục dùng được (rất nhiều test Phase 1 và `main_test.go` dựng `&app.State{...}` trực tiếp): `Catalog` là field giá trị, `Conns` nil-safe (`Status` của id lạ = `Disconnected`).
7. Test Phase 1 sẽ **bị viết lại có chủ đích**: `newTestShell` (không còn mở `local-pg`), `TestExplorerShowsMockTree`, `TestOpenTableFromExplorerCreatesTabAndRefocuses`, bốn test trong `explorer_reveal_test.go` (chuyển sang folder/connection thật), `TestSidebarWidthComesFromSettings` (đổi điểm neo "Work"). Các test grid/tab/guard dùng `openTable` → `Workspace.OpenTable` giữ nguyên.
8. Nếu có `go tool mygo vet` (nhắc ở P0-C) thì chạy như một kiểm tra thêm; không có/ lỗi môi trường thì báo `Not run`.

## P-B. Cấu trúc file và trách nhiệm

Hướng phụ thuộc (không vòng): `database` ← `connection` ← `database/postgres`; `connection` + `database` ← `storage`, `app` ← `ui` ← `main`. `connection`, `database` và `app` không import `mygo/ui`; chỉ `storage/credentials.go` và `main.go` import gói gốc `mygo` (Secrets, Window).

| File | Việc |
| --- | --- |
| `internal/connection/models.go` (+`_test`) | `DriverType`, `TLSMode`, `Profile`, `Folder`, `Catalog` + thao tác thuần, validation, `NewID` (UUID v4), mặc định |
| `internal/connection/credentials.go` | `CredentialStore` interface, `ErrSecretNotFound`, `ErrCredentialsUnavailable`, `SecretRefFor(id)`, `PasswordChange` |
| `internal/connection/manager.go` (+`_test`) | `Manager`, `Status`, `Connector`, attempt/gate, test connection, session password, `CloseAll` |
| `internal/database/errors.go` (+`_test`) | `Kind`, `Error`, `Redact` |
| `internal/database/adapter.go` | `Adapter{Ping, Close}` (mở rộng ở 2.2) |
| `internal/database/postgres/{config,connect,errors}.go` (+`_test`, `integration_test.go`) | builder `pgx.ParseConfig`, `IgnoreLibpqEnvironment`, `Connect`, `classify` |
| `internal/storage/atomic.go`; sửa `config.go` | `writeFileAtomic` dùng chung |
| `internal/storage/connections.go` (+`_test`) | `ConnectionsStore`, `ConnectionsFile`, read-only mode |
| `internal/storage/credentials.go` (+`_test`) | `SystemCredentials` (`mygo.Secrets.Service("DBL")`), `mapSecretError` |
| `internal/app/saver.go`; `saver_test.go` | `Saver[T]` |
| `internal/app/workspace.go` (+test) | `TabsOf`, `CloseConnection` |
| `internal/app/state.go`; `internal/app/connections.go` (+`_test`) | field mới, thao tác Catalog/Conns, pending confirm |
| `internal/ui/explorer.go` (viết lại), `internal/ui/connform.go`, `internal/ui/dialogs.go` (+test) | explorer thật, context menu, form, hộp thoại |
| `internal/ui/shell.go`, `internal/ui/sql_editor.go`, các test hiện có | status bar, nút "+ Connection/+ Folder", hộp thoại, tên connection ở SQL tab, test viết lại |
| `main.go`, `main_test.go` | nối store/creds/manager/post/quit |

## P-C. Todo (đúng một task active tại một thời điểm; tick khi có bằng chứng)

- [x] **T1** Mô hình connection + Catalog + validation
- [x] **T2** Lỗi có category + Redact
- [x] **T3** Storage: atomic write, `connections.json`, credentials
- [x] **T4** Manager (async, gate, test connection, session password)
- [x] **T5** PostgreSQL: dependency, builder, connect, classify
- [x] **T6** App layer: saver tổng quát, workspace helpers, State actions + confirm
- [x] **T7** UI: explorer thật + context menu + test viết lại
- [x] **T8** UI: form connection, hộp thoại, status bar, SQL tab
- [x] **T9** `main.go` nối dây + quit
- [x] **T10** Verify toàn bộ + review diff + báo cáo

## T1 — Mô hình connection (`internal/connection/models.go`)

- **Goal/acceptance:** A1, A2, S1 (validation), S3. Catalog thuần, không I/O.
- **Preconditions:** không.
- **Changes:**
  1. Kiểu: `DriverType` (5 hằng spec 6.1) với `Supported() bool` (chỉ `postgres`); `TLSMode` (`disable|require|verify-ca|verify-full`), `DefaultTLSMode = TLSRequire`; `TLSOptions{Mode, RootCert}`; `PoolOptions{MaxOpen, MaxIdle}`; `Profile{ID, Name, FolderID, Driver, Host, Port, Database, Username, SecretRef, TLS, ConnectTimeoutSec, Pool}` với tag JSON theo S1 (`folderId`, `secretRef`, `connectTimeoutSec`, `tls`, `pool`); `Folder{ID, Name}`.
  2. `Profile.Normalized()` điền mặc định (TLS=require, timeout 10, pool 2/1) và cắt khoảng trắng; `Profile.Validate() error` trả `[]FieldError{Field, Message}` (name, host — không dấu phẩy/không đường dẫn tuyệt đối, port 1–65535, database, username, timeout 1–300, pool, TLS mode, `rootCert` bắt buộc đọc được khi verify-* kiểm bằng hàm `fileReadable` inject được để test).
  3. `Catalog{Folders, Profiles, Rev}` (zero value dùng được): `AddFolder/RenameFolder/DeleteFolder(id, moveToRoot)`, `AddProfile/UpdateProfile/DeleteProfile`, `MoveProfile(id, folderID)`, `Profile(id)`, `Folder(id)`, `ProfilesIn(folderID)`; mọi mutation tăng `Rev`; `Snapshot()` copy sâu. `Normalize()` sau load: folderId mồ côi → root, bỏ ID trùng, folder/ profile thiếu ID bị loại (ghi vào danh sách cảnh báo trả về).
  4. `NewID()` UUID v4 từ `crypto/rand` (đặt version/variant bits).
- **Edge cases:** tên có khoảng trắng đầu/cuối; tên Unicode; folder rỗng/ID không tồn tại; move vào folder không tồn tại → lỗi; xóa folder không tồn tại → no-op có lỗi; driver lạ vẫn nằm trong Catalog; UUID không trùng trong 10 000 lần sinh (test).
- **Verification:** `go vet ./internal/connection/` và `go test ./internal/connection/` — kỳ vọng pass; test: validation từng trường, folder ops (một cấp, delete move-to-root), normalize, UUID format.
- **Owner:** main.

## T2 — Lỗi (`internal/database/errors.go`, `adapter.go`)

- **Goal/acceptance:** A7, S8.
- **Changes:** `Kind` (mười hai category spec §9) với `String()`; `Error{Kind, Message, Hint, Err}` implement `Error()/Unwrap()`; `Hint` chứa gợi ý (ví dụ đổi TLS mode); `Redact(text string, secrets ...string) string` — bỏ qua secret rỗng, thay mọi lần xuất hiện bằng `•••`; `Adapter` interface `Ping(ctx) error`, `Close() error`.
- **Edge cases:** secret rỗng (không đổi chuỗi), secret ngắn 1–3 ký tự, secret có regex-meta/Unicode, nhiều secret, secret là chuỗi con của secret khác (thay chuỗi dài trước).
- **Verification:** `go test ./internal/database/` — kỳ vọng pass.
- **Owner:** main.

## T3 — Storage (`internal/storage`)

- **Goal/acceptance:** A1, A4, A5, S1, S2.
- **Preconditions:** T1.
- **Changes:**
  1. `atomic.go`: `writeFileAtomic(dir, name string, data []byte, perm)` chuyển từ thân `SettingsStore.Save` (tạm→Sync→rename, MkdirAll `0o700`); `SettingsStore.Save` gọi nó. Hành vi không đổi (test cũ).
  2. `connections.go`: `ConnectionsVersion = 1`; `ConnectionsFile{Version, Folders, Connections}` (JSON như spec 11.2 + trường typed); `ConnectionsStore{dir, mu, readOnly, reason}`; `Load() (connection.Catalog, []string warnings, error)`:
     - không tồn tại → catalog rỗng, không lỗi;
     - JSON hỏng → rename `connections.json.bad`, catalog rỗng, `ErrCorrupt` (ghi được);
     - `version > 1` → catalog rỗng (không hiển thị dữ liệu không hiểu), `readOnly`, `ErrUnsupportedVersion`;
     - **mọi lỗi đọc khác** → `readOnly`, `ErrUnreadable` (bọc nguyên nhân);
     - `ReadOnly() (bool, string)`; `Save(connection.Catalog) error` trả lỗi nếu `readOnly`.
  3. `credentials.go`: `SystemCredentials{}` implement `connection.CredentialStore` bằng `mygo.Secrets.Service("DBL")`; `mapSecretError(err)`: `mygo.ErrSecretNotFound`→`connection.ErrSecretNotFound`, `errors.Is(err, errors.ErrUnsupported)`→`connection.ErrCredentialsUnavailable` (bọc), còn lại giữ nguyên. Không gọi từ main thread (chỉ Manager gọi từ worker).
- **Edge cases:** `connections.json` là thư mục (lỗi đọc → read-only, **file/thư mục không bị ghi**); file rỗng 0 byte (coi là hỏng); quarantine thất bại; hai Save đồng thời; ghi thất bại giữa chừng để lại file tạm → được xóa; thư mục `.dbl` chưa tồn tại.
- **Verification:** `go test ./internal/storage/` — kỳ vọng pass, gồm test cũ của `settings.json` (chứng minh refactor không đổi hành vi) và test lỗi đọc không ghi đè.
- **Owner:** main.

## T4 — Manager (`internal/connection/manager.go`)

- **Goal/acceptance:** A3, A5, A6; S2 (luồng password), S5.
- **Preconditions:** T1, T2, T3 (interface credentials ở T1).
- **Changes:**
  1. Kiểu: `Status` (`Disconnected|Connecting|Connected|Failed|NeedsPassword`); `Connector func(ctx, Profile, password string) (database.Adapter, error)`; `Manager` giữ `*Catalog`, `CredentialStore`, `Connector`, `post func(func())`, `entries map[string]*entry`, bộ đếm attempt, cờ `closed`. `entry{status, err *database.Error, attempt, cancel, adapter, sessionPassword *string}`.
  2. `Connect(id)` (UI thread): bỏ qua nếu Connecting/Connected/profile lạ/driver chưa hỗ trợ; tăng attempt, `context.WithTimeout(timeout+margin)`, đặt `Connecting`; worker: lấy password (`creds.Get` khi `SecretRef != ""`; lỗi/not-found/unavailable → session password → nếu không có: kết quả `needsPassword`; `SecretRef == ""` → không password) rồi gọi `Connector`; kết quả đi qua `post` → `finish`.
  3. `finish(id, attempt, result)` chạy trong `post`: **gate** = `!closed && entry tồn tại && entry.attempt==attempt && ctx.Err()==nil && profile còn trong Catalog`; không qua gate → nếu có adapter thì đóng ở goroutine, không đăng ký. Qua gate: `Connected`/`Failed(err)`/`NeedsPassword`.
  4. `Disconnect(id)`: cancel attempt, attempt=0, đóng adapter ở goroutine, `Disconnected`. `SubmitPassword(id, pw)` đặt session password rồi `Connect`; `CancelPrompt(id)` → `Disconnected`. `Forget(id)` = Disconnect + xóa session password + xóa entry. `SetSessionPassword/ClearSessionPassword`.
  5. `StorePassword(id, pw)` / `DeleteStoredPassword(ref)` (worker): `Set`/`Delete`; lỗi hoặc unsupported → báo qua callback `OnCredentialIssue(id, err)` (UI thread) và giữ session password. `Test(req TestRequest, done func(TestResult)) (cancel func())`: dựng profile tạm, lấy password theo S2 (typed / stored / none), `Connector` → `Ping` → **luôn đóng adapter**; `done` chạy trong gate riêng (test chưa bị cancel, manager chưa đóng); kết quả muộn → đóng adapter, không gọi `done`.
  6. `CloseAll(timeout)`: `closed=true`, cancel mọi attempt/test, đóng mọi adapter song song, chờ tối đa `timeout`; idempotent; **không dùng `post`**.
  7. `Status(id)`, `Err(id)`, `Active() []string` đọc được trên UI thread; mọi method nil-safe trên `*Manager == nil`.
- **Edge cases:** Connect hai lần liên tiếp; Disconnect khi Connecting rồi connector trả thành công muộn (adapter bị đóng, status giữ `Disconnected`); xóa profile khi Connecting; sửa profile (attempt cũ bị bỏ); `post` bị bỏ khi loop dừng (CloseAll vẫn đóng); `Connector` panic không được nuốt; connector bỏ qua `ctx` (vẫn bị gate); password prompt: profile bị xóa khi prompt mở → `SubmitPassword` là no-op; nhiều connection độc lập (không chia sẻ entry/adapter); credential `Get` chặn lâu → vẫn hủy được bằng cancel (gate loại).
- **Verification:** `go vet ./internal/connection/`, `go test -race ./internal/connection/` nếu `-race` chạy được trên môi trường (cần CGO; nếu không thì `go test`, báo `-race` là `Not run`). Test dùng `fakePost` (hàng đợi, chạy tay), `fakeConnector` (kênh điều khiển, đếm Close), `fakeCreds` (đếm lời gọi). Bắt buộc: **PR5** (kết quả đến sau khi profile bị xóa/attempt bị thay/cancel/quit), **0 lời gọi credential khi khởi tạo/Load**, đóng đủ adapter ở mọi đường, không goroutine rò rỉ (đếm `Close` và chờ kênh).
- **Owner:** main.

## T5 — PostgreSQL (`internal/database/postgres`)

- **Goal/acceptance:** A3, A4, A7; S4, S8.
- **Preconditions:** T1, T2, T4 (Connector signature); **dependency mới**: `go get github.com/jackc/pgx/v5@v5.11.0` rồi `go mod tidy` (đã được duyệt), kiểm tra diff `go.mod/go.sum` chỉ thêm pgx và deps của nó, rồi `CGO_ENABLED=0 go build ./...`.
- **Changes:**
  1. `config.go`: `IgnoreLibpqEnvironment()` (`os.Unsetenv` đúng 24 biến của S4; có test đối chiếu danh sách với `parseEnvSettings` trong source đã đọc); `buildConfig(p connection.Profile, password string) (*pgx.ConnConfig, error)`: dựng URL bằng `net/url` (`user`, `host:port`, `/dbname`, query `sslmode`, `sslrootcert` nếu có, `sslcert=`, `sslkey=`, `connect_timeout`, `target_session_attrs=any`, `channel_binding`, `require_auth`, `min/max_protocol_version`, `sslnegotiation`) → `pgx.ParseConfig`; kiểm `Fallbacks` rỗng; đặt `Password`; `RuntimeParams["application_name"]="DBL"`. Chuỗi URL **không bao giờ** chứa password và không được log.
  2. `connect.go`: `Connect(ctx, p, password) (database.Adapter, error)`: `buildConfig` → `stdlib.OpenDB` → `SetMaxOpenConns/SetMaxIdleConns` → `PingContext(ctx)` (ctx có deadline); lỗi → `db.Close()` + `classify` + `Redact(…, password)`. `Adapter` bọc `*sql.DB` (`Ping`, `Close`).
  3. `errors.go`: `classify(err, p, password) *database.Error`: `*pgconn.PgError` (`28P01`/`28000`→Authentication; `42501`→PermissionDenied; `3D000`→ConnectionFailure "database không tồn tại"), `context.DeadlineExceeded`/`net.Error.Timeout()`→Timeout (ConnectionFailure kèm cờ), `context.Canceled`→Canceled, lỗi TLS ("server does not support TLS"/tls handshake) → ConnectionFailure + `Hint` "đổi TLS mode", file CA → FileAccess, còn lại ConnectionFailure; message qua `Redact`.
- **Edge cases:** password rỗng/ký tự `@:/?#%` và Unicode (không phá URL vì không nằm trong URL); host IPv6 (`[::1]`); host có dấu phẩy/đường dẫn tuyệt đối bị từ chối; `rootCert` thiếu/không phải PEM; môi trường hostile (24 biến rác) trước/ sau `IgnoreLibpqEnvironment`; `~/.pgpass` giả có mục khớp (dùng `HOME`/`USERPROFILE` tạm) → vẫn dùng password của DBL; thư mục `~/.postgresql` giả có `postgresql.crt/key` → không bị đọc (U6; nếu pgx vẫn đọc thì dùng phương án dự phòng đã nêu trong spec và báo lại); context đã hủy trước khi Connect.
- **Verification:** `go get …` và `go mod tidy` (được duyệt); `go vet ./internal/database/...`; `go test ./internal/database/...`; `CGO_ENABLED=0 go build ./...` (kỳ vọng thành công — nếu không, dừng và báo vì phá ràng buộc CGO-free); `integration_test.go` (env `DBL_TEST_PG_HOST/PORT/USER/PASSWORD/DB` + tuỳ chọn `DBL_TEST_PG_TLS`): kết nối OK, sai password, database không tồn tại, TLS không hợp lệ, timeout bằng listener TCP nhận mà không trả lời, cancel — **`Not run` nếu người dùng không cung cấp instance** (skip rõ ràng).
- **Owner:** main.

## T6 — App layer (`internal/app`)

- **Goal/acceptance:** A1, A2, A6; S1 (read-only, lỗi ghi hiện UI), S2, S7.
- **Preconditions:** T1–T4.
- **Changes:**
  1. `saver.go`: `Saver[T any]` (cùng cơ chế debounce; delay 0 = ghi ngay, coalesce); `type SettingsSaver = Saver[storage.Settings]`; `NewSettingsSaver` giữ chữ ký; thêm `NewSaver`.
  2. `workspace.go`: `TabsOf(connectionID) []*Tab`; `CloseConnection(connectionID) (closed int, dirty int)` (đếm dirty trước khi đóng).
  3. `state.go`: field `Catalog connection.Catalog`, `Conns *connection.Manager`, `ConnectionsReadOnly string` (lý do, rỗng = ghi được), `PendingConn *PendingConn`, `PendingFolderDelete string`, hook `ScheduleCatalogSave func(connection.Catalog)`; `RequestQuit` giữ nguyên.
  4. `connections.go` (thao tác UI gọi, tất cả trên UI thread, đều chặn khi `ConnectionsReadOnly != ""` bằng `Notice`): `AddFolder`, `RenameFolder`, `RequestDeleteFolder` (folder rỗng xóa ngay; có connection → `PendingFolderDelete`), `ConfirmDeleteFolder` (move-to-root), `CancelDeleteFolder`, `MoveConnection`, `RenameConnection`; `SaveProfile(p, change PasswordChange) (applied bool, errs []FieldError)` — profile mới: gán ID/`SecretRef`, thêm vào Catalog, lưu, `Conns.StorePassword`; sửa: nếu connection `Connecting/Connected` **và** thay đổi trường ảnh hưởng kết nối (S7) → `PendingConn{Apply}` (kèm số tab/dirty), ngược lại áp dụng; `RequestDisconnect(id)` (hỏi chỉ khi có tab), `RequestDelete(id)` (luôn hỏi), `ConfirmConn()` (hủy attempt → `CloseConnection` → `Conns.Disconnect/Forget` → Catalog → lưu → xử lý secret), `CancelConn()`; `Connect(id)`, `CancelConnect(id)`, `SubmitPassword(id, pw)`, `CancelPasswordPrompt(id)`; `persistCatalog()` gọi `ScheduleCatalogSave(Catalog.Snapshot())`; `SetCatalogSaveError(err)` đặt/xóa `Notice` "Could not save connections: …" (giữ cho đến lần ghi thành công).
  5. Không import UI; không gọi credential/mạng trực tiếp (chỉ qua `Conns`).
- **Edge cases:** profile đổi sang driver không hỗ trợ; xóa profile đang `Connecting` với tab SQL mở; edit chỉ đổi tên/folder khi đang Connected (không hỏi, không ngắt); hai `PendingConn` liên tiếp (cái sau thay cái trước, cái trước coi là Cancel); `ConfirmConn` khi profile đã bị xóa; read-only: mọi thao tác trả lỗi và Catalog không đổi; `Notice` lỗi ghi bị xóa sau lần ghi thành công; `State{}` zero vẫn gọi được `Connect` (no-op) mà không panic.
- **Verification:** `go test ./internal/app/` — kỳ vọng pass (gồm test Phase 1 cũ + test mới cho từng nhánh Confirm/Cancel, read-only, thứ tự "đóng tab → ngắt → xóa").
- **Owner:** main.

## T7 — UI: explorer thật (`internal/ui/explorer.go`, tests)

- **Goal/acceptance:** A1, A2, S6 (explorer, context menu, nút cuối sidebar).
- **Preconditions:** T6. Đọc API `Element.ContextMenu`, `Menu.Submenu`, `OutlineState` tại source `v0.5.5` trước khi code (U3); nếu `ContextMenu` không gắn được lên nội dung hàng của `Outline`, dừng và báo.
- **Changes:**
  1. `explorer` dựng cây từ `st.Catalog` (id folder `folder:<id>`, id connection = profile ID); `rebuild()` khi `Catalog.Rev` đổi, giữ lựa chọn theo ID và trạng thái mở; folder mở sẵn lúc đầu; giữ cơ chế fade `reveal/endPass/born` (chỉ bám cha→con thật).
  2. Hàng connection: tên + chỉ báo trạng thái (text/glyph dùng `t.Success`, `t.Warning`, `t.Danger`, `t.TextMuted`; có `Label` cho truy cập) + `Tooltip` lỗi khi `Failed`. Enter/double-click: connection → `st.Connect`; folder → mở/đóng.
  3. Context menu theo S6 (Connect/Disconnect/Cancel theo trạng thái, Test, Edit… (vô hiệu với driver lạ), Rename…, Move to ▸ (Root + từng folder), New SQL tab, Delete…; Folder: New connection here, Rename…, Delete…). Các lựa chọn ghi vào biến cục bộ trong vòng lặp hàng rồi xử lý **sau** `Outline` (theo `docs/ui/context-menu.md`).
  4. Sidebar: nút "+ Connection", "+ Folder" cuối cột (spec 3.1); trạng thái rỗng "No connections yet".
  5. Cập nhật `newTestShell`/helpers: shell không còn mở `local-pg`; thêm helper dựng Catalog mẫu và `Manager` giả.
- **Edge cases:** catalog rỗng; folder rỗng; tên rất dài (`SingleLine`); thay đổi catalog khi đang chọn một hàng đã bị xóa (row=-1); read-only (menu/nút vô hiệu + lý do); `ReduceMotion`.
- **Verification:** `go vet ./internal/ui/` + `go test ./internal/ui/` — kỳ vọng pass; test mới: cây hiển thị, chuột phải mở menu đúng mục theo trạng thái (`RightClick`, `Menu`, `ChooseMenuItem`), fade vẫn hoạt động với folder, không animate khi khởi động.
- **Owner:** main.

## T8 — UI: form và hộp thoại (`connform.go`, `dialogs.go`, `shell.go`, `sql_editor.go`)

- **Goal/acceptance:** A1–A4, A6, A7; S6, S7.
- **Preconditions:** T7.
- **Changes:**
  1. `connform.go`: `ui.Modal` với Name, Folder (`Select`), Driver (`Select` chỉ "PostgreSQL"), Host, Port, Database, Username, Password (`TextInput(...).Password()`), "Remove saved password" (`Checkbox`, chỉ khi sửa và `SecretRef != ""`), TLS mode (`Select`), CA path (chỉ verify-*), Timeout (s); lỗi validation cạnh từng ô (dùng `Palette.Error`); `Test connection` ↔ `Cancel test` với spinner và kết quả inline (`Success`/lỗi + `Hint`); Save/Cancel; Esc/click ngoài đóng form như Cancel (form không có dữ liệu cần bảo vệ) và hủy test đang chạy. Form giữ state của chính nó, không đọc secret để điền.
  2. `dialogs.go`: tên folder (create/rename), đổi tên connection, `AlertDialog` cho: xóa folder (Move to root / Cancel), xóa profile, ngắt kết nối/áp dụng sửa (nêu số tab, edit chưa lưu), hộp thoại nhập password (Modal + `Password()`; Connect/Cancel). Các hộp thoại đọc từ `State.PendingConn`, `PendingFolderDelete`, `Conns.Status == NeedsPassword`.
  3. `shell.go`: status bar thay "Mock data — no database connected" bằng trạng thái connection đang chọn/tab đang hoạt động; hiện `Notice` (kể cả lỗi lưu và read-only); `sql_editor.go`: "Connection: <tên> — <trạng thái>" (id lạ → "none"); `Ctrl+N` giữ nguyên.
  4. Mọi lời gọi chặn nằm ngoài view; view chỉ gọi `State` rồi `c.Invalidate()`.
- **Edge cases:** Port/timeout không phải số; tên rỗng; host rỗng; đổi driver lạ; form mở lại sau khi Test chạy xong muộn (kết quả bị bỏ vì token không khớp); profile bị xóa khi form sửa đang mở (Save báo lỗi, không tạo lại); read-only; lỗi không có `Hint`; Unicode trong mọi ô; password prompt khi profile bị đổi.
- **Verification:** `go test ./internal/ui/` — kỳ vọng pass; test: validation hiển thị lỗi đúng ô, Save không có lỗi tạo profile, password không xuất hiện trong `tt.Texts()`, nhánh Save/Cancel/Move to root, prompt password gửi `SubmitPassword`, status bar. Focus/bàn phím thật của `Modal` là `Not run`.
- **Owner:** main.

## T9 — `main.go` nối dây

- **Goal/acceptance:** A5, A6; S5 (thoát), S1.
- **Preconditions:** T3–T8.
- **Changes:**
  1. Dòng đầu `main`: `postgres.IgnoreLibpqEnvironment()`.
  2. Dựng `ConnectionsStore` từ `storage.DefaultDir()` (không có thư mục → `ConnectionsReadOnly` = "Home folder not found…"), `Load()` → `st.Catalog`, `st.ConnectionsReadOnly`, `Notice` theo lỗi (hỏng/`.bad`, version mới, không đọc được), cảnh báo normalize.
  3. `app.NewSaver(connStore, 0, onError)` với `onError` → `post` → `st.SetCatalogSaveError`; `st.ScheduleCatalogSave = saver.Schedule`.
  4. `connection.NewManager(&st.Catalog, storage.SystemCredentials{}, postgres.Connect, nil)`; trong `WhenReady` sau khi tạo cửa sổ: `mgr.SetPost(win.Update)`.
  5. Thoát: một hàm idempotent `shutdown()` = `mgr.CloseAll(2*time.Second)` + `connSaver.Flush()` + `saver.Flush()`, gắn vào `OnWillQuit`, `win.OnClosed` và `OnQuitConfirmed` (ba đường hiện đã flush settings).
  6. Không có lời gọi mạng/credential trước khi người dùng tác động (A5).
- **Edge cases:** không có thư mục home; `connections.json` read-only; thoát khi đang Connecting; đóng cửa sổ hai đường cùng lúc (idempotent).
- **Verification:** `go build ./...`, `go test .` (`main_test.go`: thêm test `loadConnections` cho các nhánh notice; test `TestStartupMakesNoCredentialOrConnectorCalls` với fake). `Not run`: chạy app thật (người dùng).
- **Owner:** main.

## T10 — Verify, review, báo cáo

1. Chạy: `go vet ./...`, `go test ./...`, `go build ./...`, `CGO_ENABLED=0 go build ./...` (PowerShell: `$env:CGO_ENABLED='0'; go build ./...`), thử `go tool mygo vet ./...` (nếu tồn tại). Ghi output thật; test tích hợp PG là `Not run` nếu không có `DBL_TEST_PG_*`.
2. Tự review `git diff`: scope, debug code, log có chứa secret (grep `password`, `Printf`), không file ngoài kế hoạch, không commit.
3. Phase 2.1 chạm nhiều subsystem và dữ liệu bảo mật nên dùng **một lượt review độc lập** (advisor) trước khi báo hoàn tất.
4. Báo cáo theo mức bằng chứng: Source / Static+automated / Build / Runtime (`Not run`), kèm checklist người dùng tự kiểm tra (mục 6 của `implementation-phases.md`) và lệnh chạy integration test với instance PostgreSQL cô lập. Không commit.

## P-D. Mapping acceptance → task

| Acceptance | Task |
| --- | --- |
| A1 CRUD + lưu/khôi phục | T1, T3, T6, T7, T8, T9 |
| A2 xóa folder | T1, T6, T8 |
| A3 async, nhiều kết nối, hủy | T4, T5, T9 |
| A4 không lộ password | T2, T3, T4, T5, T8 |
| A5 khởi động sạch | T3, T4, T9 |
| A6 profile đang dùng, không rò rỉ | T4, T6, T8, T9 |
| A7 lỗi có category | T2, T5, T8 |

## P-E. Self-review

Đã đối chiếu: mọi acceptance A1–A7 có task; thứ tự phụ thuộc không vòng (T1→T2→T3→T4→T5→T6→T7→T8→T9→T10; T5 chỉ cần T1/T2/T4); test Phase 1 bị đổi đã liệt kê (P-A.7); không có placeholder; không worker; `go get` chỉ ở T5 (đã được duyệt). Không dùng review plan độc lập riêng vì không có API/schema/migration mới ngoài spec đã review, ngoại trừ ba điểm làm rõ ở P-A.1–3. Rủi ro còn lại: U3 (context menu trên Outline) và U6 (client cert mặc định của pgx) có thể buộc đổi thiết kế cục bộ — nếu xảy ra tôi dừng và báo trước khi đổi phạm vi.

---

# Kết quả triển khai (2026-10-16)

## Bằng chứng theo mức

| Mức | Kết quả |
| --- | --- |
| Source | Diff được main tự review: phạm vi đúng plan, không debug code, log chỉ ghi lỗi file (không có secret); advisor review độc lập lần cuối: **không có blocker kỹ thuật sau khi xử lý 3 phát hiện** (bên dưới) |
| Static / automated | `gofmt -l .` sạch; `go vet ./...` sạch; `go test ./...` **pass** ở 9 package (chạy lại sau các sửa theo review) (`.`, `app`, `connection`, `database`, `database/postgres`, `grid`, `mock`, `storage`, `ui`); `go test -count=8 ./internal/connection/` ổn định; `go tool mygo vet ./...` không báo lỗi |
| Build | `go build ./...` và `CGO_ENABLED=0 go build ./...` thành công (pgx không đòi CGO) |
| Runtime | Smoke: build `dbl.exe` rồi chạy 6 giây với `USERPROFILE` tạm: cửa sổ mở, frame đầu sau ~575 ms (`SBDB_TRACE_STARTUP`), không panic, không ghi file. **Chưa** tương tác UI bằng tay |
| `Not run` | Integration test PostgreSQL thật (`DBL_TEST_PG_*`; máy không có binary PostgreSQL, Docker cần tải image nên không tự chạy); `-race` (không có `gcc`, `CGO_ENABLED=0`); Windows Credential Manager với service `DBL`; hành vi `Window.Update` khi thoát app thật; focus/bàn phím thật của `Modal`/`Select`; TLS thật (verify-ca/verify-full) |

## Điểm quyết định / thay đổi so với plan

- U3 (context menu trên `Outline`): hoạt động — `Row(...).ContextMenu(...)` trong hàng của `Outline`, kiểm bằng `Tester.RightClick/Menu/ChooseMenuItem`.
- U6 (client cert/root cert mặc định của pgx): **xác nhận có thật** — pgx đọc `~/.postgresql/*` (Windows: `%APPDATA%\postgresql\*`) và `root.crt` mặc định; ghim `sslcert=`, `sslkey=`, `sslrootcert=` rỗng trong URL vô hiệu hóa chúng; test dựng home giả với file hỏng (mutation check: bỏ ghim thì test fail).
- Thêm `limbo` trong `Manager` (session của attempt đã xong nhưng kết quả chưa tới UI) để `CloseAll` đóng được cả khi `post` bị bỏ lúc thoát — plan ghi "không dựa vào post", bổ sung này làm điều đó đúng cho cả session đang bay.
- `Saver[T]` có thêm `OnSaved` để xóa thông báo lỗi ghi `connections.json` sau lần ghi thành công.
- Mật khẩu tối đa 2000 byte (giới hạn 2560 byte của `mygo.Secrets`), kiểm ở `ValidatePassword`.
- `Label` của `Text` làm `Tester.Texts()` chỉ thấy nhãn, nên text trạng thái/kết quả test không đặt `Label`.

## Phát hiện của review cuối và cách xử lý

1. **Luồng chạy của hook thoát** (`CloseAll` không có lock): đã đối chiếu source `mygo@v0.5.5` — `Application.prepareQuit` "runs the quit sequence on the main thread" và gọi `OnWillQuit` ở đó (`app.go:175-200`); `OnClosed` được `fire` trong `windowHandler.Closed()` (`window.go:1596-1638`, cùng luồng với `closingParents` "main thread only"); `Window.Destroy` đi qua `onMain`. Cả ba đường `shutdown()` đều ở main thread, cùng luồng với hàm do `Window.Update` chạy, nên `Manager` không cần mutex. Vẫn là bằng chứng ở mức source; `-race` là `Not run`.
2. **Form ở cửa sổ nhỏ nhất (800×500):** test mới cho thấy form tràn (nút Save ở y≈542, bị cắt). Đã sửa: vùng cuộn của form lấy chiều cao theo `Settings.Window.Height` (trừ phần "chrome" của hộp thoại, tối thiểu 160, tối đa 560); test `TestFormFitsTheSmallestWindow` khẳng định Save/Cancel/Test/kết quả nằm trong cửa sổ 800×500 kể cả form dài nhất (verify-* + dòng kết quả). Cuộn bằng chuột/bàn phím thật chưa kiểm (`Not run`).
3. **Sửa profile đã bị xóa:** `SaveProfile` với ID không rỗng nhưng không còn trong catalog trước đây tạo lại profile. Đã sửa: trả `connection.ErrNotFound`, không tạo lại, không ghi password; có test.
4. Thông báo `connections.json` hỏng nay phân biệt trường hợp không đổi tên được file (không có `.bad`, chuyển read-only); có test.

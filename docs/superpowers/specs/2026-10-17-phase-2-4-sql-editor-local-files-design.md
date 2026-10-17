# Phase 2.4 — SQL Editor & Local Files: Design

- Ngày: 2026-10-17.
- Trạng thái: thiết kế trong hội thoại đã được duyệt; bản spec này chờ người dùng review trước implementation plan.
- Nguồn: [implementation-phases.md, Phase 2.4](../../implementation-phases.md), [specs.md, FR-007/FR-008](../../specs.md).
- Nền tảng đã đọc: Go `1.27.2`, MyGo `v0.5.5`, pgx/v5 `v5.11.0`, source hiện tại sau Phase 2.3.
- Phạm vi chỉnh sửa của bước hiện tại: chỉ tạo tài liệu này. Chưa triển khai, chạy build/test/lint/probe hoặc commit.

## 1. Mục tiêu và điều kiện thành công

Hoàn tất luồng SQL của PostgreSQL MVP trong SBDb: mở nhiều SQL tabs, chọn đúng connection, chạy selection hoặc statement tại cursor, xem kết quả chỉ đọc có giới hạn, hủy truy vấn, mở và lưu file SQL cục bộ. Nội dung SQL không mất khi query/file operation lỗi hoặc người dùng hủy thao tác.

Người dùng đã chọn **session riêng cho từng SQL tab**, giữ transaction qua nhiều lần Execute: `BEGIN` → các câu SQL → `COMMIT` hoặc `ROLLBACK`. Ba phần đã được duyệt trong hội thoại: kiến trúc/session, execution/results, local files và bảo vệ nội dung.

Phase này không thay UI framework, nâng dependency, thêm engine, autocomplete/LSP, query history, file explorer riêng hoặc khả năng sửa SQL result cells. Không tự refresh Table Inspector sau SQL write; người dùng dùng Reload/F5 hiện có. PostgreSQL MVP chỉ được nghiệm thu khi Phase 2.3 cũng đạt yêu cầu.

## 2. Hiện trạng được xác nhận bằng đọc source

| Source | Hiện trạng | Điểm tích hợp |
| --- | --- | --- |
| `internal/ui/sql_editor.go` | Mỗi view có editor buffer riêng, SQL language và line numbers; connection hiện chỉ là label | Toolbar, connection selector, execution và document synchronization |
| `internal/app/workspace.go` | Có `TabSQL`, `NewSQL`, lifecycle tabs; SQL tabs chưa dirty; dùng `Table.ConnectionID` cho SQL tab | SQL document/session state và cleanup |
| `internal/database/adapter.go` | Relational contract có metadata, table paging và SaveChanges; chưa có free-form execution | Bổ sung capability mở SQL session |
| `internal/database/postgres/connect.go` | Adapter dùng `database/sql` pool qua pgx stdlib | Pool hiện có tiếp tục phục vụ Explorer/Table Inspector |
| `internal/connection/models.go` | Pool mặc định `MaxOpen=2`, `MaxIdle=1` | Không giữ SQL tab sessions trong pool này |
| `internal/connection/manager.go` | Có connection epoch, session context, poster và async shutdown | Thu hồi mọi SQL session theo lifecycle connection |
| `internal/app/ops.go` | Các tác vụ có cancellation/epoch gate; gate đọc loại kết quả nếu context đã kết thúc | SQL completion cần giữ terminal status/outcome, không sao chép nguyên gate đọc |
| `internal/app/state.go`, `save.go`, `connections.go` | Guards và SaveAll hiện thiên về table drafts | Điều phối SQL file save cùng các pending actions |
| `internal/storage/atomic.go` | Ghi temp cùng thư mục, Sync, Close, Rename | Đọc lại giới hạn trên Windows trước khi tái sử dụng cho SQL files |
| `main.go` | Window.Update cho UI dispatch, có close/quit hooks | File dialogs, file store và shutdown SQL sessions |

Đây là bằng chứng đọc source, không phải xác nhận runtime hoặc kết quả test của phase trước.

## 3. Kiến trúc và ownership

Luồng chính:

```text
UI: editor, toolbar, result grid, native dialogs
  → app: document, pending actions, operation IDs, connection epoch
    → query: execution orchestration, bounded result snapshots
      → database contracts
        → postgres: dedicated session, protocol, errors, transaction status

app → storage: SQL files, path validation, replacement, disk revision
connection lifecycle → cancel/close all owned SQL sessions
```

- `internal/ui/` giữ editor selection/cursor, scroll, splitter và grid selection; không gọi driver, ghép SQL hoặc đọc/ghi filesystem trực tiếp.
- `internal/app/` giữ nội dung document, saved baseline, file identity, revision và trạng thái execution. UI đồng bộ các thay đổi editor vào document trước action và trước close/quit guards; không chỉ dựa vào tab active để phát hiện dirty.
- `internal/query/` điều phối execution, cancellation và snapshots; không import MyGo. Boundary parser PostgreSQL ở file/package có tên dialect rõ ràng; không dùng chung thuật toán PostgreSQL như mặc định cho engine tương lai.
- `internal/database/` định nghĩa session factory/capability, SQL session, request/result/outcome và transaction status. SQL session tách khỏi contract table paging/SaveChanges; adapter không import UI.
- `internal/database/postgres/` mở dedicated connection và sở hữu protocol reader, command tags, transaction status, value decoding và error classification.
- `internal/storage/` sở hữu đọc/ghi SQL file, identity và disk revision. Reuse credential rules hiện có; không đưa password vào document, JSON, logs hoặc error text.

Các hợp đồng trên là yêu cầu hành vi; tên và chữ ký Go cụ thể sẽ được xác định trong implementation plan sau khi spec được duyệt.

## 4. SQL sessions và transaction

### 4.1. Tạo và giữ session

Mỗi SQL tab tạo một dedicated PostgreSQL connection khi Execute lần đầu; tạo tab hoặc Open file không tự connect/chạy SQL. Dedicated connection không lấy slot từ pool metadata/table. Dùng pgx đã pin, cùng profile, database, TLS và credential policy của connection đang mở.

Tab gắn với profile ID và epoch cụ thể. Mỗi tab chỉ chạy một tác vụ database tại một thời điểm; Execute tiếp khi đang chạy bị disable, không xếp write vào queue và không hủy write cũ để chạy write mới. Các tab khác nhau có thể chạy độc lập.

Factory phải đăng ký session mới trước khi bàn giao cho application. Disconnect/shutdown thu hồi cả session đang mở, đang tạo và kết quả session chưa được UI nhận; không để resource phụ thuộc duy nhất vào một callback UI có thể bị drop.

Để giới hạn tài nguyên, spec đề xuất tối đa **8 dedicated SQL sessions/profile và 16 cho toàn app**, tính cả session đang tạo. Đây là chi tiết bổ sung để review ở bản spec, không phải con số đã được đo. Đạt giới hạn thì báo rõ và yêu cầu đóng SQL tab/session khác; không âm thầm thu hồi session có transaction hoặc session state.

### 4.2. Transaction status

Tab hiển thị connection/database và một trong các trạng thái session: chưa mở, Idle, In transaction, Transaction failed, Session lost. Opening/Running/Canceling là trạng thái tác vụ riêng. Trạng thái transaction lấy từ server sau khi xử lý xong protocol response; không đoán bằng từ khóa SQL.

- Không tự thêm BEGIN/COMMIT quanh SQL của người dùng, không tự commit khi Save file, không tự retry query.
- `BEGIN`, `SET`, temporary objects và transaction state tồn tại trong session của đúng tab, không truyền sang tab khác.
- SQL error trong explicit transaction giữ trạng thái lỗi nếu server báo như vậy; cho phép người dùng Execute `ROLLBACK` hoặc lệnh recovery hợp lệ của PostgreSQL.
- Session mất không được tự reconnect rồi tiếp tục như transaction/session cũ vẫn còn. Lần Execute tiếp theo cần người dùng xác nhận mở session mới; UI nói rõ transaction, temporary objects và session settings cũ không được khôi phục.
- Cancel ưu tiên cancel request có thời hạn cleanup riêng. Nếu server kết thúc response và session còn hợp lệ thì giữ session; khi không thể đồng bộ protocol hoặc socket đã hỏng thì đóng session và báo Session lost.
- Khi người dùng xác nhận bỏ transaction, rollback best effort ngoài UI thread rồi đóng session với deadline; đóng connection là fallback nếu rollback thất bại. Không thể bảo đảm rollback một COMMIT đã gửi.

Mở session dùng connection timeout của profile. Execution timeout bắt đầu sau khi session sẵn sàng; cancel/protocol cleanup hoặc rollback-and-close có ngân sách riêng tối đa 2 giây, rồi cưỡng chế đóng transport nếu cần. Shutdown toàn app dùng deadline tổng 2 giây như lifecycle hiện tại, không nhân deadline theo số tabs.

## 5. Chọn statement và gửi SQL

1. Một selection không rỗng được gửi nguyên vẹn, không split để execute từng câu. Whitespace-only hoặc comment-only input không gửi query.
2. Nếu có nhiều selections rời nhau, yêu cầu chọn một block duy nhất; không nối các đoạn rời thành SQL khác với source.
3. Không có selection: boundary parser tìm statement chứa cursor, bao gồm dấu `;` kết thúc. Cursor ngay trên dấu `;` thuộc statement phía trước; khoảng trắng/comment giữa hai statement chọn statement kế tiếp nếu có, cuối file chọn statement trước đó.
4. Parser nhận biết single-quoted strings với doubled quotes, escape strings, quoted identifiers, line comments, nested block comments và tagged/untagged dollar quotes. Phân biệt byte offsets của parser với offsets của editor; tiếng Việt/Unicode trước cursor không làm lệch statement.
5. String escaping phải theo PostgreSQL session, bao gồm `standard_conforming_strings`. Nếu boundary không thể xác định an toàn, báo lý do và yêu cầu selection; không đoán để chạy nhầm câu.
6. Đây là parser boundary, không phải full SQL grammar. Procedural body được dollar-quote hỗ trợ như một phần statement; SQL-standard bodies như `BEGIN ATOMIC` không tự suy diễn boundary trong phase này, dùng selection nguyên khối.
7. Selected block nhiều statement được gửi một lần theo PostgreSQL simple-query semantics. App không hứa toàn bộ block atomic khi block chứa transaction control; command hoàn tất trước một lỗi không đồng nghĩa đã commit.
8. psql metacommands, `\copy` và COPY qua STDIN/STDOUT không có luồng streaming UI trong phase này. Chặn đường thực thi không hỗ trợ và báo rõ; nếu protocol bất ngờ chuyển COPY mode thì kết thúc/thu hồi có giới hạn, không treo session. COPY tới/từ file trên server là SQL theo quyền PostgreSQL, không phải thao tác local file của app.

SQL input cho Execute tối đa 8 MiB UTF-8, nhất quán với file limit. Không rewrite câu SQL bằng LIMIT/OFFSET hoặc lặp câu SQL khi người dùng đổi trang.

## 6. Execution và bounded results

### 6.1. Luồng execution

App chụp SQL input, tab ID, operation ID, session identity và epoch; công việc chạy ngoài UI thread. Kết quả về qua Window.Update/poster. Chỉ operation hiện tại của tab/session hiện tại được thay state; completion cũ không được áp dụng lên session mới.

Cancel/timeout là terminal status phải được hiển thị nếu vẫn thuộc operation hiện tại. Không dùng nguyên điều kiện `ctx.Err()==nil` của read gate để loại bỏ cả thông báo canceled/timeout hoặc command outcome. Bằng chứng server xác nhận hoàn tất được giữ ngay cả khi người dùng vừa bấm Cancel; không đổi một completion đã xác nhận thành khẳng định write chưa xảy ra.

### 6.2. Giới hạn đã duyệt và chi tiết bổ sung

| Giới hạn | Giá trị | Hành vi |
| --- | --- | --- |
| Trang UI | 100 rows | Phân trang local trên snapshot đã nhận |
| Snapshot mỗi result | 1.000 rows | Bỏ rows dư; giữ nhãn kết quả đã giới hạn |
| Snapshot toàn execution | 16 MiB payload | Ngừng giữ thêm row payload; các kết quả còn lại có thể chỉ có metadata/status |
| Tổng rows được đọc | Ngưỡng 100.000/execution | Nếu response cần đọc thêm thì cancel execution |
| Tổng result payload được đọc | Ngưỡng 64 MiB/execution | Tính cả dữ liệu bị bỏ; chạm ngưỡng thì cancel nếu response còn tiếp |
| Execution timeout | 60 giây | Bao gồm thời gian execute/đọc; cleanup có deadline riêng |
| Số result groups | 32/execution | Chi tiết bổ sung của spec: vượt thì dừng có thông báo |
| Protocol message body | 8 MiB | Chi tiết bổ sung của spec: chặn một message quá lớn trước cấp phát toàn bộ |
| Số cột | 4.096/result | Chi tiết bổ sung của spec: vượt thì dừng có thông báo |
| Metadata được giữ | 1 MiB/execution | Tính tên cột/type descriptions; vượt thì dừng |
| Notices được giữ | 32 notices, 4 KiB/notice | Cắt ngắn từng notice; bỏ notices dư và báo số bị bỏ |

Rows/result groups/metadata/notices đều phải có giới hạn hữu hạn. Ngân sách đọc 64 MiB tính cả payload metadata/notices và rows bị bỏ. Ngưỡng payload đọc được phát hiện theo message đã nhận; có thể nhận thêm tối đa một message trong giới hạn protocol, không hứa byte-accurate wire quota hoặc process RAM chỉ bằng 16 MiB. Kiểm tra số cột, kích thước metadata và giới hạn notices trước khi giữ các cấu trúc tương ứng. Các con số là policy, không phải benchmark.

Sau khi đủ snapshot, tiếp tục đọc và bỏ rows dư **chỉ trong hard limits** để nhận command status. Không gọi helper ReadAll/Read hoặc Close có hành vi drain không giới hạn mà không có cancellation/deadline/budget đang có hiệu lực. Tới hard limit, cancel trước cleanup; không tiếp tục drain vô hạn chỉ để giữ session.

UI phân biệt:

- **Completed:** server xác nhận hoàn tất; có thể kèm nhãn chỉ giữ 1.000 rows hoặc hết snapshot budget.
- **Canceled / Timed out / Stopped at result limit:** execution chưa được xác nhận hoàn tất; snapshot nếu có là partial.
- **Failed:** có SQL/permission/connection error, kèm command statuses đã xác nhận.

Cancel hoặc hard limit có thể abort explicit transaction. Lỗi mạng/COMMIT response bị mất có thể để lại write outcome unknown; phải nói rõ, không retry và không báo “nothing was written”.

### 6.3. Presentation và giá trị

Editor ở trên, result panel ở dưới với splitter; toolbar hiện connection selector, Execute, Cancel, Open, Save và Save As. Có empty/loading/error/canceled/unsupported states và elapsed time.

Mỗi result group có metadata, snapshot và command tag riêng; dùng result selector để xem một group tại một thời điểm. SELECT có 0 rows vẫn hiện cột và empty state. Command không trả rows hiện affected row count khi server có cung cấp, không gán mặc định 0 cho command không có count. Rows của `RETURNING` hiển thị read-only và không được Execute lại để đổi trang.

Tái sử dụng grid hiện có qua read-only Source; row/column identity dựa vào ordinal, không dựa vào tên cột vì SQL có thể trả tên trùng. Không bật sorting/filtering server-side hoặc Save/Revert cho SQL results. NULL, empty string và binary được phân biệt. Numeric không qua float64 làm mất độ chính xác; giữ text representation của server theo type khi phù hợp. Mỗi lần Execute mới thay snapshot cũ để không giữ nhiều execution histories trong tab.

## 7. SQL documents và local files

### 7.1. Document model

Document gồm text hiện tại, revision, saved text baseline/revision, associated path, canonical identity và disk fingerprint. Dirty được xác định từ nội dung so với bản lưu, không chỉ từ việc editor từng thay đổi. Undo về baseline làm hết dirty; đổi connection không làm text dirty.

New tạo Query N chưa có path; tab trống chưa sửa không cần prompt khi đóng. Open thành công tạo document clean, giữ nội dung file; không thay buffer tab hiện tại. Cùng một file đã mở thì focus tab đó. Tab title là filename khi có path, tooltip có full path; dirty marker dùng cơ chế chung của tab strip.

### 7.2. Open/Save/Save As

- Dùng native dialogs, file filter SQL; mặc định dưới `~/.dbl/queries/`, connection folders độc lập với filesystem folders. Người dùng được chọn absolute path ngoài default root.
- First Save mở dialog hỏi tên/vị trí. Save sau ghi associated path. Save As chọn path mới, chỉ cập nhật association sau khi file mới được ghi thành công.
- File IO và dialogs chạy theo cách không chặn UI event loop; trả kết quả về UI qua poster. Chỉ một file operation cho mỗi tab tại một thời điểm.
- Save chụp text/revision trước khi ghi. Chỉ bản đã ghi thành công trở thành saved baseline. Nếu text đổi trong lúc ghi, document vẫn dirty; pending close/quit/connection action không được tiếp tục như đã lưu hết.
- Dialog bị hủy, path lỗi, file không có quyền hoặc replacement lỗi giữ buffer và association cũ. Không đánh dấu clean hoặc chạy pending action.
- Save As trỏ tới file đang thuộc tab khác bị từ chối; cho người dùng biết tab nào đang giữ file. Không ghi đè buffer của tab đó.

### 7.3. Path, encoding và replacement

Support UTF-8, có thể có UTF-8 BOM; bảo toàn BOM và line endings khi save nội dung không sửa. Reject invalid UTF-8, binary/NUL và file trên 8 MiB bằng bounded read; không đọc hết file rồi mới kiểm tra kích thước. Save/Execute input trên giới hạn báo validation error và giữ buffer.

Path phải là file hợp lệ, có filename, không phải directory/device/special stream. Tên người dùng nhập để ghép với default root phải là basename, không nhận traversal; path tuyệt đối do native dialog chọn được validate như đích độc lập. Canonical identity xét alias/symlink và quy tắc nền tảng để không mở/ghi cùng file từ hai tab khác nhau. Mục tiêu symlink được resolve và validate trước khi replacement; không âm thầm thay link thành file khác hoặc chạm một đích chưa xác nhận.

Trước overwrite, so fingerprint nội dung/file identity với lần Open/Save gần nhất; file bị thay đổi hoặc bị xóa bên ngoài yêu cầu xác nhận ghi đè hoặc Cancel. Sau xác nhận, kiểm tra lại revision trước khi ghi. Đây là phát hiện xung đột best effort, không hứa khóa được external editors hoặc loại mọi TOCTOU race.

Ghi temp cùng directory, flush/close rồi replace target theo semantics platform. Không delete file đích trước khi replace, không fallback thành truncate-write khi replace lỗi. Đặc biệt không mặc định helper `os.Rename` hiện tại bảo đảm atomic replacement trên Windows; implementation plan phải chọn helper/platform implementation có semantics rõ, giữ file cũ khi thất bại trước replacement và phân biệt lỗi sau khi replacement đã xảy ra. Cleanup temp bằng đường dẫn chính xác.

## 8. Guards và lifecycle chung

Các action được guard: đóng SQL tab, thoát app, đổi connection của SQL tab, disconnect/delete/edit profile đang được dùng. Giữ cách đóng các tab của connection hiện có; không thêm cơ chế tự detach SQL document trong phase này.

Thứ tự xử lý:

1. Nếu đang file save, chờ completion hoặc hủy pending action; không đóng tab trước khi biết bản nào đã được ghi.
2. Nếu query đang chạy, cho Cancel action hoặc Stop query and continue. Sau khi stop, đợi terminal status/cleanup có thời hạn rồi đánh giá lại session và write outcome; không gọi stop query là rollback thành công.
3. Nếu document dirty, hiện Save / Discard / Cancel. Save lỗi, bị hủy, hoặc revision mới vẫn dirty dừng action. Discard cho phép tiếp tục mà không lưu: action đóng tab/app sẽ bỏ text; action chỉ đổi connection giữ nguyên text và dirty state. Nhãn/thông báo của dialog phải nói rõ hậu quả theo action. Discard không mang nghĩa commit/rollback database.
4. Nếu session còn transaction, hỏi riêng Cancel / Roll back and continue. Không có implicit Commit choice. Quyết định Save SQL file không thay thế xác nhận này.
5. Khi mọi điều kiện được thỏa, thu hồi session và thực hiện action. Đổi connection giữ nguyên nội dung/path, xóa results gắn session cũ, session mới vẫn lazy-open.

Quit hoặc thao tác connection có nhiều tabs duyệt tuần tự, dừng ở tab đầu tiên không hoàn tất. Save table drafts tiếp tục dùng transaction SaveChanges của Phase 2.3; Save SQL tab chỉ ghi filesystem. Không gửi SQL document vào table Save hoặc làm mất bảo vệ table drafts đang có.

Pending actions giữ identity/revision phù hợp, được revalidate trước tiếp tục. Cancel ở bước sau không hoàn tác các Save file đã thành công ở bước trước; những tab chưa đóng vẫn giữ nguyên text hiện tại. Khi app thực sự shutdown, không dựa vào UI callback để đóng session và có deadline tổng cho cleanup.

## 9. Shortcuts và context

| Shortcut | Context và action |
| --- | --- |
| Ctrl/Cmd+N | New SQL tab, lấy connection được chọn trong Explorer như hiện tại |
| Ctrl/Cmd+S | SQL tab: Save document; Table Inspector: giữ Save table hiện có |
| Ctrl/Cmd+Enter | SQL tab: Execute selection/current statement nếu không busy/modal |
| F2 | Table grid: edit cell theo capability hiện có |
| Escape | Cell editor: revert cell editor; dialog: Cancel; không tự gán thành rollback transaction |
| F5 | Table Inspector: refresh theo guards hiện có; SQL tab không tự Execute lại |
| Ctrl+Shift+F | Không gán |

Open/Save As có nút/action rõ; không cần thêm shortcuts ngoài spec. Native dialogs gắn parent window. Modal đang mở không được để shortcut nền gây Execute hoặc tạo pending action khác. Font monospaced, SQL highlighting, line numbers và cuộn hai chiều tiếp tục dùng editor MyGo đã có.

## 10. Phạm vi file cho implementation plan tiếp theo

Đây là ownership map để review thiết kế, chưa phải implementation plan được phép thực thi.

| File/package | Trách nhiệm dự kiến |
| --- | --- |
| `internal/database/adapter.go`, file SQL execution models mới | Capability/factory và session/result contracts |
| `internal/database/postgres/connect.go`, file session/execution/parser/error mới | Dedicated pgx connection, registry, reader có budget, PostgreSQL boundary parser và errors |
| `internal/query/executor.go`, `results.go` mới | Execution lifecycle không có UI, bounded snapshots và read-only result Source |
| `internal/connection/manager.go` khi cần | Epoch/lifecycle integration và cleanup sessions, không đổi credential policy |
| `internal/app/workspace.go`, `state.go`, `save.go`, `connections.go` và file SQL/document mới | Document state, operation gate, save routing và pending actions |
| `internal/storage/files.go` và replacement helper theo platform | Bounded file IO, canonical paths, conflict fingerprint và safe replacement |
| `internal/ui/sql_editor.go`, `shell.go`, `tabs.go`, `dialogs.go`, `explorer.go` khi cần | Editor binding, toolbar/results, context shortcuts và guards |
| `internal/ui/grid.go` nếu cần | Reuse grid với duplicate SQL column labels qua ordinal IDs, giữ table behavior |
| `main.go` | Wire file service/dialog parent, ownership và shutdown |
| Tests cạnh các source tương ứng | Parser, session/results, file IO, async guards, UI fake và PostgreSQL integration |
| `docs/implementation-phases.md`, `README.md` | Chỉ cập nhật trạng thái/usage sau implementation với bằng chứng thực tế |

Implementation plan phải chốt đường dẫn tạo/sửa thực tế, contract Go, dependency order, tasks và commands người dùng tự chạy; không mở rộng sang refactor không liên quan. Không thay `go.mod`/`go.sum` theo thiết kế hiện tại.

## 11. Acceptance và validation dự kiến

Các mục dưới đây là yêu cầu kiểm tra, chưa phải kết quả đã đạt.

- Parser: selection precedence, multi-selection refusal, cursor tại boundary/end-of-file, Unicode, comments lồng nhau, quoted identifiers, escaped strings, dollar tags, procedural bodies và unsupported construct fallback.
- Executor: nhiều result groups, empty rows có columns, duplicate names, NULL/empty/binary, numeric chính xác, command tags/affected count, RETURNING và page navigation không phát thêm SQL.
- Limits: đúng ngưỡng snapshot, dropped rows, total bytes/rows/results, metadata/notices, protocol message lớn, timeout và cleanup không drain vô hạn.
- Session: BEGIN/write/COMMIT qua các Execute, BEGIN/error/ROLLBACK, session settings/temp objects độc lập, metadata pool vẫn hoạt động, capacity limit, reconnect không phục hồi session state.
- Outcome: lỗi sau command đã hoàn tất, mất mạng quanh COMMIT, cancel đua completion, explicit transaction bị abort và partial results không được gán thành committed.
- Async: cancel, close tab, đổi connection, disconnect/reconnect, callback cũ và shutdown trước UI delivery không leak session hoặc ghi đè state mới.
- Files: New/Open/Save/Save As, first Save, Unicode path, UTF-8/BOM/CRLF, bounded read, permission/path errors, overwrite, external conflict, alias identity, temp cleanup và Windows existing-target replacement.
- Guards: Save/Discard/Cancel trên dirty SQL và mixed table/SQL tabs; Save đang chạy; edit-during-save; native dialog Cancel; transaction prompt độc lập; failure dừng close/quit/connection action.
- UI: monospaced/highlighting/line numbers, scroll hai chiều, rõ connection/database, result selector/grid read-only, mọi trạng thái và shortcuts theo context; regressions của Phase 2.3.
- Manual end-to-end trên PostgreSQL disposable: connection → Explorer → đọc/sửa/Save/Revert bảng → SQL transaction/results → local files → close/disconnect/quit.

Commands dự kiến cho người dùng sau khi implementation và tests tồn tại:

```powershell
go test ./...
go build ./...
go vet ./...
go run .
```

PostgreSQL integration cần disposable database và `DBL_TEST_PG_HOST`, `DBL_TEST_PG_USER`, `DBL_TEST_PG_DB`; dùng các tùy chọn khác theo integration fixtures hiện có. Không chạy against production. Agent không tự chạy các commands này theo chỉ dẫn trực tiếp hiện tại; báo riêng tests không chạy/skipped và manual app testing của người dùng.

## 12. Handoff và approval

Spec này tổng hợp lựa chọn đã duyệt và bổ sung các ràng buộc cần thiết để triển khai có giới hạn; các chi tiết bổ sung về session capacity, cleanup deadline, result/column count, metadata/notices và protocol-message limit ở trên cần được review cùng bản spec.

Sau khi người dùng duyệt **file spec này**, bước tiếp theo là `superpowers:writing-plans` để tạo implementation plan theo file thực tế. Sau đó chờ người dùng duyệt plan và chọn cách thực thi trước khi chỉnh sửa code. Không tự commit spec hoặc code; chỉ dẫn trực tiếp của người dùng về commit và commands ưu tiên hơn workflow mặc định của skill. Hoàn tất phase vẫn chờ người dùng chạy app/kiểm tra và cho phép commit rõ ràng.

# DBL — Implementation Phases

- Nguồn yêu cầu: [Software Requirements & Technical Specification v1.0.0](specs.md).
- Ngày: 2026-10-16.
- Trạng thái: cách chia phase đã được duyệt; tài liệu chi tiết chờ review.
- Mục tiêu: chuyển roadmap tại mục 13 của spec thành các phần có thể triển khai và nghiệm thu riêng, theo dependency và luồng sử dụng.

## 1. Cách sử dụng tài liệu

Đây là lộ trình triển khai, chưa phải kế hoạch chỉnh sửa từng file để bắt đầu code. Trước mỗi phase, đọc lại source hiện có, lập plan theo file thực tế và chờ người dùng approve. Tên file/package bên dưới là phạm vi dự kiến dựa trên mục 5.2 của spec; chỉ tạo khi phase cần đến.

Spec là nguồn quyết định phạm vi sản phẩm. Tài liệu này giữ năm milestone ban đầu, bổ sung Phase 0 để chốt nền tảng và tách milestone PostgreSQL/relational thành các phase nhỏ hơn. Yêu cầu ngoài mục 1.3 của spec không được đưa thêm vào implementation.

Các checklist kiểm tra là điều kiện nghiệm thu dự kiến, không phải kết quả đã chạy. Agent không tự chạy build, test, lint hoặc lệnh verify; khi có code và cách chạy cụ thể, cung cấp command cùng điều kiện môi trường để người dùng tự chạy. Không commit nếu chưa có yêu cầu rõ ràng. Quy định trực tiếp của người dùng này áp dụng khi khác với hướng dẫn chạy kiểm tra trong mục 15 của spec.

## 2. Tổng quan và dependency

| Phase | Kết quả chính | Dependency bắt buộc | Mapping roadmap gốc |
| --- | --- | --- | --- |
| 0 | Chốt phiên bản, API và khả năng nền tảng | Không | Chuẩn bị cho Phase 1 |
| 1 | Native UI shell và tương tác với dữ liệu giả lập | 0 | Phase 1 |
| 2.1 | Lưu profile/folder và kết nối PostgreSQL | 1 | Phase 2 |
| 2.2 | Explorer, Properties và grid đọc PostgreSQL | 2.1 | Phase 2 |
| 2.3 | Chỉnh sửa và Save/Revert an toàn | 2.2 | Phase 2 |
| 2.4 | SQL editor, execution và local SQL files | 2.2 | Phase 2 |
| 3.1 | SQLite | 2.3 và 2.4 | Phase 3 |
| 3.2 | MySQL/MariaDB | 2.3 và 2.4 | Phase 3 |
| 3.3 | SQL Server | 2.3 và 2.4 | Phase 3 |
| 4 | Redis theo capability riêng | 2.1 và workspace của 1 | Phase 4 |
| 5 | Stabilization, đo hiệu năng và packaging | Toàn bộ phase chức năng | Phase 5 |

Thứ tự triển khai mặc định: **0 → 1 → 2.1 → 2.2 → 2.3 → 2.4 → 3.1 → 3.2 → 3.3 → 4 → 5**. Bảng thể hiện dependency kỹ thuật, không phải yêu cầu triển khai song song. Thứ tự SQLite, MySQL và SQL Server có thể đổi theo ưu tiên người dùng mà không thay đổi hợp đồng chức năng.

Milestone PostgreSQL MVP hoàn thành khi cả 2.1–2.4 đạt tiêu chí nghiệm thu. SQLite/MySQL/SQL Server hoàn thành milestone relational mở rộng. Bản release đầy đủ cần Redis và Phase 5 cùng Definition of Done tại mục 14 của spec.

## 3. Quy tắc áp dụng xuyên suốt

- Dùng Go và MyGo Native UI, modular monolith trong một desktop process. Không thêm WebView, web server hoặc UI stack khác khi chưa có quyết định kiến trúc được duyệt.
- UI gửi action đến application service; application service quản lý tác vụ nền, cancellation và state; adapter sở hữu SQL/dialect và không import UI.
- Mọi thao tác dài nhận `context.Context`. Kết quả đã hủy hoặc thuộc request cũ không được ghi đè state mới; cập nhật UI qua cơ chế an toàn của MyGo.
- Result sets, page caches và dữ liệu Redis phải có giới hạn. Giải phóng rows, cache, tác vụ và connection resources đúng lifecycle.
- Generated SQL dùng bound parameters và quoting theo engine. Không ghép giá trị người dùng vào SQL; không tự retry writes sau lỗi mạng hoặc kết quả không xác định.
- Loaded values, display values và draft edits có vai trò riêng. Không dùng chuỗi đã format để xác định row identity hoặc so sánh dirty state.
- Dùng lỗi có category theo mục 9 của spec; giữ context hữu ích nhưng không lộ password hoặc connection string chứa secret.
- Mỗi phase bổ sung loading/empty/error/canceled/unsupported states tương ứng. Capability thiếu phải được biểu diễn rõ, không chuyển thành giá trị mặc định gây hiểu nhầm.
- Correctness, bảo vệ dữ liệu và cancellation được triển khai trong từng phase chức năng. Phase 5 tổng hợp kiểm tra và xử lý vấn đề còn lại.

## 4. Phase 0 — Chốt nền tảng kỹ thuật

**Mục tiêu:** có quyết định kỹ thuật đủ cụ thể để khởi tạo đúng stack, tránh xây UI trên API hoặc khả năng chưa xác định.

**Mapping:** mục 2, 5, 8 và 15.17 của spec.

**Phạm vi:**

1. Xác định repository/module chính thức của MyGo và phiên bản sẽ pin; đối chiếu yêu cầu Go `1.27+` trong spec với toolchain và framework. Ghi rõ thay đổi cần người dùng duyệt nếu không thể giữ yêu cầu hiện tại.
2. Xác định API cho main window, splitter/sidebar, tabs, theme, clipboard, file dialogs, virtualized grid, cell editing và SQL syntax highlighting.
3. Chốt cách chạy database work ngoài UI thread và trả kết quả về UI theo API của phiên bản MyGo đã chọn.
4. Chốt hỗ trợ packaging Windows 10/11 x64, điều kiện macOS/Linux, CGO và các runtime/resource cần đi kèm. Single executable chỉ là mục tiêu khi nền tảng cho phép.
5. Ghi lại nguồn API, phiên bản, giới hạn và quyết định. Nếu cần probe thực thi, trình bày phạm vi và command để xin phép riêng trước khi chạy.

**File/package dự kiến:** bổ sung kết quả quyết định vào phần Phase 0 của tài liệu này; `go.mod`, `go.sum`, `mygo.json` thuộc Phase 1, sau khi chốt nền tảng.

**Tiêu chí hoàn thành:** mọi khả năng bắt buộc ở trên có API hoặc phương án triển khai cụ thể và giới hạn rõ ràng. Không chuyển sang Phase 1 nếu còn bất tương thích ảnh hưởng stack hoặc phạm vi mà chưa được duyệt. Tài liệu hiện tại chưa xác nhận phiên bản hay tính tương thích của MyGo/Go.

**Checklist kiểm tra cho người dùng:**

- [ ] Đối chiếu phiên bản framework/toolchain và tài liệu API được ghi nhận.
- [ ] Xem xét phương án virtualization, editor, cập nhật UI và packaging.
- [ ] Duyệt mọi deviation bắt buộc trước khi khởi tạo project.

**Kết quả thực hiện (2026-10-16):** đã nghiên cứu read-only và ghi vào năm subsection [P0-A](#p0-a-framework-và-toolchain)…[P0-E](#p0-e-kết-luận-và-gate-sang-phase-1) bên dưới; đề xuất `Ready for review`, chờ người dùng nghiệm thu và quyết định các mục ở P0-E. Chưa chạy build/test/probe nào; mọi runtime evidence đều là `Not run`. Checklist ở trên giữ nguyên trạng thái chưa tick vì thuộc về người dùng.

<!-- P0-A -->
### P0-A. Framework và toolchain

**Baseline từ spec.** Go `1.27+`; UI là MyGo Native UI; modular monolith; primary target Windows 10/11 x64, secondary macOS/Linux; ưu tiên build không CGO; pin dependency bằng `go.mod`/`go.sum` (tạo ở Phase 1). Kiểm kê repository ngày 2026-10-16: root chỉ có `AGENTS.md`, `README.md`, `docs/` và các thư mục công cụ (`.agents`, `.claude`, `.sb-kit`, `.memorize`, `skills-lock.json`); chưa có `go.mod`, `mygo.json` hay mã ứng dụng.

**Framework identity** (nguồn: E01–E04):

| Trường | Giá trị |
| --- | --- |
| officialRepository | <https://github.com/egoist/mygo> (license MIT, tạo 2026-09-26, chưa archive, owner `egoist`) |
| modulePath | `github.com/egoist/mygo` |
| selectedTagOrCommit | tag `v0.5.5` → commit `4d8673cd2658a2c6e86b26c44751e57bf8174f91` |
| moduleVersion | `v0.5.5` |
| sourceDate | tag công bố 2026-10-16 (Go module proxy `00:18:10Z`; GitHub release `00:26:32Z`); source đọc ngày 2026-10-16 |

Mọi kết luận ở `P0-B`…`P0-D` dựa trên đúng commit trên. Đổi version thì phải đọc lại các kết luận đó.

**Lý do chọn và rủi ro.**

- Đây là bản stable (không phải pre-release) mới nhất tại thời điểm đọc, có trên Go module proxy (E01) và GitHub release (E02). Không chọn `latest` mơ hồ: tag và commit được ghi cố định.
- **Rủi ro churn (cần người dùng quyết định, xem P0-E):** framework ở `v0.x`, repository mới 3 tuần tuổi, đã có 62+ tag từ 2026-09-27 và có công cụ migrate API (`go tool mygo migrate-ui`, `docs/ui/migration.md`, E09). API UI vẫn có thể thay đổi giữa các tag.
- **Không nhất quán nguồn:** pkg.go.dev vẫn liệt kê `v0.5.4` (2026-10-15) là mới nhất trong lúc proxy và GitHub đã có `v0.5.5`; đồng thời trang pkg.go.dev không liệt kê `v0.4.4` dù proxy có tag này. Chỉ dùng proxy và GitHub làm nguồn xác định version.
- **Giả định cần người dùng xác nhận:** spec chỉ ghi "MyGo", không nêu repository. Hai lần tìm kiếm web chỉ trả về `egoist/mygo`, và dự án này có đúng native UI vẽ bằng GPU không dùng webview (E06, E07). Chưa tìm thấy ứng viên MyGo khác.

**Evidence registry** (cùng định dạng cho các task sau; `M` = `https://github.com/egoist/mygo/blob/4d8673cd2658a2c6e86b26c44751e57bf8174f91/`; tất cả đọc ngày 2026-10-16):

| ID | Nguồn | Claim được hỗ trợ |
| --- | --- | --- |
| E01 | `https://proxy.golang.org/github.com/egoist/mygo/@v/v0.5.5.info` | `v0.5.5` tồn tại, commit `4d8673cd…`, thời gian `2026-10-16T00:18:10Z` |
| E02 | `https://proxy.golang.org/github.com/egoist/mygo/@v/list`; `gh api repos/egoist/mygo/{tags,releases,}`; `https://pkg.go.dev/github.com/egoist/mygo?tab=versions` | Danh sách `v0.1.0`…`v0.5.5` (có `v0.4.4`); release `v0.5.5` không phải pre-release; repo metadata; pkg.go.dev lệch (latest `v0.5.4`) |
| E03 | `M` `LICENSE`, `README.md:5-32` | MIT; hai chế độ giao diện; "Pure Go, no cgo"; có Windows installer |
| E04 | `M` `go.mod:3-12` | `go 1.27.1`; dependency: purego, go-text/typesetting, go-mp3, oggvorbis, x/image, x/net, x/text |
| E05 | `https://go.dev/doc/devel/release` | go1.27.0 (2026-08-19), go1.27.1 (2026-09-01), go1.27.2 (2026-10-08, bản sửa lỗi bảo mật) |
| E06 | `M` `README.md:10-20` | Native UI viết bằng Go với package `ui`, vẽ bằng GPU, không HTML/JS/webview; webview chỉ cho chế độ web page |
| E07 | `M` `docs/ui/README.md:1-8` | Native UI vẽ bằng Metal (macOS), Direct3D 11 (Windows), OpenGL (Linux), không khởi tạo webview |
| E08 | `M` `docs/ui/rendering.md`; `docs/ui/README.md` (mục Browsers) | Đường vẽ GPU/CPU của native UI (mục được dùng ở P0-D) |
| E09 | `M` `docs/ui/migration.md:1-27` | Có source migration (`mygo migrate-ui`) cho thay đổi API UI |
| E31 | `https://proxy.golang.org/<module>/@v/<version>.mod` của từng driver (xem bảng driver bên dưới); README `ncruces/go-sqlite3` tại tag `v0.35.6` | `go` directive của driver; `ncruces/go-sqlite3` là wrapper SQLite không cgo, có Windows amd64 trong danh sách platform đã test |

| E39 | `https://go.dev/doc/toolchain` (đọc 2026-10-16) | Dòng `go` là mức tối thiểu; `GOTOOLCHAIN=auto` (mặc định) tự tải toolchain mới hơn khi cần, `local` thì báo lỗi |

> Registry là một danh sách duy nhất: `E01`–`E09`, `E31` và `E39` ở bảng này; `E10`–`E26` ở `P0-B`; `E27`–`E30` ở `P0-C`; `E32`–`E38` ở `P0-D`.

**Compatibility table:**

| specRequirement | upstreamRequirement | availableRelease | localEvidence | conclusion |
| --- | --- | --- | --- | --- |
| Go `1.27+` | `go 1.27.1` (E04) | go1.27.0, 1.27.1, 1.27.2 (E05) | `User reported` (2026-10-16, PowerShell, root repo): `go version go1.26.5 windows/amd64`; `GOTOOLCHAIN=auto`, `CGO_ENABLED=0`, `GOPROXY=https://proxy.golang.org,direct`, `GOSUMDB=sum.golang.org`, `GOMOD=NUL` (chưa có module). **Go cục bộ thấp hơn `1.27`** | **Tương thích với spec**: `1.27.1` thỏa `1.27+`. Dòng `go` trong `go.mod` là mức tối thiểu (E39): toolchain cục bộ cũ hơn (ví dụ `1.27.0`) sẽ **tự tải** toolchain mới khi `GOTOOLCHAIN=auto` (mặc định; có tải mạng) hoặc báo lỗi khi `GOTOOLCHAIN=local`. Không đổi `1.27+` trong spec |
| Native UI, không webview | `ui.View` + `ui` package vẽ GPU (E06, E07) | `v0.5.5` | `Not run` | Supported by source |
| Windows 10/11 x64 | Direct3D 11 backend (E07); DLL runtime xem `P0-D` | `v0.5.5` | `Not run` | Supported by source (mức source), chưa có runtime evidence |
| macOS, Linux (secondary) | Metal / OpenGL (E07) | `v0.5.5` | `Not run` | Supported by source ở mức tài liệu; chưa xác nhận hỗ trợ cho DBL |
| Ưu tiên CGO-free | "Pure Go, no cgo" (E03) | `v0.5.5` | `CGO_ENABLED` của máy: `Not run` | Framework không yêu cầu CGO theo tài liệu; driver SQLite xem bảng dưới |
| Pin dependency | module path + tag cố định | `v0.5.5` | — | Có thể pin `github.com/egoist/mygo v0.5.5` ở Phase 1; chưa tạo `go.mod` |

**Driver trong spec (chỉ tra `go.mod` trên proxy, không tải/cài; E31):** `github.com/jackc/pgx/v5` `v5.11.0` (`go 1.25.0`); `github.com/ncruces/go-sqlite3` `v0.35.6` (`go 1.26.0`, cgo-free theo README, hỗ trợ Windows amd64); `github.com/go-sql-driver/mysql` `v1.10.1` (`go 1.24.0`); `github.com/microsoft/go-mssqldb` `v1.11.2` (`go 1.25.0`); `github.com/redis/go-redis/v9` `v9.23.0` (`go 1.26.0`). Tất cả có `go` directive thấp hơn `1.27.1` nên không làm tăng Go floor; module cuối cùng sẽ yêu cầu `go 1.27.1`. Đây là các version "mới nhất lúc tra cứu", chỉ để đánh giá tương thích; version cần pin do từng phase driver quyết định. `go-mssqldb` trong spec được hiểu là `github.com/microsoft/go-mssqldb` (đây là giả định của tài liệu này).

**Kết luận P0-A:** chọn `github.com/egoist/mygo v0.5.5` @ `4d8673cd`; không có bất tương thích về Go floor. Rủi ro còn lại: churn API `v0.x` và cần người dùng xác nhận upstream.

<!-- P0-B -->
### P0-B. Native UI capability matrix

`ui` = `github.com/egoist/mygo/ui`; `editor` = `github.com/egoist/mygo/ui/editor`; `mygo` = `github.com/egoist/mygo`. Số dòng thuộc commit `4d8673cd` (`M`). Trạng thái: `S` = Supported by source, `P` = Requires probe, `U` = Unsupported. Chưa có dòng nào chạy runtime.

Evidence bổ sung:

| ID | Nguồn | Claim |
| --- | --- | --- |
| E10 | `M` `window.go:359` (`NewWindow(opts WindowOptions) *Window`), `window.go:61-166` (`WindowOptions`: `Title`, `Width`, `Height`, `MinWidth`, `MinHeight`, `StateKey`, `Content`) | Tạo cửa sổ native UI với `Content: ui.View(fn)` |
| E11 | `M` `window.go:629-641` (`close`), `window.go:1475` (`OnClose`), `window.go:1588` (`ShouldClose`), `events.go:9-24` (`Preventable`, `CloseEvent`), `app.go:430,436` (`OnBeforeQuit`, `OnWillQuit`) | Hook đóng cửa sổ/quit có thể hủy bằng `PreventDefault()` |
| E12 | `M` `ui/api_gen.go:1934,1950,1962,1974` (`Split`, `SplitPercent`, `SplitVertical`, `SplitVerticalPercent`); `docs/ui/split.md` | Splitter có `*float32` giữ kích thước; divider cách mép ≥ 40 DIP; hỗ trợ phím mũi tên |
| E13 | `M` `ui/api_gen.go:2097,2126,5865` (`Tabs`, `TabsBase`, `TabsParts.Tab(i)`), `6030`; `docs/ui/tabs.md` | Tabs chọn theo chỉ số `*int`; `TabsBase` cho tab tự dựng (không có sẵn nút đóng/nút `+`) |
| E14 | `M` `ui/table.go:10-60,145-280`; `ui/api_gen.go:2074` (`Table`); `docs/ui/table.md` | Table: header, cột có `Width/MinWidth/MaxWidth`, resize, đổi thứ tự, sort, cuộn ngang |
| E15 | `M` `ui/list.go:38-126` (`ListState`), `list.go:374` (`buildList`); `docs/ui/list.md` | Chỉ dựng các hàng trong vùng nhìn; chọn một/nhiều hàng; `Key`, `Sort`, `SortBy`, `Columns` |
| E16 | `M` `ui/editable.go:20-131`, `ui/list.go:966`; `docs/ui/editable-text.md` | Hành vi `EditableText` trong ô và trong Table |
| E17 | `M` `ui/element.go:1232-1244` (`MaxLines`, `SingleLine`, `Ellipsis`) | Cắt chữ khi hiển thị, không đổi giá trị |
| E18 | `M` `ui/api_gen.go:401,455,468,488`; `modules.go:306-308,343-358,361,370,473,505`; `app.go:595`; `ui/context.go:308-316`; `ui/theme.go:7-59`; `docs/ui/styling.md` (mục Themes, The desktop's preferences) | Theme Light/Dark/System, thông báo thay đổi, theme theo frame |
| E19 | `M` `dialog.go:11-60,118,151,181`; `modules.go:142-155`; `clipboard.go:118-176` | File dialog và clipboard, gọi được từ goroutine bất kỳ |
| E20 | `M` `ui/editor/state.go:34,140-146`; `ui/editor/languages.go:290`; `ui/editor/buffer.go:122`; `docs/ui/code-editor.md` | Editor: `NewBuffer`, `State`, `View`, ngôn ngữ SQL có sẵn, `LineNumbers`, selection, `Modified/MarkSaved`, `Bindings` |
| E21 | `M` `ui/editor/keymap.go:250-251,309-310` | Keymap mặc định: Enter = newline; `Cmd+Enter`/`Ctrl+Enter` = `NewlineBelow`; `Ctrl+F`/`Cmd+F` = Find; không gán `Ctrl+Shift+F` |
| E22 | `M` `ui/api_gen.go:468` (`Context.Shortcut`), `5243` (`Element.Shortcut`), `ui/actions.go:51` (`OnShortcut`), `ui/keys.go:16-35,112` (`Cmd` = Command trên macOS, Control nơi khác); `docs/ui/input.md` (mục Shortcuts) | Shortcut toàn cửa sổ hoặc theo focus subtree |
| E23 | `M` `ui/api_gen.go:641` (`AlertDialog`); `docs/ui/alert-dialog.md` | Hộp thoại xác nhận nhiều nút, Escape = `ui.AlertDismissed` |
| E24 | `M` `window.go:159-207`, `window_state.go:1-80` | `StateKey` + `WindowStateStore` lưu vị trí/kích thước cửa sổ, mặc định `window-state.json` trong `PathUserData` |
| E25 | `M` `ui/api_gen.go:1407` (`Outline`); `docs/ui/outline.md` | Cây ảo hóa theo hàng; `children` do app cung cấp |
| E26 | `M` `docs/ui/performance.md:1-20` | Số đo của upstream (Apple M5, macOS arm64, Go 1.27.1, CPU render); không phải benchmark Windows/DBL |

| # | requirement | upstreamSymbolAndSignature | sourceID | implementationApproach | limitations | status | owningPhase |
| --- | --- | --- | --- | --- | --- | --- | --- |
| B01 | Main window | `mygo.NewWindow(opts WindowOptions) *Window`; `mygo.App.WhenReady(fn)`, `mygo.App.Run() error`; `WindowOptions{Title, Width, Height, MinWidth, MinHeight, StateKey, Content}` | E10 | Một cửa sổ với `Content: ui.View(app.view)` | — | S | 1 |
| B02 | Close hook / chặn đóng | `(*Window).OnClose(fn func(e *CloseEvent)) (off func())`; `(*CloseEvent).PreventDefault()`; `(*Window).Close()`; `(*Window).Destroy()`; `(*Application).OnBeforeQuit(fn func(*QuitEvent))` | E11 | Hook chạy đồng bộ trên UI thread: khi còn edit/SQL chưa lưu thì `PreventDefault()`, mở `ui.AlertDialog`, sau khi người dùng chọn mới gọi lại `Close()`/`Destroy()` (`Destroy` không phát `OnClose`) | Không có API chờ dialog bất đồng bộ trong hook; luồng "prevent → hỏi → đóng lại" là thiết kế của DBL | S | 1 (khung), 2.3/2.4 (nội dung bảo vệ) |
| B03 | Sidebar resize | `ui.Split(c *Context, size *float32, first, second func()) Element`; `ui.SplitPercent(...)` | E12 | `*float32` là state của app, lưu vào `settings.json` | Divider luôn cách mép ≥ 40 DIP; pane thứ nhất co lại khi cửa sổ hẹp nhưng `*float32` giữ giá trị người dùng chọn | S | 1 |
| B04 | Workspace tabs, tab theo table identity, focus tab đang mở | `ui.TabsBase(c *Context, selected *int, n int) TabsParts`; `TabsParts.List`; `TabsParts.Tab(i int) Element`; `ui.Tabs(c, selected *int, labels ...string)` | E13 | App giữ slice tab (mỗi tab có ID, state riêng) và tìm tab theo `(connectionID, tableIdentity)` rồi gán `*selected`; nút đóng `×` và `+` tự dựng bằng `Button` trong `Tab(i).Children` | Không có sẵn tab đóng được/kéo thả; chỉ số thay đổi khi đóng tab nên app phải ánh xạ ID↔chỉ số | S (ghép từ primitives) | 1 |
| B05 | Grid ảo hóa theo hàng | `ui.Table(c, s *ListState, columns []TableColumn, n int, cell func(row, col int)) Element` | E14, E15, E26 | `n` = số hàng của page (mặc định 100); chỉ hàng trong vùng nhìn gọi `cell` | Hàng cao theo ô cao nhất (chữ wrap làm hàng cao hơn): dùng `SingleLine()` để giữ chiều cao ổn định. Con số upstream (14.5 µs/frame cho list 1 triệu hàng) đo trên macOS/CPU, **không** chứng minh 60 FPS của DBL | S | 1 |
| B06 | Grid ảo hóa theo ô/cột ("render only visible cells", spec 7.1) | cùng `ui.Table`; `ui/table.go:262` | E14 | Source gọi `cell(i, j)` cho **mọi** cột của mỗi hàng đang hiển thị (không cắt cột theo cuộn ngang) | Chi phí ∝ (số hàng nhìn thấy × số cột). Với bảng rất rộng (hàng trăm cột) chưa có bằng chứng về hiệu năng; `cell` phải rẻ và tái dùng chuỗi đã format | **P** | 1 (probe + mock nhiều cột), 2.2 |
| B07 | Cuộn hai chiều, header đi theo cuộn ngang | `ui/table.go:237` (`list.flags \|= flagScrollX`), `head.followX = list` | E14 | Dùng `ui.Table` | Cuộn ngang theo tổng độ rộng cột; cột `Width: 0` chia phần còn lại | S | 1 |
| B08 | Resize cột | `ui.TableColumn{Title, ID, Width, MinWidth, MaxWidth, Align, Sortable, Fixed}`; `ListState.Columns TableLayout{Order []string; Widths map[string]float32}` | E14, E15 | Kéo cạnh phải header (double-click = fit); lưu `Columns` nếu muốn nhớ độ rộng | `ID` mặc định là `Title` nên phải đặt `ID` ổn định theo tên cột DB | S | 1 |
| B09 | Chọn hàng | `ListState.Selected *int`, `ListState.Selection Selector`, `ListState.Key func(row int) any` | E15 | `Key` trả về row identity do app tính (không dùng chuỗi hiển thị) để selection theo item | `Selected` là chỉ số hàng hiển thị; thay page/sort phải đặt lại | S | 1, 2.2 |
| B10 | Chọn ô (cell selection) | Không có state chọn ô trong `ListState`/`Table` (chỉ hàng) | E14, E15 | App giữ `{rowKey, columnID}`; bắt click trong `cell(row, col)` bằng `Element.Clicked()`/`Pressed()` của hộp bao ô; điều hướng phím giữa các ô tự xây | Chưa rõ click của ô có chồng/xung đột với chọn hàng của list; chưa rõ phím Trái/Phải đang do list hay app xử lý | **P** | 1 |
| B11 | Copy giá trị ô | `c.Shortcut(ui.Cmd, ui.KeyC)`; `Element.OnShortcut`; `mygo.Clipboard.WriteText(text string)` | E19, E22 | Khi có ô được chọn: ghi giá trị gốc (không phải chuỗi đã cắt) | `Clipboard.WriteText` bỏ qua lỗi (`_ = c.Write(...)`); dùng `Clipboard.Write(transfer.Data, ...) error` nếu cần lỗi. Cần chắc `Ctrl+C` không bị widget khác nuốt khi focus ở list | S (clipboard) / P (route phím) | 1 |
| B12 | Cell editor: double-click/F2, Enter, Escape, focus | `ui.EditableText(c, value *string) Element`; `ui.TextInput(c, value *string)` với `.AutoFocus()`, `.Submitted()`; `Element.Shortcut(0, ui.KeyEscape)`; `Table(...).Submitted()` | E14, E16 | **Không dùng `EditableText` làm editor ô tổng quát:** trong Table, F2 (và Enter trên macOS) chỉ kích hoạt `EditableText` **đầu tiên** của hàng đang chọn (`list.go:960-967` đặt `editAsked`, bị tiêu thụ ở `editable.go:48-49`), double-click vào hàng **không** mở editor (`editable.go:52-60`, chỉ báo `Submitted`). Khi bảng không dùng `EditableText` nào (`s.editables == false`) thì list không xử lý F2, nên `c.Shortcut(0, ui.KeyF2)` của app không bị tranh phím (`list.go:961`). Phương án: editor ô do app sở hữu (`activeCell`, `draft`), mở bằng `Submitted()`/`c.Shortcut(0, ui.KeyF2)`, dựng `TextInput` trong ô; Enter chỉ ghi `draft` vào changeset cục bộ; Escape bỏ `draft` | `EditableText` commit khi mất focus (chỉ vào `draft`, không ghi DB – phù hợp spec "không ghi khi rời ô"); focus giữa list (giữ focus bàn phím) và `TextInput` trong ô chưa có bằng chứng; trên macOS Enter vừa là `Submitted` vừa là rename | **P** | 1 (mock), 2.3 |
| B13 | Hiển thị cắt chữ, NULL/binary placeholder, dirty highlight | `Element.SingleLine()`, `.MaxLines(n)`, `.Ellipsis(s)`, `.Background(Color)` | E17 | App định dạng chuỗi hiển thị riêng, giữ giá trị typed gốc ở model; cắt bằng `SingleLine`/độ dài tối đa; màu dirty/error từ palette ngữ nghĩa của DBL | Bộ màu ngữ nghĩa "dirty/modified" không có trong `ui.Theme` → DBL tự định nghĩa theo `Theme.Dark` | S | 1 |
| B14 | Theme Light/Dark/System + thông báo đổi theme | `mygo.Theme.SetSource(s ThemeSource)` (`ThemeSystem`/`ThemeLight`/`ThemeDark`), `.Source()`, `.IsDark()`, `.OnUpdated(fn func()) (off func())`; `c.Theme() *ui.Theme`, `c.SetTheme(*ui.Theme)`, `c.Preferences()` | E18 | `SetSource` có thể gọi trước `Run` để áp theme đã lưu; theme mặc định theo hệ thống ở mỗi frame; palette DBL tạo từ bản sao của `c.Theme()` rồi `SetTheme` | `SetTheme` có hiệu lực theo frame (`context.go:308-316`) nên phải gọi trong view mỗi frame. Đổi theme gọi `contentThemeChanged()` để vẽ lại, không có mã tạo lại cửa sổ; việc state tab/connection giữ nguyên là suy luận từ source, cần người dùng xác nhận khi chạy Phase 1 | S (cơ chế) / runtime `Not run` | 1 |
| B15 | File dialogs cục bộ | `mygo.Dialog.Open(opts OpenDialogOptions) ([]string, error)`; `.Save(opts SaveDialogOptions) (string, error)`; `.Message(MessageOptions)`; `FileFilter{Name, Extensions}` | E19 | Gọi từ goroutine (không gọi trong view); `DefaultPath` = `~/.dbl/queries/`; kết quả trả qua `Window.Update` | Phương thức chặn cho đến khi đóng dialog; kiểm tra path traversal là việc của DBL (FR-008) | S | 2.4 |
| B16 | Soạn SQL + highlight | `editor.NewBuffer(text string) *Buffer`; `(*Buffer).SetLanguage(...)`; `editor.LanguageNamed("SQL")`; `editor.State{Buffer, LineNumbers, ...}`; `editor.View(c, st) Element` | E20 | Mỗi tab SQL giữ `*editor.State`; font monospaced mặc định; cuộn hai chiều | Ngôn ngữ SQL tích hợp là tokenizer chung (`languages.go:290`), chưa có bằng chứng về từ khóa riêng của PostgreSQL/MySQL/SQL Server; dùng `Language.Tokenizer` tùy biến nếu cần | S (highlight cơ bản) / P (độ phủ dialect) | 2.4 |
| B17 | Số dòng | `editor.State.LineNumbers LineNumbers` (`LineNumbersOn`, `LineNumbersRelative`, `LineNumbersOff`) | E20 | Giá trị mặc định (zero) là `LineNumbersOn` | — | S | 2.4 |
| B18 | Unsaved indicator, execute selection/current statement | `(*Buffer).Modified() bool`, `.MarkSaved()`, `editor.State.Selections()`, `.CursorPos()`, `(*Buffer).String()` | E20 | Chỉ báo chưa lưu từ `Modified()`; lấy vùng chọn/vị trí con trỏ để chạy statement; parser statement theo dialect do DBL viết | Buffer dùng offset byte UTF-8 | S | 2.4 |
| B19 | Phím tắt Ctrl+N/S/Enter, F2, F5, Esc; Ctrl+Shift+F không gán | `c.Shortcut(mods, key)`, `c.OnShortcut(mods, key, fn)`, `ui.Cmd`, `ui.KeyN/KeyS/KeyEnter/KeyF2/KeyF5/KeyEscape`; `editor.State.Bindings []editor.Binding` với `editor.NoCommand` | E21, E22 | Phím toàn cục qua `c.OnShortcut(ui.Cmd, ...)` (macOS tự thành Command). **Xung đột đã thấy:** keymap mặc định của editor gán `Ctrl+Enter`/`Cmd+Enter` = `NewlineBelow` (E21) nên SQL editor phải thêm `editor.Binding{Mods: ui.Cmd, Key: ui.KeyEnter, Command: editor.NoCommand}` để nhường phím Execute cho app | Thứ tự ưu tiên giữa shortcut toàn cửa sổ và editor đang focus chưa kiểm chứng runtime | S (có biện pháp) / P (thứ tự ưu tiên phím) | 1, 2.4 |
| B20 | Xác nhận khi đóng tab/app có dữ liệu chưa lưu | `ui.AlertDialog(c *Context, open *bool, title, message string, buttons ...string) int`; `ui.AlertDismissed` | E23 | Save/Discard/Cancel là ba nút; Escape = Cancel | Hộp thoại do view dựng nên chỉ hiện khi vòng lặp view chạy | S | 1 (khung), 2.3/2.4 |
| B21 | Nhớ kích thước cửa sổ, theme, sidebar | `WindowOptions.StateKey`; `App.SetWindowStateStore(WindowStateStore)`; `(*Window).Size()`, `OnResize` | E24 | Spec yêu cầu `settings.json` tại `~/.dbl/` nên lưu kích thước/sidebar/theme trong chính file đó; nếu dùng `StateKey` thì mặc định ghi `window-state.json` ở `PathUserData` (vị trí khác `~/.dbl/`) hoặc cài `WindowStateStore` ghi vào `settings.json` | Chọn một nguồn sự thật duy nhất ở Phase 1 để tránh hai file lệch nhau | S | 1 |
| B22 | Explorer cây (lazy) | `ui.Outline[K comparable](c, s *OutlineState[K], roots []K, children func(K) []K, row func(K)) Element` | E25 | `children` chạy trong view nên chỉ được trả dữ liệu đã cache; nạp metadata ở worker khi nút được mở rồi `Window.Update` | Cần xác nhận cách phát hiện sự kiện "node vừa được mở" từ `OutlineState.Open` | S (khung) / P (hook mở nút) | 1 (mock), 2.2 |

**Khoảng trống chốt theo dòng.** `B06`, `B10`, `B12` và phần route phím của `B11`/`B19` là các điểm cần quyết định trước khi Phase 1 dùng làm nền; chúng cần probe nhỏ trong Phase 1 (mock grid), xem đề xuất ở `P0-D`. Không có dòng `Unsupported` cho khả năng bắt buộc.

**Review các scenario bắt buộc** (bằng source, không phải runtime):

| Scenario | Cơ chế/API | Kết luận |
| --- | --- | --- |
| Chỉ dựng phần nhìn thấy, không phải toàn bộ dataset | `buildList` chỉ gọi hàng trong vùng nhìn (E15); `cell(i, j)` cho mọi cột của hàng đó (`table.go:262`) | Hàng: S. Cột: P (B06) |
| Enter chỉ xác nhận draft; Escape hủy | `EditableText` ghi vào `draft` trước, chỉ gán `*value` khi Enter/mất focus (`editable.go:104-124`); không có I/O | Cơ chế đúng, nhưng dùng làm editor ô là P (B12) |
| F2/double-click đặt focus đúng | F2 chỉ mở `EditableText` đầu tiên của hàng; double-click chỉ báo `Submitted` | App tự dựng (B12) – P |
| Cắt hiển thị không đổi giá trị typed | `SingleLine()`/`Ellipsis` chỉ ảnh hưởng vẽ (E17) | S |
| Đổi theme không tái tạo workspace | `SetSource` → `apply`, `updateBackgrounds`, `contentThemeChanged` (không tạo lại cửa sổ; `modules.go:473-492`); state tab nằm trong app | S (cơ chế); runtime `Not run` |
| Native renderer không dựa vào browser | `docs/ui/README.md:3-7`: không khởi tạo webview; GPU backend theo OS (E07) | S; DLL/GPU fallback xem P0-D |

<!-- P0-C -->
### P0-C. Async operation lifecycle

**UI dispatch API thực** (E27–E29):

| ID | Nguồn | Claim |
| --- | --- | --- |
| E27 | `M` `content.go:152-179` (`Invalidate`, `Update`), `content.go:181-` (`contentChanged`); `docs/ui/views.md` (mục "Change the state from other goroutines") | `func (w *Window) Update(fn func())`: chạy `fn` trên main thread rồi vẽ frame mới, trả về ngay; an toàn từ goroutine bất kỳ. `func (w *Window) Invalidate()`: chỉ vẽ lại, gộp nhiều lời gọi |
| E28 | `M` `loop.go:19-34` (`post`), `loop.go:67-80` (`shutdown`), `loop.go:86` (`RunOnMain`), `loop.go:94-107` (`onMain`) | `post` trả `false` và **bỏ `fn`** khi event loop đã dừng; `Update` không kiểm tra giá trị trả về; `RunOnMain` chặn đến khi `fn` xong và bị bỏ sau khi app thoát |
| E29 | `M` `docs/ui/views.md` (mục Views): "Keep build values out of app state and background work"; `docs/ui/views.md` cuối trang: `mygo vet` | Giá trị `ui.Element` chỉ sống trong một lượt build; worker chỉ nhận dữ liệu thuần |
| E30 | `M` `dialog.go:11-12`, `secrets.go` (mục doc của `Secrets`), `clipboard.go:118-176` | `Dialog.*` chặn tới khi đóng; `Secrets.*` an toàn từ goroutine nhưng có thể chặn khi hệ thống hỏi mở khóa; `Clipboard.*` an toàn từ goroutine |

Kết luận cho Step 1:

- Cơ chế an toàn để cập nhật UI từ worker là `win.Update(func(){ ... })` (S). Không có API thay đổi state UI trực tiếp từ worker.
- **`Update` không tự bảo vệ chống kết quả cũ:** nếu cửa sổ đã đóng nhưng event loop còn chạy thì `fn` **vẫn chạy** trên main thread (chỉ phần vẽ lại bị bỏ qua vì `w.conn == nil`); nếu loop đã dừng thì `fn` bị bỏ lặng lẽ. Vì vậy stale/closed-tab guard phải nằm **trong** `fn` do app viết.
- Dialog và Secrets là lời gọi chặn: chỉ gọi từ worker goroutine, không từ lượt build của view.
- Chưa phát hiện blocker threading cho Phase 1. Điều chưa kiểm chứng runtime (`Not run`): thứ tự thực thi các `Update` khi nhiều sự kiện đến cùng lúc (FIFO theo `mainLoop.queue`, `loop.go:41-60`) và hành vi lúc app đang thoát.

**Operation protocol (hợp đồng thiết kế, chưa phải Go struct):**

| Trường | Ý nghĩa |
| --- | --- |
| `operationID` | ID duy nhất, tăng dần cho từng lần chạy |
| `tabID` | Tab sở hữu kết quả |
| `connectionID` | Connection được dùng khi bắt đầu |
| `requestGeneration` | Số hiệu thế hệ của "trạng thái grid hiện tại" của tab; tăng mỗi khi filter/sort/page/reload/đổi connection thay thế kết quả cũ |
| `context` | `context.Context` tạo bởi application service, truyền cho mọi lời gọi DB |
| `cancel` | `context.CancelFunc` của `context` trên |

Máy trạng thái của một operation: `idle → loading → success | error | canceled`; từ `success`/`error`/`canceled` có thể sang `loading` khi bắt đầu operation mới; `loading → loading` chỉ qua bước "tăng generation + cancel operation cũ" (không có chuyển trực tiếp). Với `error`/timeout khi tab đã có dữ liệu: giữ dữ liệu cũ, đánh dấu cũ/lỗi, không xóa grid.

**Vòng đời một operation:**

1. Trên UI thread, application service tăng `requestGeneration` của tab, **cancel** operation đang chạy (nếu có), tạo `context` mới, đăng ký `{operationID, tabID, connectionID, requestGeneration, cancel}` và đặt trạng thái `loading`.
2. Worker goroutine chạy I/O với `context` đó. Worker sở hữu `*sql.Rows`/transaction/connection cho đến khi xong; khi xong nó **đóng/drain** các tài nguyên đó và chỉ giữ dữ liệu thuần (đã bound).
3. Worker gọi `win.Update(func(){ gate; apply })`.
4. **Completion gate (trên UI thread, bên trong `fn`):** chỉ áp dụng nếu đồng thời: app chưa đang thoát; tab còn tồn tại; `tab.connectionID == connectionID`; operation đang đăng ký của tab có đúng `operationID`; `tab.requestGeneration == requestGeneration`; (kiểm tra thêm) `context.Err() == nil`. Nếu không: bỏ kết quả, không chạm state tab.
5. Hủy chỉ là tín hiệu "cố gắng dừng", không bảo đảm completion không đến; driver có thể trả thành công muộn → gate ở bước 4 là điều kiện bắt buộc, không phụ thuộc cancel.

**Resource ownership:**

| Tài nguyên | Ai cancel | Ai close/rollback | Khi nào release | State được giữ |
| --- | --- | --- | --- | --- |
| Connect attempt (test/connect) | Application service khi người dùng hủy, sửa/xóa profile, thoát app | Worker: đóng connection/pool nếu hủy hoặc lỗi; nếu thành công muộn sau cancel thì đóng ngay, không đăng ký vào manager | Khi attempt kết thúc | Profile; mật khẩu chỉ đọc từ credential store khi cần, không giữ lâu |
| Metadata / page fetch | Operation mới của cùng tab, đóng tab, đổi/ngắt connection | Worker (`defer rows.Close()` trước khi post) | Trước khi post kết quả | Trang/cache cũ đến khi có kết quả generation mới được chấp nhận; cache có giới hạn |
| SQL rows/cursor | Người dùng bấm Cancel, đóng tab, ngắt connection | Query executor trong worker: đóng rows/cursor; chỉ đọc có giới hạn | Khi đọc xong, hết giới hạn hoặc bị hủy | Kết quả đã bound; không giữ cursor ở UI |
| Save transaction | Hủy chỉ được phép **trước** `COMMIT`; sau khi gửi `COMMIT` không cancel (tránh kết quả không xác định) | Worker: `defer Rollback` nếu chưa commit | Khi commit/rollback có kết quả rõ ràng | **Pending edits giữ nguyên** cho đến khi commit được xác nhận; commit không xác định → giữ edits, báo rõ, không tự retry |

Đóng tab/connection/app: xóa đăng ký operation, tăng generation, gọi `cancel` → mọi completion về sau bị gate loại; riêng tab đang `Save` chưa xác định thì không đóng im lặng mà phải hỏi (thiết kế, do Phase 2.3 chốt chi tiết).

**Scenario table (review bằng thiết kế + source; runtime `Not run`):**

| # | Trigger | Expected state | Cơ chế (source/thiết kế) |
| --- | --- | --- | --- |
| 1 | A hoàn thành sau B (A bắt đầu trước) | Grid giữ kết quả của B | `requestGeneration` của A < generation hiện tại → gate loại A (thiết kế; `Update` chỉ đảm bảo chạy trên main thread, E27) |
| 2 | Tab đóng trước khi completion tới | Không mutate tab đã đóng, không panic | Đóng tab xóa đăng ký + `cancel`; gate không tìm thấy tab/operation → bỏ qua; `Update` vẫn chạy `fn` sau khi cửa sổ/tab đóng nên guard nằm trong `fn` (E27, E28) |
| 3 | Đổi connection khi đang fetch | Kết quả của connection cũ bị bỏ | `connectionID` khác và generation tăng → gate loại; worker đóng rows trước khi post |
| 4 | Cancel nhưng driver trả success muộn | Không ghi đè state mới | Gate kiểm tra `operationID`/generation/`context.Err()`, không tin cancel (thiết kế) |
| 5 | Timeout/lỗi khi đã có dữ liệu cache | Giữ dữ liệu cũ, báo lỗi/trạng thái cũ | Chuyển `error` nhưng không xóa trang cũ (thiết kế) |
| 6 | Mất mạng trong Save | Edits còn nguyên, không retry tự động, báo kết quả không xác định nếu đã gửi `COMMIT` | Rollback trước `COMMIT`; sau `COMMIT` coi kết quả là không xác định; không có vòng retry (thiết kế, theo spec mục 10) |

**Kết luận P0-C:** có API dispatch xác định (`Window.Update`) và không có blocker threading nào ở mức source; stale-result guard phải do ứng dụng cài đặt trong `fn` vì `Update` luôn chạy `fn` khi loop còn hoạt động.

<!-- P0-D -->
### P0-D. Packaging và kiểm tra thủ công

Evidence bổ sung (đường dẫn tương đối `M`; số dòng thuộc commit `4d8673cd`; các mục E32–E38 được trích bởi một agent đọc source rồi tôi kiểm tra chéo trực tiếp các điểm quan trọng: WebView2 chỉ cho web window, hai loại file config, WARP/CPU, `CGO_ENABLED=0`, cờ `-H=windowsgui`, phụ thuộc `.deb`; các số dòng còn lại chưa được đối chiếu từng dòng):

| ID | Nguồn | Claim |
| --- | --- | --- |
| E32 | `cmd/mygo/config.go:18-19,192-234`, `cmd/mygo/template/native/mygo.json.tmpl:1-6`, `docs/configuration.md:36-39,175-190` | CLI đọc `mygo.json` (JSON) hoặc `mygo.config.ts`; có cả hai là lỗi; template native chỉ tạo `mygo.json` (`name`, `identifier`, `version`, `out`); key không biết bị bỏ qua |
| E33 | `internal/windows/app.go:108-136`, `internal/gpu/d3d11/device/device.go:30-48`, `docs/ui/rendering.md:1-21`, `ui/window.go:364-394` | Windows nạp DLL hệ thống theo tên (comctl32, user32, gdi32, dwmapi, shcore, ole32, advapi32, shell32, uxtheme, d3d11, dwrite, uiautomationcore…); WebView2 chỉ khởi tạo khi có cửa sổ web page ("an app whose windows all [show native UI] runs without the runtime", `app.go:133-135`); Direct3D 11 → WARP → vẽ bằng CPU; `MYGO_GPU=0` ép CPU |
| E34 | `docs/getting-started.md:10,76,96-100,183-186`, `cmd/mygo/init.go:51-61,144-150,166,172-189,293-299`, `cmd/mygo/build.go:214,300-358`, `cmd/mygo/proc.go:72-76` | Lệnh `init -template native`, `go tool mygo dev`, `go tool mygo build`; `init` ghi `go.mod` với `go <phiên bản toolchain đang chạy init>` và thêm `tool` directive bằng `go get -tool github.com/egoist/mygo/cmd/mygo@latest`; build GUI dùng `-H=windowsgui`; `CGO_ENABLED=0` nếu chưa đặt |
| E35 | `cmd/mygo/nsis.go:30-70,100-113,284`, `cmd/mygo/winsign.go:19-34,65,187`, `cmd/mygo/resources.go:530-544`, `cmd/mygo/winres.go:14-19`, `docs/distribution.md:250-254,318-332` | Output Windows = thư mục `build/windows-amd64/` gồm `.exe` cùng resources; installer `<tên> Setup <ver> <arch>.exe` cần `makensis` (hoặc tự tải NSIS 3.13 trên Windows); ký số bằng `signtool` chỉ khi cấu hình; icon/manifest/version nhúng bằng `.syso` do Go sinh |
| E36 | `backend_windows.go:1`, `backend_linux.go:1`, `backend_darwin.go:1`, `backend_other.go:1`, `internal/linux/ffi.go:520-541`, `cmd/mygo/deb.go:154`, `cmd/mygo/installscript.go:242-245`, `cmd/mygo/dmg.go:32`, `docs/getting-started.md:198-203`, `docs/distribution.md:34-35,389` | Backend native: macOS Metal, Linux GTK 3 + OpenGL, Windows Direct3D 11 (amd64/arm64); docs nói build cho mọi nền tảng từ mọi máy; disk image macOS cần máy macOS; `.deb` luôn khai báo `libwebkit2gtk-4.1-0` kể cả app native-only (mâu thuẫn với `getting-started.md:20-21`) |
| E37 | `secrets.go:12-58,72-115`, `internal/windows/secrets.go:22-25,59-89`, `internal/linux/secrets.go:71-85`, `docs/architecture.md:3956` | `mygo.Secrets.Set/Get/Delete` dùng Windows Credential Manager, macOS Keychain, Linux libsecret; giá trị tối đa 2560 byte; lỗi `errors.ErrUnsupported` khi không có credential store |
| E38 | `paths.go:53-138` | Windows: `PathUserData` = `%AppData%\<AppName>`, `PathCache` = `%LocalAppData%\<AppName>`, `PathResources` = thư mục chứa exe |

**Platform matrix:**

| target | buildHost | toolchain | CGO | nativeLibraries | runtimeAssets | packagingCommand | credentialBackendOrFallback | evidence | verificationStatus |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **Windows 10/11 x64 (mandatory)** | Windows (hoặc host khác theo docs, E36); tải NSIS lần đầu cần mạng | Go ≥ `1.27.1` (E04); `go tool mygo` cần `tool` directive (E34) | Không cần cho framework (E03, E34); `ncruces/go-sqlite3` cgo-free (E31); các driver còn lại chưa kiểm tra cgo | Chỉ DLL hệ thống Windows (E33); **không** cần WebView2 cho app chỉ có native UI (E33); không có DLL đi kèm theo mặc định (E35); plugin SQLite của MyGo (không dùng cho DBL) sẽ kèm `mygo-sqlite3.dll` | `.exe` + resources cùng thư mục (`icon.png` nếu có), **không phải single exe**; installer là file đơn (E35) | `go tool mygo build` (E34); xem runbook bên dưới | Windows Credential Manager qua `mygo.Secrets`; không có thì hỏi mật khẩu theo session, không ghi plaintext (E37) | E33–E35, E37 | Source only; runtime `Not run` (chưa chạy trên Windows 10 sạch, chưa kiểm thử D3D11/WARP trên máy đích) |
| Windows arm64 (không yêu cầu bởi spec) | — | — | — | Backend D3D11 có arm64 (E36) | — | — | Như trên | E36 | Source only; không ghi nhận hỗ trợ |
| macOS | Disk image cần macOS (E36); codesign/notarize cần macOS | Go ≥ `1.27.1` | Không cần | Metal/AppKit hệ thống (E36) | App bundle + resources | `go tool mygo build` (docs) | Keychain (E37) | E36, E37 | Source/doc only; chưa xác nhận cho DBL |
| Linux | `.deb`/tar/install script sinh bằng Go (E36) | Go ≥ `1.27.1` | Không cần | GTK 3 + GLib/GIO/cairo, OpenGL (`libepoxy`/`libEGL`) hoặc CPU; cần `DISPLAY`/`WAYLAND_DISPLAY`; libsecret tùy chọn (E36, E37) | App + resources | `go tool mygo build` (docs) | libsecret; không có Secret Service → hỏi mật khẩu mỗi session (E37) | E36, E37 | Source/doc only; `.deb` đang khai báo thừa `libwebkit2gtk-4.1-0` (mâu thuẫn nguồn, ghi nhận) |

**Đánh giá dependency theo spec (không tải/cài):** các driver spec nêu có `go` directive thấp hơn floor của MyGo (E31) nên không cản toolchain. Chỉ `ncruces/go-sqlite3` có xác nhận cgo-free từ README; `pgx`, `mysql`, `go-mssqldb`, `go-redis` chưa được kiểm tra cgo cụ thể (chưa có bằng chứng ⇒ không kết luận "CGO-free"; dùng `go list -deps` ở phase driver tương ứng). Tài nguyên lưu của DBL (`~/.dbl/settings.json`, `connections.json`, `queries/`) do DBL tự quản lý theo spec, độc lập với `PathUserData` của MyGo (E38); nếu bật `StateKey` thì `window-state.json` nằm ở `PathUserData` (E24).

**Artifact/runtime contract (Step 4):**

| Câu hỏi | Kết luận từ source | Status |
| --- | --- | --- |
| Exe có cần DLL/assets/helper process kèm theo không? | Không DLL kèm theo mặc định; DLL hệ thống; resources (ví dụ `icon.png`) nằm cạnh exe và cũng được đưa vào installer (E35). Không có helper process cho native UI | Source; runtime `Not run` |
| Có phụ thuộc browser/server không? | Không: WebView2 không khởi tạo cho app native-only; `mygo dev -web` mới mở TCP listener; single-instance dùng Unix socket (E33) | Source |
| GPU không có / Direct3D 11 lỗi thì sao? | Thử phần cứng → WARP → vẽ bằng CPU (E33) | Source; `Requires probe` trên máy đích |
| Secure storage không có thì sao? | `Secrets.*` trả lỗi bọc `errors.ErrUnsupported`; DBL hỏi mật khẩu theo session (E37) | Source |
| "Hoạt động trên máy sạch"? | Chưa có bằng chứng; không khẳng định | `Requires probe` |
| Build lần đầu cần mạng? | Có thể: tải NSIS 3.13 khi thiếu `makensis` (E35) | Source |

**Command runbook cho người dùng.** Không lệnh nào dưới đây được agent chạy; không tạo file nào trong repo.

*Nhóm 1 — kiểm tra toolchain (có thể chạy ngay):*

| Mục | Giá trị |
| --- | --- |
| purpose | Biết Go và môi trường build thực tế |
| prerequisites | Go trong `PATH`; PowerShell |
| workingDirectory | Root repo (`Z:\CongThien\Temp\dbl`) |
| command | `go version` và `go env GOVERSION GOOS GOARCH CGO_ENABLED GOTOOLCHAIN` |
| expectedObservation | `GOOS=windows`, `GOARCH=amd64`; ghi lại `CGO_ENABLED`. `GOVERSION` ≥ `go1.27.1` (module MyGo yêu cầu `go 1.27.1`, E04) là lý tưởng. Nếu thấp hơn: với `GOTOOLCHAIN=auto` Go sẽ tự tải toolchain đủ mới khi Phase 1 chạy `go` trong module (tải qua mạng, là side effect Phase 1 cần người dùng chấp nhận); với `GOTOOLCHAIN=local` build sẽ báo lỗi (E39). Không tự sửa môi trường |
| sideEffects | Không tạo file; không ghi cấu hình (lệnh chỉ đọc) |

```powershell
go version
go env GOVERSION GOOS GOARCH CGO_ENABLED GOTOOLCHAIN
```

*Nhóm 2 — dành cho Phase 1, **chỉ dùng sau khi plan Phase 1 được duyệt** (không chạy ở repo hiện tại):*

| Lệnh (nguồn) | Ghi chú |
| --- | --- |
| `go run github.com/egoist/mygo/cmd/mygo@latest init -template native <dir>` (README, E03) | **Không dùng nguyên văn**: `@latest` vi phạm quy tắc pin. Phase 1 thay bằng `@v0.5.5` (suy ra từ cú pháp `module@version`, chưa chạy) và `-template` phải đứng trước `<dir>` (E34) |
| `go get -tool github.com/egoist/mygo/cmd/mygo@latest` (do `init` tự gọi, E34) | `init` dùng `@latest`, chỉ cảnh báo khi lỗi và tự chạy `go mod tidy` (`init.go:181-189`); Phase 1 phải xác minh `go.mod` sau `init` chứa `github.com/egoist/mygo v0.5.5` và `tool` directive đúng version |
| `go tool mygo dev` / `go tool mygo build` (E34) | Chạy từ thư mục app; output Windows ở `build/windows-amd64/` (khi `out` = `build`, E32, E34–E35); tạo file build, tải NSIS lần đầu nếu cần installer |

Không đưa `go test ./...` hay lệnh MyGo tự suy đoán vào checklist Phase 0.

**Đề xuất probe (chưa được phép chạy; cần plan riêng và code/dependency của Phase 1):**

| ID | Câu hỏi | Scenario nhỏ nhất | File dự kiến | Command | Quan sát mong đợi |
| --- | --- | --- | --- | --- | --- |
| PR1 | Chi phí render khi bảng nhiều cột (B06) | Mock grid 100 hàng × 20/100/300 cột, cuộn dọc/ngang | `internal/ui/grid.go` (mock) | `go tool mygo dev` (chỉ biết được sau Phase 1) | Mượt hay giật; so sánh theo số cột trên máy Windows tham chiếu; nếu không đạt thì chọn grid tự cắt cột theo offset cuộn |
| PR2 | Click ô vs chọn hàng; route `Ctrl+C`, `F2`, `Ctrl+Enter` (B10, B11, B19) | Mock grid + một SQL editor trong cùng cửa sổ | `internal/ui/{grid,sql_editor}.go` | như trên | Chọn được ô riêng với hàng; `Ctrl+Enter` chạy Execute (không chèn dòng mới); `F2` mở editor ô |
| PR3 | Focus khi mở editor ô và Enter/Escape (B12) | Mock editor ô bằng `TextInput` | `internal/ui/grid.go` | như trên | Enter chỉ ghi draft; Escape hủy; focus trả lại grid |
| PR4 | Đổi theme có giữ state tab/connection không (B14) | Mở 2 tab, chọn ô, đổi Light/Dark/System | `internal/ui/theme.go` | như trên | State còn nguyên sau khi đổi theme |
| PR5 | Hành vi `Update` khi tab/cửa sổ đã đóng và khi thoát app (C scenario 2) | Worker trì hoãn rồi gọi `Update` sau khi đóng tab | `internal/app/` (mock) | như trên | Gate loại kết quả; không panic |
| PR6 | App chạy trên Windows 10 sạch, không GPU (WARP/CPU), không có WebView2 | Chạy `.exe`/installer build ở Phase 1 trên máy/VM sạch | không | chạy file build | Cửa sổ mở, không đòi WebView2; ghi lại GPU/WARP |
| PR7 | Độ phủ highlight SQL cho PostgreSQL/MySQL/SQL Server (B16) | Mở file SQL mẫu có từ khóa riêng từng dialect | `internal/ui/sql_editor.go` | như trên | Từ khóa dialect được tô hay không; quyết định có cần `Tokenizer` riêng |

Tất cả probe đang ở trạng thái `Not run`; kết quả người dùng cung cấp phải được ghi là `User reported` kèm command, môi trường và quan sát.

**Kết luận P0-D:** có route packaging có nguồn cho Windows (E33–E35) và không có blocker ở mức source. Không có bằng chứng runtime nào; không diễn đạt là đã build/chạy thành công.

<!-- P0-E -->
### P0-E. Kết luận và gate sang Phase 1

**Decision summary** (đề xuất, chờ người dùng duyệt):

| Mục | Quyết định đề xuất | Dẫn chiếu |
| --- | --- | --- |
| Framework / version | `github.com/egoist/mygo` `v0.5.5` @ `4d8673cd2658a2c6e86b26c44751e57bf8174f91` | E01–E04 |
| Toolchain | Go ≥ `1.27.1` (phù hợp với `1.27+` của spec); `CGO_ENABLED=0` | E04, E05, E34 |
| UI primitives chính | `mygo.NewWindow` + `ui.View`, `ui.Split`, `ui.TabsBase`, `ui.Table`/`ListState`, `ui.Outline`, `editor.State/View`, `ui.AlertDialog`, `mygo.Theme`, `mygo.Dialog`, `mygo.Clipboard`, `mygo.Secrets` | B01–B22 |
| Async protocol | `Window.Update` + completion gate (operationID, tabID, connectionID, requestGeneration, context) | P0-C |
| Windows packaging | `go tool mygo build` → thư mục `.exe` + resources, installer NSIS tùy chọn; không cần WebView2 | P0-D |
| Credential | `mygo.Secrets` (Windows Credential Manager); fallback hỏi mật khẩu theo session | E37 |

**Coverage table cho năm yêu cầu Phase 0:**

| Yêu cầu | Task | Kết luận | Nguồn |
| --- | --- | --- | --- |
| 1. Framework + version + Go floor | 1 | `egoist/mygo v0.5.5`; Go ≥ `1.27.1` tương thích với spec | E01–E05 |
| 2. API UI (window, sidebar, tabs, theme, clipboard, dialogs, grid, editor, highlight) | 2 | 15/22 dòng `S`; `B06`, `B10`, `B12` là `P`; `B11`, `B16`, `B19`, `B22` vừa `S` vừa có phần `P` | E10–E26 |
| 3. DB work ngoài UI thread, trả kết quả về UI | 3 | `Window.Update` + gate trong `fn`; không có blocker threading ở mức source | E27–E30 |
| 4. Packaging Windows, macOS/Linux, CGO, runtime | 4 | Windows có route có nguồn, không cần CGO/WebView2; không phải single exe; secondary chỉ ở mức doc | E31–E38 |
| 5. Ghi nguồn, phiên bản, giới hạn, quyết định; probe xin phép riêng | 1–5 | Registry E01–E38; probe PR1–PR7 chờ duyệt | Toàn bộ |

**Review Focus → nơi kiểm tra:**

| Focus | Kết quả | Vị trí |
| --- | --- | --- |
| Nhầm sản phẩm / native mode | Chỉ tìm thấy `egoist/mygo`; native UI không webview, WebView2 không khởi tạo | P0-A, E06–E07, E33 |
| Toolchain không đáp ứng | Go `1.27.1` cần cho module; có go1.27.1/1.27.2; không hạ `1.27+` | P0-A, E04–E05 |
| UI đóng / request lỗi thời | `Update` vẫn chạy `fn` sau khi cửa sổ đóng ⇒ guard trong `fn` | P0-C, E27–E28 |
| Grid/editor chỉ demo | Hàng ảo hóa; cột không; cell selection/editor phải tự dựng | P0-B (B05–B12) |
| Phụ thuộc ngoài dự kiến | Không DLL kèm; DLL hệ thống; resources cạnh exe; `.deb` khai báo thừa WebKit | P0-D, E33–E36 |

**Phần chưa giải quyết:**

| Loại | Mục | Tác động |
| --- | --- | --- |
| `Requires probe` (ảnh hưởng virtualization / nền tảng) | B06 (cột), B10, B12, route phím B11/B19, B22, B16 dialect, D3D11/WARP/máy sạch | Cần đề xuất PR1–PR7 và quyết định của người dùng trước khi dùng làm nền; không mặc nhiên pass |
| `Unsupported` | Không có | — |
| Thiếu runtime evidence | Toàn bộ | Mọi kết luận ở mức source; chờ nghiệm thu |

**Deviation / quyết định cần người dùng duyệt:**

1. **Xác nhận upstream** là `egoist/mygo` (spec chỉ ghi "MyGo"). *Đã xác nhận bởi người dùng (2026-10-16).*
2. **Churn API `v0.x`:** chấp nhận pin `v0.5.5`, không tự nâng version, mỗi lần nâng phải đọc lại P0-B/P0-C. Phương án thay thế: chọn tag cũ hơn (`v0.5.4` là bản pkg.go.dev đang coi là mới nhất) — không có lý do kỹ thuật ưu tiên hơn.
3. **Go floor thực tế `1.27.1`** (không phải `1.27.0`): Go cục bộ là `go1.26.5` (`User reported`), thấp hơn cả floor `1.27+` của spec, và `GOTOOLCHAIN=auto`. Vì vậy khi Phase 1 chạy `go` trong module yêu cầu `go 1.27.1`, Go sẽ tự tải toolchain đủ mới từ `golang.org/toolchain` qua mạng (side effect mạng cần người dùng chấp nhận ở Phase 1; chưa có `go1.27.x` cục bộ). Lệnh `init` chạy bằng `go1.26.5` sẽ ghi `go 1.26.5` rồi `go get -tool` mới có thể nâng lên; phải kiểm tra `go.mod` sau `init` (E34, suy luận, chưa chạy). Cách khác là người dùng tự cài Go `1.27.1+` trước. `init` còn ghi `go <phiên bản cục bộ>` rồi `go get -tool` có thể nâng lên `1.27.1` (E34, suy luận, chưa chạy). Với `GOTOOLCHAIN=local` thì build báo lỗi.
4. **Single executable:** nền tảng cho Windows tạo thư mục `.exe` + resources hoặc installer; spec chỉ coi single exe là mục tiêu khi nền tảng cho phép ⇒ không phải deviation, ghi để Phase 5 không hứa quá mức.
5. **Grid:** nếu PR1 cho thấy `ui.Table` không đủ với bảng nhiều cột, cần quyết định grid tự cắt cột (thay đổi phạm vi Phase 1); chưa đề xuất thay đổi spec.
6. **`~/.dbl/` vs `PathUserData`:** giữ `settings.json` tại `~/.dbl/` theo spec; không dùng `window-state.json` mặc định của MyGo (hoặc cài `WindowStateStore` ghi vào `settings.json`).
7. **Lệnh scaffold dùng `@latest`:** Phase 1 phải pin `@v0.5.5` và kiểm tra `go.mod` sau `init`.
8. **Không sửa `docs/specs.md`:** không có bằng chứng buộc phải đổi spec (`mygo.json` trong spec là tên file cấu hình hợp lệ, E32); không đề xuất thay đổi spec.

**Trạng thái đề xuất:** `Ready for review` (không phải `Blocked`): không có `Unsupported` và không có bất tương thích Go floor. Chuyển sang Phase 1 cần người dùng (a) xác nhận upstream và chấp nhận rủi ro churn, (b) quyết định về các mục `Requires probe` ở trên (cho phép thêm probe nhỏ vào Phase 1 hay yêu cầu plan probe riêng), (c) chạy lệnh toolchain ở runbook nhóm 1 và báo kết quả.

**Exit checklist:**

- [x] Upstream và version tái lập được đã xác định, toolchain compatibility có nguồn (chờ người dùng xác nhận upstream).
- [x] Native capability bắt buộc có phương án/API hoặc câu hỏi probe rõ ràng; không có `Unsupported`.
- [x] Async protocol bảo vệ UI affinity, stale results, cancellation và cleanup (mức thiết kế).
- [x] Windows packaging/dependencies và điều kiện secondary targets có nguồn (secondary chỉ ở mức doc).
- [x] Commands/probes bàn giao đúng phạm vi, không ghi runtime pass.
- [ ] Deviations/blockers đã trình bày; **người dùng nghiệm thu** trước khi chuyển phase.

## 5. Phase 1 — Native UI Foundation

**Mục tiêu:** xây shell ứng dụng và kiểm tra mô hình tương tác bằng dữ liệu giả lập.

**Mapping:** mục 3, FR-009, mục 5, 8.1–8.3 và 11.1.

**Phạm vi:**

1. Khởi tạo Go/MyGo với dependency được pin, giữ package boundaries thực dụng; không tạo sẵn các adapter chưa cần.
2. Main window, sidebar resize, explorer giả lập, workspace và Table Inspector/SQL Editor tabs. Mỗi tab giữ state riêng; mở cùng table identity thì focus tab hiện có.
3. Grid giả lập có virtualization, scroll hai chiều, resize cột, chọn row/cell, copy cell và tương tác double-click/F2, Enter/Escape với editor cục bộ.
4. Light/Dark/System dùng semantic colors chung; focus, lỗi, disabled và dirty cell phân biệt được trong từng theme.
5. Đọc/ghi `settings.json` có version tại `~/.dbl/`; lưu theme, window size và sidebar width, dùng thay thế file an toàn khi khả thi.

**File/package dự kiến:** `main.go`, `go.mod`, `go.sum`, `mygo.json`; `internal/app/{app,state,actions}.go`; `internal/ui/{window,explorer,workspace,tabs,grid,sql_editor,theme}.go`; `internal/storage/config.go`; `resources/` theo nhu cầu thực tế.

**Tiêu chí hoàn thành:** shell dùng Native UI, đổi tab/theme giữ state, settings tồn tại sau khi mở lại app. Mock grid chỉ render phần nhìn thấy; thao tác cell chưa ghi database. Startup chỉ đọc local state cần thiết.

**Checklist kiểm tra cho người dùng:**

- [ ] Mở app, resize sidebar/window, đổi theme rồi mở lại.
- [ ] Mở nhiều tab, focus lại table đã mở và xác nhận state độc lập.
- [ ] Scroll mock dataset lớn, copy cell và kiểm tra keyboard/editor interactions.
- [ ] Ghi baseline startup/idle trên máy tham chiếu; chưa coi đây là số đo bản release.

## 6. Phase 2.1 — Connection & Storage

**Mục tiêu:** quản lý connection profiles/folders và mở PostgreSQL connection theo yêu cầu.

**Mapping:** FR-001, phần connection của FR-010, mục 6.1, 8.1, 8.4, 9–11.

**Phạm vi:**

1. Profile có UUID, driver, folder, typed TLS/options và timeout. Thêm create/edit/rename/delete, test/connect/disconnect và chuyển folder.
2. Folder một cấp, connection không có folder ở root; xóa folder cho chọn chuyển connections về root hoặc cancel.
3. `connections.json` có version; password ở credential store theo `SecretRef`. Nếu secure storage không có, hỏi password theo session; không ghi plaintext ra JSON/log.
4. PostgreSQL connect bằng `pgx/v5/stdlib` qua `database/sql`; pool nhỏ, có cấu hình; nhiều connections hoạt động đồng thời.
5. Timeout/cancel, category lỗi và cleanup. Startup không connect hoặc truy xuất credential; chỉ thực hiện khi cần. Test connection giải phóng tài nguyên khi xong.
6. Edit/delete/disconnect profile đang dùng xử lý các tab bị ảnh hưởng trước khi hủy tài nguyên. Phase 2.3/2.4 bổ sung bảo vệ grid edits/SQL content vào luồng này.

**File/package dự kiến:** `internal/connection/{models,manager}.go`; `internal/storage/{config,credentials}.go`; `internal/database/{adapter,models}.go`; `internal/database/postgres/`; `internal/ui/explorer.go` và form/dialog connection theo cấu trúc thực tế; `internal/app/`.

**Tiêu chí hoàn thành:** profiles/folders được lưu và khôi phục đúng; test/connect/disconnect chạy ngoài UI thread; lỗi xác thực, timeout và cancel có thông tin phù hợp; nhiều kết nối không chia sẻ nhầm session/state.

**Checklist kiểm tra cho người dùng:**

- [ ] CRUD profiles/folders, di chuyển connection và hai nhánh xóa folder.
- [ ] Kết nối thành công, sai password, TLS không hợp lệ, timeout và cancel trên PostgreSQL cô lập.
- [ ] Mở nhiều connections, sửa/xóa profile đang dùng và mở lại app.
- [ ] Kiểm tra JSON/log không chứa secret; kiểm tra fallback khi credential store không khả dụng.

## 7. Phase 2.2 — PostgreSQL Read Workflow

**Mục tiêu:** hoàn thành luồng connection → explorer → Table Inspector → đọc dữ liệu.

**Mapping:** FR-002, FR-003, phần đọc của FR-004, FR-006, FR-010; mục 6.2–6.3, 7.1–7.2 và 8.

**Phạm vi:**

1. Lazy explorer cho database/schema/tables/views, refresh từng node. PostgreSQL database selection theo connection; không giả định một session dùng được mọi database.
2. Table Inspector có Data/Properties; view chỉ đọc; không mở trùng tab theo connection và table identifier đầy đủ.
3. Properties có columns/native types, nullable/default, PK/unique/identity/ordinal, constraints, outgoing/incoming foreign keys, referenced tables/columns, indexes và triggers khi được hỗ trợ. Phân biệt dữ liệu không có với không được hỗ trợ hoặc không đủ quyền.
4. Page mặc định 100, server-side paging, cache có giới hạn và eviction rõ ràng. Establish stable order khi có thể; dùng unique tie-breaker khi sort cần nó. Offset được phép khi có stable order; ưu tiên keyset cho bảng lớn có key phù hợp và ghi rõ chiến lược đã chọn.
5. Một column filter với các operator trong FR-006, typed comparisons, contains cho text, NULL predicates; debounce cấu hình ban đầu khoảng 300 ms, reset page khi đổi filter và cancel request cũ.
6. Giá trị phân biệt NULL/empty/numeric/bool/datetime/text/binary/JSON. Truncate chỉ ở hiển thị; binary dùng placeholder. Grid có selection, copy, resize, sort, reload và scroll hai chiều.
7. Xác định row identity/editability từ metadata nhưng giữ grid chỉ đọc tới Phase 2.3. Nếu không có stable ordering, hiển thị cảnh báo; không suy ra mọi bảng có PK.

**File/package dự kiến:** `internal/database/{models,adapter,dialect}.go`; `internal/database/postgres/` cho metadata/dialect/paging; `internal/grid/controller.go` và value conversion; `internal/ui/{explorer,grid,properties}.go`; `internal/app/`.

**Tiêu chí hoàn thành:** đọc đúng dữ liệu/metadata PostgreSQL bằng luồng thật, không tải cả bảng; filtering/sorting chạy trên server; canceled/stale results không thay state mới; lỗi metadata không làm dữ liệu thành editable ngoài ý muốn.

**Checklist kiểm tra cho người dùng:**

- [ ] Bảng thường, view, bảng không PK, composite keys, tên reserved/Unicode và quyền đọc bị giới hạn.
- [ ] NULL/empty, số có precision, datetime, JSON, binary và large values không bị biến đổi do format.
- [ ] Filter nhanh liên tiếp, đổi sort/page, reload, refresh metadata và đóng tab khi request đang chạy.
- [ ] Cache/result memory có giới hạn; explorer không nạp toàn bộ metadata khi connect.

## 8. Phase 2.3 — PostgreSQL Safe Editing

**Mục tiêu:** thay đổi nhiều cell/row rồi Save hoặc Revert một cách có kiểm soát.

**Mapping:** FR-005, phần chỉnh sửa FR-004, mục 7.3–7.4, 9–10 và 12.

**Phạm vi:**

1. Row identity ưu tiên PK rồi non-null unique key đáng tin cậy, dùng canonical typed values. Không có identifier phù hợp thì chỉ đọc.
2. Chặn sửa PK, generated/identity, binary/large objects, view và query result cells. Adapter báo rõ trường hợp không thể bảo đảm concurrency check theo kiểu dữ liệu.
3. Draft/changeset tách khỏi loaded rows, giữ original row identity và original values cần kiểm tra conflict; validation errors gắn với edit. Sửa về giá trị gốc phải bỏ dirty state.
4. Double-click/F2 mở editor; Enter chỉ xác nhận edit cục bộ; Escape hủy giá trị trong editor; Save mới ghi database; Revert bỏ pending changes.
5. Validate toàn bộ trước khi ghi, dùng transaction cho changeset của tab, parameterized UPDATE và NULL-safe optimistic concurrency. Affected rows bằng 0 là conflict/deleted row; lớn hơn 1 là unsafe update; lỗi phải rollback toàn bộ Save.
6. Chỉ xóa pending changes sau commit đã xác nhận thành công. Commit thành công nhưng reload lỗi phải báo đã lưu và lỗi tải lại, tránh submit lại thay đổi đã commit. Kết quả commit không xác định phải được báo rõ, giữ dữ liệu edit để xử lý và không tự retry.
7. Save/Discard/Cancel khi đổi filter/page, reload, disconnect, đóng tab/app hoặc xóa profile có edits. Đổi tab giữ changes; refresh metadata không ghi đè edits. Mọi action làm thay thế loaded rows, kể cả đổi sort, phải đi qua cơ chế bảo vệ tương ứng.

**File/package dự kiến:** `internal/grid/{changeset,validation,persistence,controller}.go`; `internal/database/postgres/` cho update/concurrency; `internal/ui/{grid,tabs}.go`; `internal/connection/manager.go`; `internal/app/` cho điều phối action và exit.

**Tiêu chí hoàn thành:** một Save thành công áp dụng changeset trong một transaction; lỗi không mất edits và không để lại cập nhật một phần. Editing không được bật khi identity/concurrency không an toàn.

**Checklist kiểm tra cho người dùng:**

- [ ] Nhiều cell/row, composite key, unique-key fallback, NULL và revert về original value.
- [ ] Hai session cập nhật cùng row, row bị xóa, lỗi giữa batch và rollback.
- [ ] Validation failure không phát SQL write; lỗi save giữ edits; mất mạng không tự retry.
- [ ] Từng nhánh Save/Discard/Cancel ở các action bảo vệ pending edits; commit thành công nhưng reload thất bại.

## 9. Phase 2.4 — SQL Editor & Local Files

**Mục tiêu:** hoàn thành PostgreSQL MVP với SQL execution và quản lý file SQL cục bộ.

**Mapping:** FR-007, FR-008, mục 3.2, 6.3, 8–10.

**Phạm vi:**

1. Nhiều SQL tabs độc lập, monospaced font, syntax highlighting, scroll hai chiều, line numbers khi được hỗ trợ, unsaved indicator và connection selector rõ ràng.
2. Execute selection trước; nếu không có selection, xác định statement tại cursor bằng parser theo dialect. Không split mọi dấu chấm phẩy; complex scripts chưa hỗ trợ dùng selected block với giới hạn được ghi rõ.
3. Query executor có cancellation, lỗi SQL, affected row count và read-only results. SELECT dùng page mặc định 100 khi pagination an toàn; trường hợp khác có giới hạn rõ, không đọc result unbounded. Không lặp write statement để lấy trang kết quả tiếp theo.
4. Tôn trọng transaction semantics của database; đóng rows/cursor/session resources đúng lúc và không tự retry write queries.
5. New/Open/Save/Save As, default dưới `~/.dbl/queries/`; first Save hỏi tên/vị trí, subsequent Save cập nhật file gắn với tab, validate path và safe file replacement khi phù hợp. Folder file SQL độc lập với folder connections.
6. Bảo vệ SQL content chưa lưu khi đóng tab/app hoặc xử lý connection đang dùng; lỗi file không làm mất buffer.
7. Shortcuts Ctrl+N, Ctrl+S, Ctrl+Enter, F2, Escape, F5 theo context; macOS theo convention phù hợp. Explorer có action mở SQL tab cho connection được chọn; Ctrl+Shift+F không được gán.

**File/package dự kiến:** `internal/query/{executor,results}.go` và statement parser; `internal/ui/{sql_editor,tabs,explorer}.go`; `internal/storage/files.go`; `internal/database/postgres/`; `internal/app/`.

**Tiêu chí hoàn thành:** mở connection PostgreSQL, xem/sửa bảng, chạy SQL và lưu/mở file trong cùng ứng dụng. Query results chỉ đọc; cancel/timeout và file errors không làm mất nội dung hoặc đóng băng UI. Milestone MVP chỉ hoàn thành khi Phase 2.3 cũng đạt tiêu chí.

**Checklist kiểm tra cho người dùng:**

- [ ] Selection/current statement với strings, comments, quoted identifiers và procedural SQL chứa dấu chấm phẩy.
- [ ] SELECT lớn, write statement, syntax/permission error, timeout, cancel và nhiều tabs/connections.
- [ ] Save đầu tiên, overwrite, Save As, file không có quyền, đường dẫn không hợp lệ và unsaved prompts.
- [ ] End-to-end PostgreSQL: quản lý connection → explorer → đọc/sửa/Save/Revert → SQL → local file.

**Kết quả triển khai Phase 2.4 (2026-10-17):** đã bổ sung dedicated SQL sessions, statement selection/parser PostgreSQL, bounded read-only results, cancellation, local SQL files và guards nội dung/transaction theo [spec](superpowers/specs/2026-10-17-phase-2-4-sql-editor-local-files-design.md) và [plan](superpowers/plans/2026-10-17-phase-2-4-sql-editor-local-files.md). Trạng thái **chờ người dùng kiểm tra app và nghiệm thu**. Agent không chạy build/test/lint, PostgreSQL integration hoặc manual app testing theo chỉ dẫn trực tiếp; các checklist trên chưa được xác nhận. Không coi source implementation là bằng chứng PostgreSQL MVP đã nghiệm thu.

## 10. Phase 3.1 — SQLite

**Mục tiêu:** đưa shared relational workflow sang database file cục bộ.

**Mapping:** FR-001–FR-008 và FR-010 trong phạm vi SQLite; mục 6–10, 12.2.

**Phạm vi:**

1. Adapter dùng `ncruces/go-sqlite3`, SQLite file profile và hierarchy `main` với tables/views.
2. Metadata, native values, constraints/FKs/incoming references/indexes/triggers theo khả năng engine; hiển thị rõ giới hạn introspection.
3. Quoting/placeholders, paging/filter/sort, SQL parser/execution và transactional updates/concurrency theo SQLite; cấu hình connection riêng thay vì áp dụng máy móc pool network database.
4. Reuse UI/application contracts; chỉ điều chỉnh phần shared khi bằng chứng engine mới cho thấy hợp đồng chưa đủ.

**File/package dự kiến:** `internal/database/sqlite/`; driver-specific validation trong `internal/connection/`; phần shared `internal/database/`, `internal/query/`, `internal/grid/` khi cần; integration fixtures tại `tests/integration/`.

**Tiêu chí hoàn thành:** SQLite đạt luồng đọc/ghi/SQL/file tương ứng PostgreSQL, với giới hạn engine được hiển thị rõ và chế độ chỉ đọc khi không an toàn.

**Checklist kiểm tra cho người dùng:**

- [ ] Local file hợp lệ/không có quyền/bị khóa; file path Unicode; cleanup khi disconnect.
- [ ] Composite keys, NULL, generated columns, views, reserved identifiers, concurrency và rollback.
- [ ] SQL selection/parser, pagination/filter/sort và shared UI regression với PostgreSQL.

## 11. Phase 3.2 — MySQL/MariaDB

**Mục tiêu:** bổ sung engine network relational với database/schema mapping riêng.

**Mapping:** FR-001–FR-008 và FR-010 trong phạm vi MySQL/MariaDB; mục 6–10, 12.2.

**Phạm vi:**

1. Adapter dùng `go-sql-driver/mysql`, typed connection/TLS options và database-as-schema hierarchy.
2. Metadata và capabilities theo server/version được hỗ trợ; paging/filter/sort, quoting/placeholders và value conversion theo dialect.
3. Updates với NULL-safe concurrency và affected-row semantics được xác định rõ. Nếu table/storage engine không cung cấp transaction guarantee cần thiết, disable unsafe Save và nêu lý do.
4. SQL statement parsing/execution với syntax đặc thù và selected-block fallback; ghi rõ phiên bản MySQL/MariaDB được hỗ trợ trong phase.

**File/package dự kiến:** `internal/database/mysql/`; connection validation và statement parser theo dialect; integration fixtures tại `tests/integration/`; shared contracts khi cần.

**Tiêu chí hoàn thành:** luồng relational nhất quán theo capability; không tự coi metadata, transaction hoặc affected-row behavior giống PostgreSQL.

**Checklist kiểm tra cho người dùng:**

- [ ] Connect/TLS, database/schema navigation, metadata và permission errors.
- [ ] Unicode/collation, precision, NULL, composite keys, filter operators và pagination.
- [ ] Conflict, rollback, affected-row semantics, transaction support và MySQL/MariaDB compatibility theo phiên bản đã chốt.

## 12. Phase 3.3 — SQL Server

**Mục tiêu:** hoàn thành bộ relational drivers trong spec.

**Mapping:** FR-001–FR-008 và FR-010 trong phạm vi SQL Server; mục 6–10, 12.2.

**Phạm vi:**

1. Adapter dùng `go-mssqldb`, connection/authentication/TLS options theo phạm vi được chốt và catalog/schema hierarchy.
2. Metadata cho columns/constraints/FKs/incoming references/indexes/triggers; identity/generated fields và row-version capability khi có.
3. Quoting/placeholders, pagination có ordering phù hợp, typed filtering, value conversion và concurrency predicates theo engine/type.
4. SQL execution/parser theo dialect; hỗ trợ giới hạn scripting được ghi rõ. Disable editing khi không thể tạo concurrency checks đáng tin cậy.

**File/package dự kiến:** `internal/database/sqlserver/`; connection validation và statement parser theo dialect; integration fixtures tại `tests/integration/`; shared contracts khi cần.

**Tiêu chí hoàn thành:** PostgreSQL, SQLite, MySQL/MariaDB và SQL Server dùng cùng các luồng relational, với khác biệt capability được biểu diễn rõ.

**Checklist kiểm tra cho người dùng:**

- [ ] Catalog/schema, reserved/Unicode identifiers, metadata bị giới hạn quyền và identity/generated fields.
- [ ] Paging/sorting, placeholder binding, NULL, precision, row-version hoặc original-value conflict checks.
- [ ] Transactions/rollback, timeout/cancel, SQL parser/execution và regression trên các engine trước.

## 13. Phase 4 — Redis

**Mục tiêu:** duyệt và chỉnh sửa Redis theo mô hình key/value, tách khỏi relational contract.

**Mapping:** FR-001, phần Redis của FR-010, mục 6.3, 8–10 và 12.2.

**Phạm vi:**

1. Adapter capability riêng dùng `redis/go-redis`; reuse profiles/folders/credentials, connect/disconnect, logical database selection khi deployment hỗ trợ.
2. Key discovery bằng `SCAN`, name pattern filter, cancellation, giới hạn batch/cache; không dùng `KEYS` để duyệt tổng quát. Xử lý duplicate keys và keyspace thay đổi trong lúc scan; không hứa snapshot ổn định.
3. Workspace trình bày key type, TTL và giá trị cho String/Hash/List/Set/Sorted Set. Đọc collection theo trang/batch hoặc giới hạn phù hợp, không tải toàn bộ key lớn.
4. Trước phần ghi, lập capability matrix cho từng type: thao tác sửa được hỗ trợ, command, giới hạn tải, conflict behavior, TTL behavior và write guarantee; xin duyệt trong plan của phase.
5. Supported edits giữ draft đến Save/Revert; pending edits được bảo vệ khi chuyển key, refresh, đóng tab hoặc disconnect. Không áp dụng SQL/table constraints/relational pagination cho Redis.
6. Dùng command phù hợp từng type; nêu rõ atomicity của từng Save và trường hợp nhiều key. Không hứa transaction semantics giống SQL, không tự retry ambiguous writes; lỗi giữ edits và báo rõ phần kết quả đã xác định.

**File/package dự kiến:** `internal/database/redis/`; capability/state riêng cho Redis trong application layer; `internal/ui/` cho key explorer và value inspector/editor; integration fixtures tại `tests/integration/`. Bố trí file cụ thể theo approved plan, không ép Redis implement `SQLAdapter`.

**Tiêu chí hoàn thành:** duyệt được cả năm types và sửa được thao tác đã được duyệt trong capability matrix; TTL/write guarantees được giải thích chính xác; dữ liệu lớn vẫn được đọc có giới hạn.

**Checklist kiểm tra cho người dùng:**

- [ ] SCAN/pattern, duplicate/expired/deleted keys, large keyspace và large collections.
- [ ] Năm types, TTL, giá trị không phải UTF-8 và trường hợp deployment không hỗ trợ logical DB selection.
- [ ] Save/Revert, concurrent modification, permission errors, timeout/cancel và ambiguous write failures.
- [ ] Mỗi thao tác ghi giữ đúng TTL/conflict/atomicity contract đã duyệt; không mất edits khi lỗi.

## 14. Phase 5 — Stabilization & Packaging

**Mục tiêu:** hoàn tất release theo Definition of Done, dựa trên kết quả kiểm tra thực tế.

**Mapping:** mục 1.2, FR-009, mục 8–12, roadmap Phase 5 và mục 14–15.

**Phạm vi:**

1. Tổng hợp regression matrix cho từng engine, không đồng nhất capability thiếu với lỗi implementation. Bao phủ connection/explorer/inspector/grid/SQL/files/Redis, theme, shortcuts và pending edits.
2. Rà soát cancellation, stale results, tab/connection lifecycle, cleanup rows/cursors, memory bounds, file safety, credential redaction và khả năng rollback/conflict.
3. Người dùng chạy đo cold startup, idle RAM/CPU, grid scroll, initial load, paging, schema expansion, large results và repeated tab open/close trên máy/OS được ghi nhận.
4. Đối chiếu mục tiêu ban đầu: cold startup dưới 1 giây, idle RAM dưới 100 MB khi chưa có active connection, CPU idle gần 0 và grid scroll hướng tới 60 FPS. Startup không tính connection/network latency; ghi số đo thực tế và deviation được duyệt, không coi target là benchmark có sẵn của framework.
5. Packaging Windows 10/11 x64 trước; macOS/Linux khi được hỗ trợ. Ghi toolchain/dependencies, credential backend, CGO/runtime resources và những gì cần đi kèm executable.
6. Hoàn thiện installation/usage, supported versions, capability limitations, Redis write guarantees, SQL selected-block fallback, performance evidence và cách tự chạy kiểm tra.

**File/package dự kiến:** các file liên quan trực tiếp tới lỗi được tìm thấy trong `internal/`; test/fixture của từng package và `tests/integration/`; `mygo.json`, `resources/`, `README.md` và tài liệu release/capability trong `docs/` theo plan riêng. Packaging scripts chỉ thêm khi workflow thực tế cần.

**Tiêu chí hoàn thành:** mọi mục Definition of Done có bằng chứng hoặc giới hạn đã được người dùng duyệt; checks do người dùng chạy có kết quả được ghi nhận; package chạy trên primary target. Không ghi nhận platform chưa kiểm tra là đã được xác nhận hỗ trợ.

**Checklist kiểm tra cho người dùng:**

- [ ] Unit/integration/UI checks từ các phase trước, đặc biệt mọi hành vi ghi dữ liệu.
- [ ] Functional regression trên từng engine/version và platform được công bố.
- [ ] Performance measurements và resource lifecycle sau nhiều vòng mở/đóng tab.
- [ ] Cài/chạy package, load/save local config/SQL files và credential fallback trên máy đích.

## 15. Coverage và điều kiện chuyển phase

| Nhóm yêu cầu | Phase sở hữu chính | Mở rộng/nghiệm thu |
| --- | --- | --- |
| FR-001 — Connections/folders/credentials | 2.1 | 2.3–2.4 bảo vệ edits/content; 3.1–3.3 và 4 mở rộng engine |
| FR-002 — Explorer | 2.2 | 2.4 SQL-tab action; 3.1–3.3 hierarchy từng engine |
| FR-003 — Table Inspector/Properties | 2.2 | 3.1–3.3 metadata từng engine |
| FR-004 — Data Grid | 1 và 2.2 | 2.3 editing; 3.1–3.3 dialect/capability |
| FR-005 — Safe Editing | 2.3 | 3.1–3.3 write/concurrency semantics |
| FR-006 — Filtering | 2.2 | 3.1–3.3 dialect/typed operators |
| FR-007 — SQL Editor | 2.4 | 3.1–3.3 parser/execution theo dialect |
| FR-008 — SQL Files | 2.4 | 5 cross-platform/file-error regression |
| FR-009 — Theme | 1 | Áp dụng khi thêm UI ở mọi phase; 5 regression |
| FR-010 — Driver Support | 2.1–2.4 PostgreSQL | 3.1 SQLite; 3.2 MySQL/MariaDB; 3.3 SQL Server; 4 Redis |
| Mục 8–11 — Performance, errors, security, config | Mỗi phase có liên quan | 5 tổng hợp evidence |
| Mục 12 và 14 — Testing/Definition of Done | Checklist từng phase | 5 nghiệm thu release |

Trước khi triển khai một phase:

1. Xác nhận dependency đạt tiêu chí; phần chưa có runtime evidence phải được nêu rõ để người dùng quyết định.
2. Đọc source hiện tại và quy định repository; viết plan với file ownership, thay đổi dự kiến và checklist kiểm tra cụ thể.
3. Giải quyết quyết định còn thuộc phase đó, như capability matrix Redis hoặc phiên bản server hỗ trợ, trước khi triển khai phần phụ thuộc.
4. Chờ người dùng approve plan rồi mới sửa code; kết thúc bằng changed-file summary, source review và command kiểm tra để người dùng tự chạy.

Việc duyệt tài liệu roadmap này không tự động cho phép triển khai tất cả phase. Bước tiếp theo là review tài liệu, sau đó chọn phase cần lập kế hoạch chi tiết.

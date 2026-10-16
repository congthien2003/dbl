# Phase 0 — Technical Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hoàn thành bộ quyết định có dẫn chứng về Go/MyGo, native UI capabilities, async lifecycle và packaging để người dùng quyết định có thể bắt đầu Phase 1 hay cần điều chỉnh kiến trúc.

**Architecture:** Phase 0 là công việc khảo sát và tài liệu hóa trong repository hiện tại. Kết quả được bổ sung vào mục 4 của roadmap, theo các hợp đồng bằng chứng dùng chung; API và phiên bản chỉ được chốt sau khi đọc nguồn chính thức tương ứng. Khởi tạo ứng dụng và hiện thực UI thuộc Phase 1.

**Tech Stack:** Go `1.27+` và MyGo Native UI theo spec; phiên bản/module cụ thể là đầu ra của Task 1. Nguồn nghiên cứu: repository chính thức, source ở tag/commit cố định, release notes và tài liệu Go/MyGo chính thức.

**Spec:** [docs/specs.md](../../specs.md), mục 2, 3, 5, 8, 10 và 15.17–15.18; [docs/implementation-phases.md](../../implementation-phases.md), mục 1–4. Executor đọc cả hai trước khi thực hiện.

## Global Constraints

Các ràng buộc từ spec được giữ nguyên:

- `Programming language | Go 1.27+`.
- `UI Framework: MyGo Native UI`.
- `Architecture: Modular Monolith`.
- `Primary target: Windows 10/11 x64`.
- `Secondary targets: macOS, Linux`.
- `Prefer CGO-free builds and avoid introducing an ORM.`
- `Pin dependency versions using go.mod and commit go.sum.` — chuẩn bị lựa chọn pin; việc tạo module thuộc Phase 1, commit cần yêu cầu riêng của người dùng.
- `Do not rely on latest dependencies in reproducible builds.`
- `UI components must not execute SQL directly.`
- `Database adapters must not import MyGo UI packages.`
- `All blocking database calls must execute outside the UI rendering path.`
- `Canceled or obsolete query results must never overwrite newer grid state.`
- `Verify MyGo APIs against the pinned framework version before using them.`
- `Record any unavoidable scope deviation before implementation.`

Áp dụng thêm chỉ dẫn trực tiếp của người dùng:

- Lập plan và chờ approve trước khi triển khai. Việc viết plan này chưa cho phép thực thi các task bên dưới.
- Không tự chạy build, test, lint, lệnh verify, cài dependency, scaffold hoặc probe. Read-only research được phép trong execution của Phase 0; command cần thực thi được bàn giao để người dùng chạy.
- `AGENTS.md` tại root cho phép build/verify/test, nhưng chỉ dẫn trực tiếp cấm tự chạy của người dùng được ưu tiên. Không dùng quyền cho phép trong file để suy ra quyền chạy lệnh.
- Cuối phase chờ người dùng kiểm tra và nghiệm thu. Không commit/push nếu không có yêu cầu rõ ràng.
- Không thay đổi `docs/specs.md`, `AGENTS.md`, runtime/application code hoặc dependency files trong Phase 0. Nếu bằng chứng yêu cầu đổi spec, ghi đề xuất và chờ duyệt trước khi đổi.

## Review Focus

1. **Nhầm sản phẩm hoặc native mode:** dự án mang tên MyGo nhưng dùng WebView/web server không được coi là đáp ứng Native UI. Task 1, bước 5 kiểm tra provenance; Task 2, bước 4 kiểm tra rendering path.
2. **Toolchain không đáp ứng spec:** release chưa có hoặc selected tag yêu cầu Go/CGO khác dự kiến phải được báo thành incompatibility; không tự hạ `1.27+`. Task 1, bước 5 đối chiếu release/module requirements.
3. **UI đã đóng hoặc request đã lỗi thời:** kết quả background không được sửa tab mới/đã đóng; cancellation không đủ nếu thiếu stale-result guard. Task 3, bước 4 kiểm tra các scenario riêng biệt.
4. **Grid/editor chỉ có demo đơn giản:** render toàn bộ cells, mất typed values hoặc không có input/highlighting không chứng minh được capability bắt buộc. Task 2, bước 4 kiểm tra API và giới hạn từng capability.
5. **Executable phụ thuộc tài nguyên ngoài dự kiến:** CGO, runtime libraries, CLI assets hoặc credential backend không có trên máy đích phải được ghi vào packaging matrix. Task 4, bước 4 kiểm tra artifact/runtime contract.

Đây là plan tài liệu hóa; kiểm tra của task là đối chiếu nguồn và review các scenario. Không thêm unit tests chỉ để kiểm tra nội dung Markdown. Runtime probes không thuộc execution mặc định; cần phạm vi được duyệt riêng.

---

## File Structure và ownership

| File | Hành động | Trách nhiệm |
| --- | --- | --- |
| `docs/superpowers/plans/2026-10-16-phase-0-technical-foundation.md` | Tạo khi viết plan; cập nhật checkbox khi thực thi | Trình tự task, constraints, handoff và trạng thái thực hiện |
| `docs/implementation-phases.md`, mục `4. Phase 0 — Chốt nền tảng kỹ thuật` | Modify khi plan được duyệt và thực thi | Kết quả nghiên cứu, quyết định, capability matrices, commands và phase gate |

Mọi task ghi nối tiếp vào cùng mục Phase 0; không thực thi các task ghi tài liệu này song song. Không tạo thêm report/ADR riêng để tránh lặp thông tin. Giữ phần mục tiêu/phạm vi hiện có và thêm năm subsection trước mục 5 của roadmap:

| Subsection mới | Task sở hữu | Nội dung bàn giao |
| --- | --- | --- |
| `### P0-A. Framework và toolchain` | 1 | Framework identity, selected version, evidence records và toolchain compatibility |
| `### P0-B. Native UI capability matrix` | 2 | Mapping requirement → API → source → giới hạn → quyết định |
| `### P0-C. Async operation lifecycle` | 3 | UI dispatch và operation/state/resource lifecycle |
| `### P0-D. Packaging và kiểm tra thủ công` | 4 | Platform matrix, runtime dependencies và commands cho người dùng |
| `### P0-E. Kết luận và gate sang Phase 1` | 5 | Go/no-go proposal, deviations và trạng thái nghiệm thu |

### Hợp đồng bằng chứng dùng chung

Mỗi source record gồm `ID`, URL chính thức, tag/commit hoặc version của tài liệu, path/symbol khi là source code, ngày đọc và claim được hỗ trợ. Dùng ID `E01`, `E02`, ... tăng dần trong `P0-A`; task sau thêm nguồn vào cùng registry và tham chiếu ID tương ứng. Ưu tiên link source cố định theo commit/tag; không dùng branch mặc định để chứng minh API của version đã chọn.

Mỗi kết luận có một trong ba trạng thái:

- `Supported by source`: source của version đã chọn cho thấy API/cơ chế đáp ứng yêu cầu; chưa khẳng định runtime đã chạy.
- `Requires probe`: tài liệu/source chưa đủ để kết luận một hành vi cụ thể; ghi câu hỏi, phạm vi probe và expected observation.
- `Unsupported`: có bằng chứng thiếu hoặc không tương thích; ghi ảnh hưởng và đề xuất cần người dùng quyết định.

Không tìm được bằng chứng thì dùng `Requires probe`, không suy ra `Unsupported`. Phần runtime evidence chỉ ghi `Not run` hoặc `User reported` cùng command, môi trường và output/observation người dùng cung cấp. Bản thân việc có sample code không phải benchmark hay bằng chứng đã chạy trên Windows.

## Task 1: Chốt framework identity và toolchain compatibility

**Files:**

- Modify: `docs/implementation-phases.md`, thêm `P0-A` trong mục 4.
- Test/review: evidence registry và compatibility table trong `P0-A`; không tạo test code.

**Interfaces:**

- Consumes: spec mục 2, 15.17; Phase 0 trong roadmap; chỉ dẫn repository/người dùng.
- Produces: `P0-A` với framework identity `{officialRepository, modulePath, selectedTagOrCommit, moduleVersion, sourceDate}`, evidence registry và compatibility table `{specRequirement, upstreamRequirement, availableRelease, localEvidence, conclusion}`. Các giá trị được lấy từ nguồn; không dùng tên module tự suy đoán.
- Handoff: Task 2–4 chỉ kết luận API dựa trên `selectedTagOrCommit` này. Nếu đổi version, phải cập nhật lại mọi kết luận phụ thuộc.

- [x] **Step 1: Ghi baseline và tiêu chí chọn framework.** Trong `P0-A`, ghi yêu cầu Go `1.27+`, MyGo Native UI, Windows 10/11 x64 và CGO-free là preference. Xác nhận repository chưa có application module dựa trên read-only file inventory khi execution bắt đầu.
- [x] **Step 2: Xác định đúng upstream.** Tra cứu dự án MyGo chính thức, đối chiếu README/documentation với repository owner, module declaration và native renderer. Nếu có nhiều dự án không phân biệt được hoặc không có candidate đáp ứng, ghi evidence và dừng task phụ thuộc để hỏi người dùng chọn upstream.
- [x] **Step 3: Chọn một version tái lập được.** Ưu tiên stable release tag đáp ứng native capabilities; nếu chỉ có commit phù hợp, ghi lý do và rủi ro cùng chính xác commit/module version. Không chọn `latest` và không cài dependency để tra cứu.
- [x] **Step 4: Lập compatibility table.** Đọc `go.mod`, build tags, release docs của upstream và Go release/download documentation chính thức. Ghi Go minimum/toolchain, OS/architecture, CGO/native dependencies; phân biệt upstream requirement với local toolchain chưa biết. Chưa có output local thì ghi `Not run`.
- [x] **Step 5: Kiểm tra provenance và version.** Đối chiếu mọi link với đúng tag/commit; xác nhận rendering mode không dùng WebView/HTTP server. Đối chiếu `1.27+` với release có thể dùng; nếu không đáp ứng, ghi incompatibility và đề xuất thay đổi, không sửa version floor trong spec. Expected: chỉ một upstream/version được chọn, hoặc blocker rõ ràng; mọi claim có source record.

**Điều kiện chuyển task:** chỉ bắt đầu Task 2–4 khi xác định được upstream/version để khảo sát. Có thể tiếp tục nghiên cứu candidate đang có incompatibility để lập đủ quyết định, nhưng không ghi nhận sẵn sàng Phase 1 khi incompatibility chưa được duyệt xử lý.

## Task 2: Chốt Native UI capability matrix

**Files:**

- Modify: `docs/implementation-phases.md`, thêm `P0-B`; thêm evidence records vào `P0-A` khi cần.
- Test/review: các dòng capability trong `P0-B` theo checklist bước 4.

**Interfaces:**

- Consumes: upstream/version và evidence schema từ Task 1; spec mục 3, FR-004, FR-005, FR-007, FR-009.
- Produces: matrix `{requirement, upstreamSymbolAndSignature, sourceID, implementationApproach, limitations, status, owningPhase}`. Signature là API thực từ source; capability ghép từ nhiều primitives phải liệt kê đủ primitives thay vì bịa tên widget.

- [x] **Step 1: Tạo đầy đủ các dòng requirement.** Bao gồm window/close hook; resizable sidebar; workspace tabs/focus; grid virtualization theo visible rows/cells; scroll hai chiều; column resize; cell/row selection; clipboard; editor input/focus/keyboard; theme/system-theme notification; local file dialogs; SQL text editing/highlighting; line numbers khi hỗ trợ. Gắn `owningPhase` là 1, 2.2, 2.3 hoặc 2.4 theo roadmap.
- [x] **Step 2: Map mỗi dòng sang source/API.** Đọc exported symbols, signatures, renderer implementation và examples của version được chọn. Ghi cách dùng đủ rõ để Phase 1 không cần đoán API cho window/tabs/grid/theme; không viết application code trong phase này.
- [x] **Step 3: Chốt khoảng trống theo từng dòng.** Phân biệt widget có sẵn với phần phải ghép từ native primitives. Với grid virtualization/cell editor/highlighting thiếu primitive cần thiết, ghi `Requires probe` hoặc `Unsupported` kèm bằng chứng và tác động. Không tự thay stack hoặc bỏ chức năng bắt buộc; line numbers giữ đúng điều kiện “where supported” của spec.
- [x] **Step 4: Review các scenario bắt buộc.** Kiểm tra source path cho visible-range rendering thay vì toàn bộ dataset; editor Enter chỉ xác nhận draft, Escape hủy input, F2/double-click giữ focus đúng; display truncation không thay underlying typed value; đổi theme không tái tạo workspace mất state; native renderer không dựa vào browser. Expected: từng scenario có API/cơ chế cụ thể hoặc khoảng trống được đánh dấu rõ, không gắn runtime `PASS` khi chưa chạy.

**Điều kiện hoàn thành:** mọi dòng matrix có bằng chứng hoặc câu hỏi probe cụ thể, giới hạn và phase sở hữu; không có dòng chỉ ghi “framework hỗ trợ” mà thiếu symbol/source.

## Task 3: Thiết kế async operation lifecycle theo MyGo API thực

**Files:**

- Modify: `docs/implementation-phases.md`, thêm `P0-C`; bổ sung nguồn vào `P0-A`.
- Test/review: scenario table trong `P0-C`.

**Interfaces:**

- Consumes: framework version từ Task 1, window/tab lifecycle từ Task 2; spec mục 5.1, 7.1, 8.2, 8.5 và 9.
- Produces: operation protocol gồm `{operationID, tabID, connectionID, requestGeneration, context, cancel}`; state transitions `idle → loading → success/error/canceled`; completion gate và bảng cleanup ownership. Đây là tên trường/hợp đồng thiết kế, chưa tạo Go structs hoặc interfaces.
- Handoff: Phase 1 dùng protocol để thiết kế application state; Phase 2.1–2.4 áp dụng cho connect, metadata, fetch, save và SQL execution.

- [x] **Step 1: Xác định UI dispatch API.** Ghi chính xác package/symbol/signature, thread-affinity requirements và điều kiện gọi sau khi window đóng của upstream; trích source của selected version. Nếu không có dispatch an toàn đã xác định, đánh dấu blocker cho Phase 1.
- [x] **Step 2: Mô tả một operation từ lúc bắt đầu đến kết thúc.** Application service tạo context và generation; worker chỉ thao tác I/O; completion được dispatch về UI. Trên UI thread, chỉ apply nếu context còn hợp lệ, tab/connection còn đúng và generation khớp request hiện tại. Bump generation và cancel request trước khi thay thế operation; không coi cancel là bằng chứng completion sẽ không đến.
- [x] **Step 3: Chốt resource ownership.** Bảng riêng cho connect attempt, metadata/page fetch, SQL rows/cursor và save transaction: ai cancel, ai close/rollback, khi nào release, state nào được giữ. Closing tab/connection/app phải vô hiệu completion và cleanup; save không được tự retry hay xóa pending edits khi kết quả chưa xác định.
- [x] **Step 4: Review scenario table.** Ghi trigger/expected state/source-or-design cho sáu trường hợp: A hoàn thành sau B; tab đóng trước completion; đổi connection khi đang fetch; cancel nhưng driver trả success muộn; timeout/error trong khi có cached data; mất mạng trong Save. Expected: không stale update, không mutate UI từ worker, không mất edits, không auto-retry và cleanup có owner. Runtime evidence vẫn `Not run` nếu người dùng chưa thử.

**Điều kiện hoàn thành:** protocol xác định rõ nơi chạy I/O, nơi cập nhật UI, stale-result guard và resource cleanup; bất kỳ API threading chưa đủ bằng chứng đều được đánh dấu và chặn kết luận tương ứng.

## Task 4: Chốt packaging matrix và bàn giao kiểm tra thủ công

**Files:**

- Modify: `docs/implementation-phases.md`, thêm `P0-D`; bổ sung nguồn vào `P0-A`.
- Test/review: platform/runtime matrix và command runbook trong `P0-D`.

**Interfaces:**

- Consumes: selected version/toolchain từ Task 1, capabilities từ Task 2, async risks từ Task 3; spec mục 1.2, 2, 8, 10 và 13.
- Produces: platform matrix `{target, buildHost, toolchain, CGO, nativeLibraries, runtimeAssets, packagingCommand, credentialBackendOrFallback, evidence, verificationStatus}` và user runbook `{purpose, prerequisites, workingDirectory, command, expectedObservation, sideEffects}`.

- [x] **Step 1: Lập platform matrix.** Windows 10/11 x64 là mandatory row; macOS/Linux là secondary rows theo hỗ trợ upstream. Xác định host/cross-compile restrictions, CGO/native libraries/assets và cách phân phối thực tế từ source/version đã chọn. Không mặc định single executable hoặc cross-compile được.
- [x] **Step 2: Đánh giá dependency phù hợp.** Đối chiếu driver packages đã chỉ định trong spec với toolchain và packaging approach, không tải/cài dependency. Với OS credential store, ghi backend candidate có nguồn cho primary platform hoặc fallback hỏi password mỗi session; không dùng plaintext file để bù khả năng thiếu. Secondary platform chưa có bằng chứng không được đánh dấu hỗ trợ đã xác nhận.
- [x] **Step 3: Viết command runbook cho người dùng.** Commands chỉ có tác dụng kiểm tra toolchain có thể ghi ngay như dưới đây; ghi chạy từ repo root, PowerShell, cần Go trong PATH, không tạo project files. Expected: `go version` đáp ứng `1.27+`, tương thích selected MyGo; `go env` cho biết host/toolchain/CGO thực tế. Nếu không đáp ứng, ghi incompatibility chứ không sửa môi trường người dùng.

  ```powershell
  go version
  go env GOVERSION GOOS GOARCH CGO_ENABLED GOTOOLCHAIN
  ```

  Chép chính xác command packaging từ tài liệu selected version vào runbook, kèm CLI version/prerequisites, build host, output files và working directory của app Phase 1. Label command này là dùng sau khi Phase 1 đã có module; không yêu cầu chạy ở repo hiện tại. Không đưa `go test ./...` hoặc MyGo CLI tự suy đoán vào checklist Phase 0.

- [x] **Step 4: Review artifact/runtime contract.** Đối chiếu từng row với build instructions và runtime loader/resources source: executable có cần DLL/assets/helper process không, có browser/server dependency không, secure storage unavailable thì fallback gì. Expected: mỗi dependency được nêu hoặc đánh dấu cần bằng chứng; không hứa hoạt động trên máy sạch chỉ vì có build command.
- [x] **Step 5: Tách các probe còn cần thành đề xuất cụ thể.** Chỉ tạo proposal cho câu hỏi còn thiếu bằng chứng: tên câu hỏi, scenario nhỏ nhất, file paths nếu cần tạo, command chính xác khi đã xác định được, kết quả quan sát và phạm vi thay đổi. Probe cần code/dependency hoặc chưa thể viết command chính xác thì để ở trạng thái chờ plan riêng; không scaffold/cài/chạy trong Phase 0. Ghi output người dùng cung cấp đúng là `User reported`, không suy rộng platform/performance chưa được đo.

**Điều kiện hoàn thành:** có route packaging có nguồn cho Windows hoặc blocker rõ ràng; mọi command có điều kiện chạy và side effects, mọi runtime claim phân biệt với source evidence. Chưa có runtime evidence không được diễn đạt như đã build/chạy thành công.

## Task 5: Tổng hợp quyết định và gate sang Phase 1

**Files:**

- Modify: `docs/implementation-phases.md`, thêm `P0-E` và cập nhật câu trạng thái của Phase 0; không sửa roadmap các phase khác.
- Modify: `docs/superpowers/plans/2026-10-16-phase-0-technical-foundation.md`, checkbox/task status phản ánh công việc thực sự đã làm.
- Test/review: coverage table và exit checklist trong `P0-E`.

**Interfaces:**

- Consumes: `P0-A`–`P0-D` cùng evidence registry, capability statuses và user-reported observations.
- Produces: kết luận Phase 0 gồm selected framework/toolchain, implementation approach, unresolved risks, deviations cần duyệt, verification status và đề xuất `Ready for review` hoặc `Blocked` kèm lý do. Đây là trạng thái tài liệu cục bộ, không thao tác goal hoặc tự approve Phase 1.

- [x] **Step 1: Viết decision summary.** Liệt kê module/version/toolchain được đề xuất, UI primitives chính, async protocol và Windows packaging route. Dẫn tới evidence/capability IDs; không lặp toàn bộ source registry.
- [x] **Step 2: Lập coverage table cho năm yêu cầu Phase 0.** Map framework/toolchain → Task 1; UI APIs → Task 2; async/thread safety → Task 3; platforms/CGO/packaging → Task 4; evidence/deviations/user checks → Task 1–5. Mỗi dòng có kết luận và nguồn, không chỉ đánh dấu task đã viết xong.
- [x] **Step 3: Phân loại phần chưa giải quyết.** `Unsupported` ảnh hưởng yêu cầu bắt buộc hoặc version floor là blocker cho Phase 1. `Requires probe` ảnh hưởng native rendering, UI dispatch, virtualization hoặc packaging phải có proposal và quyết định người dùng trước khi dùng làm nền tảng; không mặc nhiên pass. Thiếu runtime evidence nhưng source đủ rõ phải ghi giới hạn và chờ người dùng nghiệm thu.
- [x] **Step 4: Tự review plan/spec inline.** Đối chiếu đúng selected version ở mọi task, năm Review Focus có scenario sở hữu, không signature/CLI đoán, không blanket claim cross-platform/CGO-free/60 FPS/startup dưới 1 giây. Phân biệt target hiệu năng với benchmark; không thêm automated checks hoặc tự chạy verify để review Markdown.
- [x] **Step 5: Bàn giao người dùng.** Link mục Phase 0 đã cập nhật, nêu quyết định, blockers, command runbook và bằng chứng chưa có. Dừng chờ người dùng kiểm tra/duyệt; không tự khởi tạo Phase 1 hoặc commit. Phase 1 cần plan theo file riêng và approve tiếp theo.

**Exit checklist:**

- [x] Upstream và version tái lập được đã xác định, toolchain compatibility có nguồn.
- [x] Toàn bộ native capability bắt buộc có phương án/API hoặc blocker rõ ràng.
- [x] Async protocol bảo vệ UI affinity, stale results, cancellation và cleanup.
- [x] Windows packaging/dependencies và điều kiện secondary targets có nguồn.
- [x] Commands/probes được bàn giao đúng phạm vi, không ghi runtime pass khi chưa chạy.
- [ ] Deviations/blockers đã được trình bày; người dùng nghiệm thu trước khi chuyển phase.

## Execution Handoff

Đề xuất **Native** vì năm task cùng cập nhật một phần tài liệu, phụ thuộc tuần tự vào framework/version và cần giữ kết luận nhất quán. Với cách này, agent chính thực hiện lần lượt rồi có một reviewer độc lập ở cuối theo workflow đã chọn. Task self-review khi viết plan được làm inline; không dispatch subagent để tạo plan này.

Lựa chọn khác là **Subagent-driven**: từng task có implementer/reviewer riêng, context và chi phí cao hơn; task ghi chung file vẫn phải thực hiện nối tiếp.

Chờ người dùng review plan và chọn cách thực thi trước khi bắt đầu Task 1. Các phương pháp execution không thay đổi giới hạn command, probe, commit và gate kết thúc phase của người dùng.

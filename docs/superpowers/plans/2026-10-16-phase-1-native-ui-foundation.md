# Phase 1 — Native UI Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Khởi tạo project Go/MyGo `v0.5.5` và dựng shell Native UI chạy trên dữ liệu giả lập: cửa sổ, sidebar kéo được, cây connection giả, workspace tabs, grid ảo hóa, editor ô cục bộ, Light/Dark/System và `settings.json`.

**Architecture:** Modular monolith một process. `internal/ui` chỉ vẽ và gửi action; `internal/app` giữ state thuần (workspace/tab, settings saver) không import package UI của MyGo; `internal/grid` giữ giá trị typed, hiển thị và draft; `internal/mock` sinh dữ liệu giả xác định theo chỉ số; `internal/storage` đọc/ghi `settings.json`. Không có adapter database, không có I/O mạng.

**Tech Stack:** Go (toolchain ≥ `1.27.1`), `github.com/egoist/mygo` `v0.5.5` (commit `4d8673cd2658a2c6e86b26c44751e57bf8174f91`), kiểm thử view bằng `ui.NewTester` (không cần cửa sổ).

**Spec:** [docs/specs.md](../../specs.md), mục 3, 5, 8.1–8.3, 11.1, 12.3, FR-004/FR-005 (phần UI cục bộ), FR-009, roadmap Phase 1 (mục 13); [docs/implementation-phases.md](../../implementation-phases.md), mục 3, 5 và 4 (`P0-A`…`P0-E`, đặc biệt bảng `B01`–`B22`, scenario `P0-C`, runbook `P0-D`). Executor đọc cả hai trước khi làm.

## Global Constraints

Giữ nguyên từ spec:

- `Programming language | Go 1.27+`; `UI Framework: MyGo Native UI`; `Architecture: Modular Monolith`.
- `Primary target: Windows 10/11 x64`; `Secondary targets: macOS, Linux`.
- `Prefer CGO-free builds and avoid introducing an ORM.`
- `Pin dependency versions using go.mod and commit go.sum.` / `Do not rely on latest dependencies in reproducible builds.`
- `UI components must not execute SQL directly.` / `Database adapters must not import MyGo UI packages.`
- `Verify MyGo APIs against the pinned framework version before using them.` (đối chiếu với `github.com/egoist/mygo@v0.5.5`, các evidence `E10`–`E30` trong roadmap).
- `Do not use WebView, web server or another UI stack.` (roadmap mục 3).
- Settings: `~/.dbl/settings.json`, có `version`, `theme` (`system|light|dark`), `window.width`, `window.height`, `window.sidebarWidth` (spec 11.1); `Configuration writes should be atomic where feasible.`
- Startup: `Do not initialize database connections at application launch.` Chỉ đọc settings/theme/trạng thái cửa sổ.
- UI: explorer resizable; mỗi tab giữ state riêng; mở lại table đã mở thì focus tab cũ; đóng tab có dữ liệu chưa lưu phải hỏi; Double-click/F2 mở editor ô; Enter xác nhận edit cục bộ; Escape hủy giá trị đang sửa; ô đã sửa được highlight; `Never silently write changes when a user merely leaves a cell` (Phase 1 không ghi database).
- Theme: `All components must use shared semantic colors.`; ô sửa, lỗi, focus và disabled phải phân biệt được ở cả hai theme; đổi theme không reset workspace.
- Grid: `Render only visible cells`; NULL, chuỗi rỗng, số, bool, datetime, text, binary, JSON được phân biệt; giá trị lớn chỉ cắt khi hiển thị; binary hiển thị placeholder.

Chỉ dẫn trực tiếp của người dùng (ưu tiên hơn skill mặc định):

- **Agent không tự chạy** `go build`, `go test`, `go vet`, `go tool mygo …`, `go get`, `go mod tidy`, `mygo init`, lint hay bất kỳ lệnh verify/cài đặt nào. Các bước "Run" bên dưới là **lệnh người dùng chạy** (PowerShell, từ root repo) rồi báo output; executor viết code/test rồi dừng chờ. Nếu người dùng cho phép rõ ràng chạy lệnh cụ thể thì được chạy đúng lệnh đó.
- **Không commit, không push.** Không có bước commit trong plan này; cuối phase chờ người dùng kiểm tra, test thử app rồi mới tự quyết định commit (`AGENTS.md`).
- Không sửa `docs/specs.md`, `AGENTS.md`. Chỉ sửa `docs/implementation-phases.md` khi nghiệm thu (đánh dấu/ghi kết quả probe), nếu người dùng yêu cầu.
- Phát hiện API MyGo khác với roadmap `P0-B`/`P0-C`: dừng task, ghi lại, hỏi người dùng; không tự đổi stack hay bỏ chức năng.

## Quyết định cần người dùng duyệt (các lựa chọn của plan này)

1. **Tên ứng dụng `SBDb`** (người dùng chốt) dùng cho tiêu đề cửa sổ, `mygo.json` và UI. **Module path giữ `dbl`** (tên repo/spec, chưa có repo URL) và thư mục dữ liệu giữ `~/.dbl/` đúng spec; hai điểm này không đổi theo tên app trừ khi bạn yêu cầu. Identifier do `init` sinh từ tên (`com.example.sbdb`); identifier là khóa của credential store và single-instance nên cần đổi trước khi phát hành (Phase 5). `docs/specs.md` vẫn gọi sản phẩm là "DBL" và không bị sửa.
2. **Toolchain:** Go cục bộ hiện là `go1.26.5` (`User reported`). Người dùng sẽ tự cập nhật Go lên `1.27.2` (đã xác nhận) trước Task 1; Task 1 Step 1 kiểm tra bằng `go version`.
3. **Probe PR1–PR4 của roadmap `P0-D` được gộp vào Phase 1** (Task 7–9 và bước kiểm tra thủ công cuối): cột nhiều, chọn ô, editor ô, đổi theme giữ state. PR5 (kết quả đến muộn sau khi đóng tab) thuộc Phase 2.1 vì cần async thật; PR6 (máy Windows sạch) và PR7 (highlight dialect) do người dùng/Phase 2.4 quyết.
4. **Blur:** nhấp sang ô khác khi đang sửa = xác nhận edit cục bộ (như Enter); không ghi gì ra database. Đây là lựa chọn của plan (spec chỉ nói Enter/Escape).
5. **Copy ô NULL** ghi chuỗi rỗng; binary copy chuỗi placeholder; copy luôn lấy giá trị đầy đủ, không phải chuỗi đã cắt.
6. **`settings.json` hỏng** được đổi tên thành `settings.json.bad` rồi chạy với mặc định; **version mới hơn** (`> 1`) không bị ghi đè (app chạy mặc định, không lưu).
7. **Không dùng `WindowOptions.StateKey`** (tránh `window-state.json` thứ hai ở `%AppData%`); kích thước cửa sổ lưu trong `settings.json`.
8. **Phase 1 không có nút Save changes, không có filter/paging, không có SQL execution, không có Ctrl+S/F5**; chỉ có Revert cục bộ. Không gán `Ctrl+Enter` cho editor (Phase 2.4 sẽ nhường phím theo `B19`).

## Review Focus

Năm tình huống spec ngầm yêu cầu nhưng người đọc spec dễ bỏ sót, theo khả năng gặp nhất; mỗi dòng có test ở task sở hữu:

1. **`settings.json` thiếu/hỏng/phiên bản mới/giá trị vô lý** (kích thước 0, sidebar 99999, theme lạ): app vẫn mở với giá trị hợp lệ, không mất file hỏng, không ghi đè file của phiên bản mới (Task 2).
2. **Mở trùng, đóng tab đang active, đóng tab cuối:** không tạo tab trùng, tab lân cận được chọn, workspace rỗng hiển thị trạng thái rỗng và không panic (Task 5, Task 6).
3. **Giá trị lạ trong ô:** NULL ≠ chuỗi rỗng, văn bản nhiều dòng/Unicode/rất dài chỉ cắt khi hiển thị và copy trả đủ giá trị, binary không bị render thô (Task 4, Task 7).
4. **Sửa rồi quay về giá trị gốc, Escape, ô không sửa được:** quay về gốc xóa dirty; Escape không để lại draft; PK/binary/view bỏ qua F2 (Task 8).
5. **Đóng tab/cửa sổ khi có edit chưa lưu, và đổi theme giữa chừng:** luôn hỏi trước khi mất draft; đổi theme không làm mất tab, vùng chọn hay edit đang mở (Task 6, Task 8, Task 10).

---

## File Structure và ownership

| File | Hành động | Trách nhiệm |
| --- | --- | --- |
| `go.mod`, `go.sum`, `mygo.json`, `resources/icon.png`, `.gitignore` | Tạo (Task 1, từ output `init` do người dùng chạy) | Module `dbl`, pin `github.com/egoist/mygo v0.5.5`, `tool` directive của CLI, cấu hình app |
| `main.go`, `main_test.go` | Tạo (Task 1), mở rộng (Task 6, 10) | Wiring: settings → theme → window; smoke test |
| `internal/storage/config.go`, `config_test.go` | Tạo (Task 2) | `Settings`, `SettingsStore` (đọc/ghi atomic, version, backup) |
| `internal/app/saver.go`, `saver_test.go` | Tạo (Task 2) | Debounce lưu settings, `Flush` |
| `internal/ui/theme.go`, `theme_test.go` | Tạo (Task 3) | `Palette` semantic, map theme preference ↔ `mygo.ThemeSource` |
| `internal/database/models.go` | Tạo (Task 4) | `TableRef` theo spec 6.2 (chỉ kiểu này) |
| `internal/grid/value.go`, `draft.go`, tests | Tạo (Task 4) | `Value`, `Kind`, hiển thị/cắt/copy, `Drafts` |
| `internal/mock/dataset.go`, `catalog.go`, tests | Tạo (Task 4) | Dataset giả O(1) theo chỉ số; cây connection giả |
| `internal/app/workspace.go`, `state.go`, tests | Tạo (Task 5) | `Workspace`, `Tab`, `State` (settings, theme, dirty, quit) |
| `internal/ui/{window,explorer,workspace,tabs}.go` + tests | Tạo (Task 6) | Shell: title/theme, Split, Outline, tab strip, status bar |
| `internal/ui/grid.go` + test | Tạo (Task 7), mở rộng (Task 8) | Grid ảo hóa, chọn ô, copy; editor ô |
| `internal/ui/sql_editor.go` + test | Tạo (Task 9) | Tab SQL với `editor.State` riêng, Ctrl+N |

Task phụ thuộc tuần tự (Task 1 → 10); Task 2, 3, 4 độc lập nhau sau Task 1 nhưng thực hiện theo thứ tự để dễ review.

---

## Task 1: Khởi tạo project (người dùng chạy lệnh)

**Files:**

- Create: `go.mod`, `go.sum`, `mygo.json`, `resources/icon.png`, `.gitignore`, `main.go`, `main_test.go`

**Interfaces:**

- Produces: module `dbl` yêu cầu `github.com/egoist/mygo v0.5.5`; `main.go` có `type shell struct{}` tối thiểu với `func (s *shell) view(c *ui.Context)` (được thay ở Task 6).

- [ ] **Step 1: Người dùng chuẩn bị toolchain.** Cài Go `1.27.2` (khuyến nghị) hoặc chấp nhận tự tải `go1.27.1`. Chạy `go version` rồi báo kết quả. Expected: `go1.27.1` trở lên, hoặc `go1.26.5` kèm xác nhận cho `GOTOOLCHAIN=auto` tải toolchain.
- [ ] **Step 2: Người dùng chạy `init` ngoài repo** (thư mục phải trống/không tồn tại; `-template` đứng trước dir; `E34`). PowerShell:

  ```powershell
  $tmp = Join-Path $env:TEMP 'dbl-init'
  go run github.com/egoist/mygo/cmd/mygo@v0.5.5 init -template native -name SBDb -module dbl $tmp
  ```

  Side effects: tải module từ proxy, tạo `$tmp`. Expected: in "Created SBDb". Lệnh `init` tự gọi `go get … @latest` và `go mod tidy` (`E34`), nên bước sau là bắt buộc.
- [ ] **Step 3: Người dùng ghim lại đúng version** (chạy trong `$tmp`):

  ```powershell
  cd $tmp
  go get github.com/egoist/mygo@v0.5.5
  go get -tool github.com/egoist/mygo/cmd/mygo@v0.5.5
  go mod tidy
  go list -m github.com/egoist/mygo
  ```

  Expected: dòng cuối in `github.com/egoist/mygo v0.5.5`; `go.mod` có `go 1.27.1` hoặc cao hơn và dòng `tool github.com/egoist/mygo/cmd/mygo`. Nếu khác, báo output cho executor, không sửa tay `go.sum`.
- [ ] **Step 4: Executor sao chép** `go.mod`, `go.sum`, `mygo.json`, `resources/icon.png` từ `$tmp` vào root repo (không sao chép `main.go`/`main_test.go` của template) và tạo `.gitignore` gồm `/build/`, `/.mygo/`, `mygo_windows_*.syso` (nội dung của template). Giữ `mygo.json` là `{name:"SBDb", identifier, version:"0.1.0", out:"build"}`; kiểm tra module trong `go.mod` là `dbl`.
- [ ] **Step 5: Smoke test.** Viết `main_test.go` với `TestViewShowsTitle`: `tt := ui.NewTester((&shell{}).view, 480, 320)` và `tt.HasText("SBDb")` là true. Viết `main.go` gồm `shell.view` (một `ui.Text` "SBDb") và `main()` theo mẫu: `mygo.App.WhenReady(func(){ mygo.NewWindow(mygo.WindowOptions{Title:"SBDb", Width:1280, Height:800, MinWidth:800, MinHeight:500, Content: ui.View(s.view)}) })` rồi `mygo.App.Run()` (log lỗi nghiêm trọng bằng `log.Fatal`). Không dùng `StateKey`.
- [ ] **Step 6: Người dùng chạy** `go vet ./...` và `go test ./...` từ root. Expected: không lỗi; `TestViewShowsTitle` PASS. Tùy chọn: `go tool mygo dev` để thấy cửa sổ "SBDb" mở (cần GPU/WARP; ghi lại nếu cửa sổ không mở).

**Điều kiện hoàn thành:** `go.mod` ghim `github.com/egoist/mygo v0.5.5` và có `tool` directive; `go.sum` có trong repo; test smoke PASS; không thêm dependency ngoài MyGo và các phụ thuộc gián tiếp của nó.

---

## Task 2: Settings — đọc/ghi `settings.json` và debounce lưu

**Files:**

- Create: `internal/storage/config.go`, `internal/storage/config_test.go`, `internal/app/saver.go`, `internal/app/saver_test.go`

**Interfaces:**

- Produces (package `storage`):

  ```go
  const SettingsVersion = 1
  type ThemePref string // "system" | "light" | "dark"
  const (ThemeSystem ThemePref = "system"; ThemeLight = "light"; ThemeDark = "dark")
  type WindowSettings struct{ Width, Height, SidebarWidth int } // json: width, height, sidebarWidth
  type Settings struct {
      Version int          `json:"version"`
      Theme   ThemePref    `json:"theme"`
      Window  WindowSettings `json:"window"`
  }
  const (MinWindowWidth = 800; MinWindowHeight = 500; MaxWindowDim = 16384; MinSidebarWidth = 160; MaxSidebarWidth = 800)
  var ErrCorrupt, ErrUnsupportedVersion error
  func DefaultSettings() Settings            // version 1, system, 1280x800, sidebar 260
  func (s Settings) Normalize() Settings     // điền mặc định cho trường thiếu, kẹp giá trị vào khoảng hợp lệ
  func DefaultDir() (string, error)          // <home>/.dbl
  type SettingsStore struct{ /* dir, readOnly */ }
  func NewSettingsStore(dir string) *SettingsStore
  func (st *SettingsStore) Load() (Settings, error)
  func (st *SettingsStore) Save(s Settings) error
  ```

- Produces (package `app`):

  ```go
  type SettingsWriter interface{ Save(storage.Settings) error }
  type SettingsSaver struct{ /* writer, delay, pending */ }
  func NewSettingsSaver(w SettingsWriter, delay time.Duration, onError func(error)) *SettingsSaver
  func (sv *SettingsSaver) Schedule(s storage.Settings) // gộp nhiều lần gọi, ghi bản cuối sau delay
  func (sv *SettingsSaver) Flush()                      // ghi ngay bản đang chờ (nếu có), đồng bộ
  ```

- [ ] **Step 1: Viết test `internal/storage/config_test.go`** (dùng `t.TempDir()`; `Load` là hàm duy nhất đọc file):
  - `TestLoadMissingFileReturnsDefaults`: dir trống → `DefaultSettings()`, `err == nil`, không tạo file.
  - `TestSaveLoadRoundTrip`: lưu `{Theme: dark, Window{1400, 900, 300}}`, `Load` trả đúng giá trị; file JSON có đúng key `version`, `theme`, `window.width`, `window.height`, `window.sidebarWidth` (spec 11.1).
  - `TestSaveCreatesDirAndLeavesNoTempFiles`: dir con chưa tồn tại → sau `Save` thư mục chỉ chứa `settings.json`.
  - `TestSaveReplacesExistingFile`: lưu hai lần, `Load` thấy giá trị lần hai; không còn file tạm.
  - `TestLoadMissingKeysUsesDefaults`: file `{"version":1}` → `DefaultSettings()`.
  - `TestLoadCorruptFileIsBackedUp`: file `{not json` → trả `DefaultSettings()`, `errors.Is(err, ErrCorrupt)`, `settings.json.bad` có nội dung gốc, `settings.json` không còn; `Save` sau đó thành công.
  - `TestLoadNewerVersionIsNotOverwritten`: file `{"version":99,"theme":"dark"}` → `DefaultSettings()`, `errors.Is(err, ErrUnsupportedVersion)`; `Save` trả `ErrUnsupportedVersion` và nội dung file không đổi.
  - `TestNormalizeClamps`: bảng — width `0`→`MinWindowWidth`, width `100000`→`MaxWindowDim`, height `10`→`MinWindowHeight`, sidebar `5`→`MinSidebarWidth`, sidebar `99999`→`MaxSidebarWidth`, theme `"neon"`→`system`, theme rỗng→`system`.
  - `TestDefaultDirIsHomeDotDbl`: đặt `HOME` và `USERPROFILE` qua `t.Setenv` → `filepath.Join(home, ".dbl")`.
- [ ] **Step 2: Implement `internal/storage/config.go`** theo Interfaces. `Save` ghi file tạm cùng thư mục, `Sync`, rồi `os.Rename` (thay thế file đích; dir `0o700`, file `0o600`); key lạ trong file bị bỏ khi đọc (ghi trong comment). `Load` sau khi parse gọi `Normalize`.
- [ ] **Step 3: Viết test `internal/app/saver_test.go`** với `SettingsWriter` giả có channel:
  - `TestScheduleCoalescesToLastValue`: gọi `Schedule` ba lần với giá trị khác nhau trong khoảng nhỏ hơn `delay` (delay 20 ms) → đúng một lần `Save`, giá trị là lần gọi cuối.
  - `TestFlushWritesPendingImmediately`: `Schedule` rồi `Flush` ngay → `Save` đã được gọi khi `Flush` trả về; không `Save` thêm sau khi `delay` trôi qua.
  - `TestFlushWithNothingPendingDoesNothing`.
  - `TestWriteErrorGoesToCallback`: writer trả lỗi → `onError` nhận lỗi đó; saver vẫn nhận `Schedule` tiếp theo.
- [ ] **Step 4: Implement `internal/app/saver.go`.** Dùng `time.AfterFunc` + mutex; goroutine ghi không chạm UI.
- [ ] **Step 5: Người dùng chạy** `go test ./internal/storage ./internal/app -v`. Expected: tất cả test ở trên PASS (bao gồm `-race` nếu người dùng muốn: `go test -race ./internal/app`).

**Điều kiện hoàn thành:** các test trên PASS; chưa có code nào ngoài hai package gọi `os` cho settings.

---

## Task 3: Theme — palette ngữ nghĩa và ánh xạ preference

**Files:**

- Create: `internal/ui/theme.go`, `internal/ui/theme_test.go`

**Interfaces:**

- Consumes: `storage.ThemePref` (Task 2); `ui.LightTheme()`, `ui.DarkTheme()`, `ui.Color{R,G,B,A}`, `Color.Over` (E18).
- Produces (package `ui` của DBL, đường dẫn `dbl/internal/ui`; importer đặt alias `dblui`):

  ```go
  type Palette struct{ Dirty, Error, Disabled, ActiveCell, Null mygoui.Color }
  func NewPalette(t *mygoui.Theme) Palette           // theo t.Dark; màu mờ được hợp sẵn lên t.Background nên opaque
  func ThemeSource(p storage.ThemePref) mygo.ThemeSource // system→ThemeSystem, light→ThemeLight, dark→ThemeDark
  ```

  (`mygoui` = `github.com/egoist/mygo/ui`.)

- [ ] **Step 1: Viết `theme_test.go`:**
  - `TestPaletteDistinguishableInBothThemes`: với `LightTheme()` và `DarkTheme()`, bốn màu `Dirty, Error, Disabled, ActiveCell` đôi một cách nhau khoảng cách Euclid RGB ≥ 24 (thang 0–255) và mỗi màu cách `t.Background` ≥ 24; tất cả `A == 255`.
  - `TestPaletteDiffersBetweenThemes`: `NewPalette(Light).Dirty != NewPalette(Dark).Dirty`.
  - `TestThemeSourceMapping`: ba giá trị `storage.ThemePref` ánh xạ đúng; giá trị lạ → `mygo.ThemeSystem`.
- [ ] **Step 2: Implement `theme.go`.** Chọn màu từ `Theme` (`Danger`, `Warning`, `Accent`, `SurfaceHover`…) hợp lên `Background`; ngưỡng trong test quyết định có cần chỉnh màu hay không. Không gọi `SetTheme`; palette tính lại mỗi frame từ `c.Theme()`.
- [ ] **Step 3: Người dùng chạy** `go test ./internal/ui -run 'TestPalette|TestThemeSource' -v`. Expected: PASS.

**Điều kiện hoàn thành:** palette dùng được cho Task 7–8 mà không phụ thuộc màu cố định; không import `mygo` root ngoài `ThemeSource`.

---

## Task 4: Giá trị typed, draft và dữ liệu giả

**Files:**

- Create: `internal/database/models.go`, `internal/grid/value.go`, `internal/grid/draft.go`, `internal/mock/dataset.go`, `internal/mock/catalog.go` và test cùng thư mục (`value_test.go`, `draft_test.go`, `dataset_test.go`, `catalog_test.go`)

**Interfaces:**

- Produces:

  ```go
  // package database (spec 6.2)
  type TableRef struct{ Catalog, Schema, Name string }

  // package grid
  type Kind uint8 // KindNull, KindText, KindNumber, KindBool, KindDateTime, KindJSON, KindBinary
  type Value struct{ Kind Kind; Text string; Size int } // Size = số byte của binary
  func (v Value) Display(maxRunes int) string // NULL→"NULL"; text rỗng→"(empty)"; binary→"<binary N bytes>"; ký tự điều khiển (\n,\t) → khoảng trắng; cắt + "…" khi vượt maxRunes
  func (v Value) CopyText() string            // giá trị đầy đủ; NULL→""; binary→placeholder
  func (v Value) Equal(o Value) bool          // so Kind, Text, Size (không so chuỗi hiển thị)
  type CellRef struct{ RowID int64; Col int }
  type Drafts struct{ /* map[CellRef]Value */ }
  func (d *Drafts) Set(ref CellRef, original, v Value) // v == original thì xóa draft
  func (d *Drafts) Get(ref CellRef) (Value, bool)
  func (d *Drafts) IsDirty(ref CellRef) bool
  func (d *Drafts) Len() int
  func (d *Drafts) Revert()

  // package mock
  type Column struct{ Name string; Kind grid.Kind; Width float32; Editable, PrimaryKey bool }
  type Dataset struct{ Name string; Rows int; Columns []Column; ReadOnly bool }
  func NewDataset(name string, rows, cols int, readOnly bool) *Dataset
  func (d *Dataset) RowID(row int) int64            // row+1, ổn định, không phụ thuộc chuỗi hiển thị
  func (d *Dataset) Value(row, col int) grid.Value  // xác định, O(1), không cấp phát theo kích thước dataset
  type Node struct{ ID, Label string; Kind NodeKind; Children []*Node; ConnectionID string; Table database.TableRef }
  type NodeKind uint8 // NodeFolder, NodeConnection, NodeSchema, NodeTable, NodeView
  func Catalog() []*Node                              // cây giả: 2 folder, 3 connection, 1 connection ở root
  func DatasetFor(connectionID string, ref database.TableRef) (*Dataset, bool)
  ```

- [ ] **Step 1: Viết test `grid`:**
  - `TestDisplayDistinguishesNullFromEmpty`: `Value{Kind:KindNull}.Display(20)=="NULL"`, `Value{Kind:KindText}.Display(20)=="(empty)"`, hai giá trị `Equal` là false.
  - `TestDisplayTruncatesOnlyForDisplay`: text 1000 rune → `Display(50)` có độ dài ≤ 50 rune và kết thúc `…`; `CopyText()` bằng đúng chuỗi gốc.
  - `TestDisplayMultilineAndUnicode`: `"a\nb\tc"` hiển thị `"a b c"`; `"Việt Nam 🙂"` không bị cắt giữa rune.
  - `TestBinaryDisplayIsPlaceholder`: `Value{Kind:KindBinary, Size:2048}.Display(10)=="<binary 2048 bytes>"` (không bị cắt) và `CopyText()` giống nhau.
  - `TestDraftsRevertToOriginalClearsDirty`: `Set` giá trị khác → `IsDirty` true, `Len()==1`; `Set` lại đúng giá trị gốc → `IsDirty` false, `Len()==0`.
  - `TestDraftsIdentityIsRowIDNotDisplay`: hai `CellRef` cùng `Col`, `RowID` khác nhau độc lập; `Revert()` làm `Len()==0`.
- [ ] **Step 2: Implement `internal/grid/value.go`, `draft.go`** và `internal/database/models.go` theo Interfaces.
- [ ] **Step 3: Viết test `mock`:**
  - `TestValueIsDeterministicAndCoversKinds`: `NewDataset("orders", 1_000_000, 12, false)`: `Value(r,c)` lặp lại cho cùng `(r,c)`; trong 5000 hàng đầu xuất hiện đủ bảy `Kind`, ít nhất một NULL và một chuỗi rỗng, một text dài > 500 rune; cột 0 là số, `PrimaryKey`, `Editable==false`, không bao giờ NULL.
  - `TestDatasetIsLazy`: `testing.AllocsPerRun` của `Value(999_999, 5)` ≤ 2 (không dựng bảng 1 triệu hàng).
  - `TestColumnsMatchRequestedCount`: `NewDataset("wide", 100, 200, false)` có đúng 200 cột, tên cột duy nhất.
  - `TestReadOnlyDatasetHasNoEditableColumns`.
  - `TestCatalogShape`: có folder "Work" (2 connection) và "Personal" (1), 1 connection ở root; mỗi connection có schema `public` với tables `users` (500 hàng), `orders` (1.000.000 hàng × 12 cột), `wide_events` (100.000 hàng × 200 cột) và view `active_users` (read-only); `DatasetFor` trả đúng kích thước; ID node duy nhất trong toàn cây.
- [ ] **Step 4: Implement `internal/mock/dataset.go`, `catalog.go`.** Giá trị sinh từ hash của `(row, col)`; chọn tỉ lệ NULL (~1/7, trừ cột PK), chuỗi rỗng (~1/11) và text dài (~1/13 ở cột text dài); binary chỉ cần `Size`.
- [ ] **Step 5: Người dùng chạy** `go test ./internal/grid ./internal/mock -v`. Expected: PASS.

**Điều kiện hoàn thành:** `grid` và `mock` không import package UI của MyGo; `database` chỉ có `TableRef`.

---

## Task 5: Workspace và state ứng dụng (thuần Go)

**Files:**

- Create: `internal/app/workspace.go`, `internal/app/state.go`, `internal/app/workspace_test.go`, `internal/app/state_test.go`

**Interfaces:**

- Consumes: `database.TableRef`, `grid.Drafts`, `mock.Dataset`, `storage.Settings` (Task 2–4).
- Produces:

  ```go
  type TabID int
  type TabKind uint8 // TabTable, TabSQL
  type TableKey struct{ ConnectionID string; Ref database.TableRef } // identity so sánh được
  type Tab struct {
      ID TabID; Kind TabKind; Title string
      Table TableKey; Dataset *mock.Dataset; Drafts grid.Drafts
  }
  func (t *Tab) Dirty() bool // Kind==TabTable && Drafts.Len() > 0; SQL luôn false ở Phase 1
  type Workspace struct{ Tabs []*Tab; ActiveID TabID }
  func (w *Workspace) OpenTable(key TableKey, title string, ds *mock.Dataset) (tab *Tab, created bool) // focus tab sẵn có
  func (w *Workspace) NewSQL(connectionID string) *Tab // tiêu đề "Query 1", "Query 2", ...
  func (w *Workspace) Close(id TabID)  // chọn tab lân cận (ưu tiên bên phải, rồi trái); rỗng thì ActiveID = 0
  func (w *Workspace) Select(id TabID)
  func (w *Workspace) Active() *Tab    // nil khi rỗng
  func (w *Workspace) DirtyTabs() []*Tab

  type State struct {
      Settings storage.Settings
      Workspace Workspace
      Notice string // thông báo ngắn ở status bar (vd. settings hỏng)
      // hooks gán bởi main: SetThemeSource func(storage.ThemePref); ScheduleSave func(storage.Settings)
  }
  func (s *State) SetTheme(p storage.ThemePref)
  func (s *State) SetSidebarWidth(w int)  // kẹp theo storage.Min/MaxSidebarWidth rồi ScheduleSave
  func (s *State) SetWindowSize(w, h int) // kẹp rồi ScheduleSave
  func (s *State) RequestClose(id TabID) (closed bool) // dirty → đặt PendingClose = id, trả false
  func (s *State) ConfirmClose() / CancelClose()
  func (s *State) RequestQuit() (allowed bool)         // có tab dirty → PendingQuit = true, trả false
  ```

  (`PendingClose TabID`, `PendingQuit bool` là field công khai để UI đọc.)

- [ ] **Step 1: Viết `workspace_test.go`:**
  - `TestOpenTableFocusesExistingTab`: mở cùng `TableKey` hai lần → `created` lần hai là false, số tab không đổi, `ActiveID` là tab đó; cùng `Ref` nhưng khác `ConnectionID` → tab mới.
  - `TestTabsKeepIndependentDrafts`: sửa draft ở tab A không làm tab B dirty.
  - `TestCloseActivatesNeighbour`: đóng tab active ở giữa → tab bên phải active; đóng tab cuối danh sách → tab bên trái; đóng tab không active giữ nguyên active.
  - `TestCloseLastTabLeavesEmptyWorkspace`: `Active()==nil`, không panic; `Select` ID không tồn tại bị bỏ qua.
  - `TestNewSQLNumbering`: `Query 1`, `Query 2`; đóng `Query 1` rồi tạo tiếp → `Query 3` (số không tái sử dụng).
  - `TestTabIDsAreNotReused`.
- [ ] **Step 2: Viết `state_test.go`:**
  - `TestRequestCloseDirtyTabAsks`: tab dirty → `RequestClose` trả false, `PendingClose == id`, tab còn; `ConfirmClose` đóng; `CancelClose` xóa `PendingClose`, tab còn nguyên draft.
  - `TestRequestCloseCleanTabCloses`.
  - `TestRequestQuitWithDirtyTab`: có tab dirty → `RequestQuit` false và `PendingQuit` true; không còn dirty → true.
  - `TestSetSidebarWidthClampsAndSchedulesSave` và `TestSetWindowSizeClamps`: giá trị ngoài khoảng được kẹp; `ScheduleSave` được gọi với settings mới (hook giả).
  - `TestSetThemeCallsHookAndKeepsWorkspace`: đổi theme gọi hook đúng `ThemePref`, `Workspace` (tab, draft) không đổi.
- [ ] **Step 3: Implement `workspace.go`, `state.go`** theo Interfaces. `State` không import `github.com/egoist/mygo`.
- [ ] **Step 4: Người dùng chạy** `go test ./internal/app -v`. Expected: PASS.

**Điều kiện hoàn thành:** toàn bộ logic tab/dirty/quit kiểm thử được không cần UI; `internal/app` không import `github.com/egoist/mygo` hay `…/ui`.

---

## Task 6: Shell UI — cửa sổ, sidebar, explorer, tabs, status bar

**Files:**

- Create: `internal/ui/window.go`, `explorer.go`, `workspace.go`, `tabs.go`, `shell_test.go`
- Modify: `main.go`, `main_test.go`

**Interfaces:**

- Consumes: `app.State`, `app.Workspace`, `mock.Catalog`, `mock.DatasetFor`, `ThemeSource`/`NewPalette` (Task 3), MyGo: `ui.Split`, `ui.Outline`/`OutlineState[string]`, `ui.TabsBase`, `ui.Segmented`, `ui.AlertDialog` (E12, E13, E23, E25).
- Produces (package `dbl/internal/ui`):

  ```go
  type Shell struct{ /* state *app.State, explorer outline state, per-tab view state map[app.TabID]*tabView */ }
  func NewShell(st *app.State) *Shell
  func (sh *Shell) View(c *mygoui.Context)
  ```

  Bố cục theo spec 3.1: hàng trên có chữ "SBDb" và `Segmented` "System/Light/Dark" ở bên phải; bên trái `Split` với `*float32` = `State.Settings.Window.SidebarWidth`; explorer là `Outline` của `mock.Catalog()`; bên phải là tab strip + nội dung tab đang chọn (placeholder ở task này) hoặc trạng thái rỗng; status bar dưới cùng có "Mock data — no database connected" và `State.Notice`.

- [ ] **Step 1: Viết `shell_test.go`** (dùng `mygoui.NewTester(sh.View, 1280, 800)`; state tạo bằng `app.State` với hook giả):
  - `TestExplorerShowsMockTree`: có text "Work", "Local PG" (mở folder/connection bằng click) và "orders" sau khi mở lần lượt.
  - `TestOpenTableFromExplorerCreatesTabAndRefocuses`: double-click bảng `users` (hai `ClickAt` trong khoảng thời gian double-click, dùng `tt.Advance`) → tab "users" xuất hiện và active; thao tác lần hai không tạo tab thứ hai; mở `orders` tạo tab thứ hai.
  - `TestCloseButtonClosesTabWithoutSelectingIt`: `tt.Click("Close orders")` đóng đúng tab `orders` khi tab `users` đang active; `users` vẫn active.
  - `TestEmptyWorkspaceShowsEmptyState`: không tab nào → có text "No tab open" (hoặc câu tương đương cố định trong code) và không panic; tab strip không hiển thị tab nào.
  - `TestSidebarWidthComesFromSettings`: `SidebarWidth=300` → rect của text "Work" (`tt.Find`) có `X+W <= 300` và rect của text "No tab open" có `X >= 300`; đổi `SidebarWidth=200` rồi `tt.Frame()` → "No tab open" có `X >= 200` và `X < 300`.
  - `TestThemeSwitchKeepsWorkspace`: mở hai tab, `tt.Click("Dark")` → hook theme nhận `dark`; hai tab và tab active không đổi.
  - `TestStatusBarShowsNotice`: `State.Notice="Settings could not be read"` hiện trong status bar.
- [ ] **Step 2: Implement các file UI.** Tab strip dựng bằng `TabsBase` (`E13`): mỗi tab có tiêu đề (cắt `SingleLine`) và nút đóng có `Label("Close "+title)`; nút `+` mở SQL tab (Task 9 hoàn thiện nội dung). Per-tab view state nằm trong `map[app.TabID]*tabView` của `Shell` và bị xóa khi tab không còn trong workspace (kiểm tra mỗi frame). `Outline` trả về double-click/Enter trên node `NodeTable|NodeView` qua `Submitted()` của list nền (`E25`); mở bằng `Workspace.OpenTable`. Nếu API double-click/Submitted của `Outline` ở `v0.5.5` khác tài liệu, ghi vào kết quả và dừng.
- [ ] **Step 3: Cập nhật `main.go`:** đọc settings (`storage.NewSettingsStore(storage.DefaultDir()).Load()`; lỗi `ErrCorrupt`/`ErrUnsupportedVersion` → `State.Notice` bằng tiếng Anh ngắn, vẫn chạy), `mygo.Theme.SetSource(dblui.ThemeSource(settings.Theme))` **trước** `Run`, tạo `SettingsSaver` (delay 400 ms), gán hook `SetThemeSource`/`ScheduleSave`, `WindowOptions{Width, Height từ settings, MinWidth: 800, MinHeight: 500, Content: ui.View(shell.View)}`, `win.OnResize` → `State.SetWindowSize(win.Size())`, `win.OnClosed` và `mygo.App.OnWillQuit` → `saver.Flush()`. Xóa `shell` tạm của Task 1 và cập nhật `main_test.go` thành test gọi `NewShell(...).View`.
- [ ] **Step 4: Người dùng chạy** `go test ./...` và `go vet ./...`. Expected: toàn bộ PASS.
- [ ] **Step 5: Người dùng chạy thử** `go tool mygo dev`: cửa sổ mở, kéo thanh chia sidebar, mở/đóng tab, đổi theme, đóng và mở lại app thấy kích thước/sidebar/theme được giữ; kiểm tra `~\.dbl\settings.json`. Ghi quan sát (`User reported`).

**Điều kiện hoàn thành:** shell chạy trên mock; settings được lưu/khôi phục; không có code nào gọi database; không dùng webview.

---

## Task 7: Grid giả lập ảo hóa — cuộn, cột, chọn, copy

**Files:**

- Create: `internal/ui/grid.go`, `internal/ui/grid_test.go`
- Modify: `internal/ui/workspace.go` (tab Table Inspector hiển thị grid; sub-tab Data/Properties bằng `Segmented`, Properties là placeholder "Properties are coming in a later phase")

**Interfaces:**

- Consumes: `mock.Dataset`, `grid.Value`, `grid.Drafts`, `Palette`; MyGo: `ui.Table`, `ui.ListState`, `ui.TableColumn` (`E14`, `E15`), `Element.Shortcut`, `mygo.Clipboard.WriteText` (E19, E22).
- Produces:

  ```go
  type gridView struct { /* ListState, selected row, selected cell, columns layout */ }
  func (g *gridView) build(c *mygoui.Context, tab *app.Tab, p Palette)
  type cellPos struct{ Row, Col int } // Row = chỉ số hàng hiển thị; -1 = không chọn
  ```

  Hàng của Table là chỉ số hiển thị; identity của hàng cho draft/selection là `Dataset.RowID(row)` (`ListState.Key`, `B09`). Cột: `TableColumn{ID: tên cột, Title: tên cột, Width, MinWidth: 60, Sortable: false}`; mỗi ô là `SingleLine()` với `Display(120)`; NULL/empty dùng `Palette.Null`; ô có draft hiển thị giá trị draft.

- [ ] **Step 1: Viết `grid_test.go`:**
  - `TestOnlyVisibleRowsAreBuilt` (`orders`, 1.000.000 hàng, cửa sổ 1280×800): `len(tt.Texts())` < 1500; `tt.HasText("1")` (hàng đầu) là true; text giá trị của hàng thứ 500.000 không xuất hiện.
  - `TestScrollingShowsLaterRows`: `tt.Scroll(x, y, 0, dy)` đủ lớn → hàng đầu không còn trong `Texts()`, hàng khác xuất hiện; số text vẫn < 1500.
  - `TestSelectCellMarksOneCellAndRow`: click ô → `gridView.selectedCell` đúng `(row, col)`; click ô khác thì ô cũ bỏ chọn; hàng chứa ô vẫn được chọn.
  - `TestCopySelectedCellCopiesFullValue`: chọn ô text dài (> 500 rune) → `tt.Key(mygoui.Cmd, mygoui.KeyC)` → `tt.Clipboard()` bằng `CopyText()` đầy đủ; ô NULL → clipboard rỗng; ô binary → placeholder.
  - `TestCopyWithoutSelectionKeepsClipboard`: `tt.SetClipboard("keep")`, không chọn ô, Ctrl/Cmd+C → vẫn `"keep"`.
  - `TestColumnResizeKeepsOtherState` (probe PR2): sau kéo mép phải header (`tt.Press/Move/Release`), độ rộng cột đổi và ô đang chọn không đổi.
  - `TestWideTableBuildsAndScrolls` (probe PR1): `wide_events` (200 cột) mở được; `len(tt.Texts())` < 6000 ở cửa sổ 1280×800; `tt.Scroll` ngang đến cột cuối thấy tên cột thứ 200.
- [ ] **Step 2: Implement `grid.go`** với `ui.Table`; ô được bọc bằng một container nhận `Clicked()` để đặt `selectedCell` (chú ý click ô không được làm mất việc chọn hàng của list, `B10`); Ctrl/Cmd+C dùng `Shortcut` gắn vào phần tử bảng (không dùng shortcut cấp cửa sổ để không tranh phím với `TextInput`/editor). Nếu click ô và chọn hàng xung đột (phần tử ô nuốt click hoặc list nuốt click) hoặc `Shortcut` không tới được bảng, **dừng và báo** (cập nhật `B10`/`B11`/`B19` trong kết quả nghiệm thu), không tự thay bằng widget khác.
- [ ] **Step 3: Người dùng chạy** `go test ./internal/ui -run 'TestOnlyVisible|TestScrolling|TestSelectCell|TestCopy|TestColumnResize|TestWide' -v`. Expected: PASS.
- [ ] **Step 4: Người dùng đo probe PR1 bằng tay** (`go tool mygo dev`): mở `orders`, `wide_events`; cuộn dọc/ngang; mở panel Inspector (`F12` hoặc `Ctrl+Shift+I`, mục Performance, `E26`/docs) và ghi **last / average / slowest** frame time cho 12 cột và 200 cột trên máy tham chiếu (`User reported`, không coi là benchmark phát hành). Nếu `wide_events` giật rõ rệt (average vượt chu kỳ refresh), dừng và đưa kết quả cho người dùng quyết định (quyết định 5 của `P0-E`); không tự viết grid cắt cột.

**Điều kiện hoàn thành:** test PASS; số đo PR1 được ghi nhận; grid đọc dữ liệu qua `Dataset.Value` theo từng ô, không tạo slice theo toàn bộ dataset.

---

## Task 8: Editor ô cục bộ, dirty và Revert

**Files:**

- Modify: `internal/ui/grid.go`
- Test: `internal/ui/grid_edit_test.go`

**Interfaces:**

- Consumes: `gridView` (Task 7), `grid.Drafts`/`CellRef`, `Palette.Dirty`, `Dataset.Columns[i].Editable`, `Tab.Dataset.ReadOnly`; MyGo: `ui.TextInput`, `Element.AutoFocus`, `Submitted`, `Shortcut`, `DoubleClicked` (E16).
- Produces: trạng thái sửa trong `gridView` (`editing *cellEdit{pos cellPos; text string}`), hành vi:
  - Mở editor: double-click ô, hoặc F2 khi có ô được chọn; bỏ qua nếu cột không `Editable` hoặc dataset `ReadOnly`.
  - Enter: ghi draft vào `Tab.Drafts` (`Set(ref, original, newValue)`), đóng editor, trả focus về grid.
  - Escape: bỏ giá trị đang gõ, không tạo draft.
  - Nhấp sang ô khác khi đang sửa: xác nhận như Enter (quyết định 4).
  - Draft được tạo từ `Value{Kind: original.Kind, Text: s}` (original NULL → `KindText`); chuỗi rỗng là text rỗng, không phải NULL.
  - Ô có draft được tô `Palette.Dirty`; tiêu đề tab hiển thị dấu `●` (`Label("Unsaved changes")`) khi `Tab.Dirty()`; nút `Revert changes` (disabled khi không dirty) gọi `Drafts.Revert()`.

- [ ] **Step 1: Viết `grid_edit_test.go`** (mỗi test mở `users` hoặc `orders`, chọn ô cột text editable):
  - `TestF2OpensEditorEnterCommitsDraft`: F2 → có `TextInput`; `tt.Type("hello")`, `tt.Key(0, KeyEnter)` → `Tab.Drafts.Len()==1`, draft `Text` kết thúc/đúng như gõ (theo cách editor khởi tạo từ giá trị gốc), ô hiển thị giá trị mới, tab có `●`, editor đã đóng.
  - `TestDoubleClickOpensEditor`: hai click nhanh vào ô → editor mở.
  - `TestEscapeDiscardsEditorValue`: F2, gõ, `KeyEscape` → `Drafts.Len()==0`, ô giữ giá trị gốc, không `●`.
  - `TestEditBackToOriginalClearsDirty`: sửa rồi sửa lại đúng giá trị gốc → `Drafts.Len()==0`, không `●`.
  - `TestClickingAnotherCellCommitsEdit`: F2, gõ, click ô khác → draft có mặt, editor đóng.
  - `TestNonEditableCellsIgnoreF2`: cột PK (`id`), cột binary và mọi cột của view `active_users` → F2 không mở editor, `Drafts.Len()==0`.
  - `TestRevertButtonClearsAllDrafts`: sửa hai ô ở hai hàng → `Revert changes` → `Len()==0`, nút disabled, không `●`.
  - `TestCtrlCWhileEditingDoesNotCopyCell` (probe PR2): `tt.SetClipboard("keep")`, F2, Ctrl/Cmd+C không có vùng chọn trong input → clipboard vẫn `"keep"`.
  - `TestEditorSurvivesThemeSwitch` (probe PR4): F2, gõ `abc`, chuyển theme → editor vẫn mở và giữ `abc`; Enter tạo draft `abc`.
  - `TestDraftsDoNotLeakAcrossTabs`: sửa ở tab `users`, chuyển sang `orders` rồi quay lại → draft vẫn ở `users`, `orders` sạch.
- [ ] **Step 2: Implement** trong `grid.go` (`startEdit`, `commitEdit`, `cancelEdit`). Dùng `TextInput` trong ô (không dùng `EditableText`, `B12`). Nếu focus giữa list và `TextInput` không chuyển được (F2 không focus input, Enter/Escape bị list nuốt…) thì dừng và báo kết quả cho `B12`/PR3, không đổi sang widget khác.
- [ ] **Step 3: Người dùng chạy** `go test ./internal/ui -run 'TestF2|TestDoubleClick|TestEscape|TestEditBack|TestClickingAnother|TestNonEditable|TestRevert|TestCtrlC|TestEditorSurvives|TestDrafts' -v`. Expected: PASS.
- [ ] **Step 4: Người dùng thử tay** (probe PR3/PR4): sửa nhiều ô ở nhiều hàng, Enter/Escape/F2/double-click, đổi theme khi đang sửa, kiểm tra màu ô dirty ở Light và Dark, kiểm tra focus trả về grid sau Enter (mũi tên di chuyển tiếp). Ghi `User reported`.

**Điều kiện hoàn thành:** mọi test PASS; không có đường nào ghi ra database/file ngoài `settings.json`.

---

## Task 9: Tab SQL Editor tối thiểu và phím tắt toàn cục

**Files:**

- Create: `internal/ui/sql_editor.go`, `internal/ui/sql_editor_test.go`
- Modify: `internal/ui/workspace.go`, `internal/ui/window.go`

**Interfaces:**

- Consumes: `editor.NewBuffer`, `editor.LanguageNamed("SQL")`, `editor.State{Buffer, LineNumbers}`, `editor.View` (E20); `c.OnShortcut` hoặc `c.Shortcut` (E22).
- Produces: `type sqlView struct{ state *editor.State }`; `func (v *sqlView) build(c *mygoui.Context, tab *app.Tab)`; phím `Ctrl+N` (macOS: Command+N) mở SQL tab mới. SQL tab hiển thị dòng "Connection: <tên connection giả hoặc 'none'> (mock)" tĩnh, không có nút Run.

- [ ] **Step 1: Viết `sql_editor_test.go`:**
  - `TestCtrlNOpensNewSQLTab`: `tt.Key(mygoui.Cmd, mygoui.KeyN)` → tab "Query 1" active; lần hai → "Query 2".
  - `TestSQLTabsKeepSeparateBuffers`: gõ vào tab A, mở tab B, `Buffer.String()` của B rỗng; quay lại A vẫn còn nội dung.
  - `TestSQLTabUsesSQLLanguageAndLineNumbers`: `state.Buffer` có ngôn ngữ SQL và `state.LineNumbers == editor.LineNumbersOn`.
  - `TestClosingSQLTabReleasesItsEditorState`: đóng tab → map view state của `Shell` không còn khóa của tab đó.
  - `TestSQLTabIsNeverDirtyInPhase1`: `Tab.Dirty()` false dù có nội dung (bảo vệ SQL thuộc Phase 2.4).
- [ ] **Step 2: Implement.** `Ctrl+N` đăng ký ở cấp cửa sổ; kiểm tra rằng nó không chặn gõ chữ trong editor trên Windows (không phải phím soạn thảo mặc định ở Windows keymap, `E21`).
- [ ] **Step 3: Người dùng chạy** `go test ./internal/ui -run 'TestCtrlN|TestSQL' -v`. Expected: PASS.

**Điều kiện hoàn thành:** SQL tab chỉ soạn thảo; không có execute; không bind `Ctrl+Enter`.

---

## Task 10: Bảo vệ edit chưa lưu, startup trace và nghiệm thu thủ công

**Files:**

- Modify: `internal/ui/window.go`, `internal/ui/tabs.go`, `main.go`
- Test: `internal/ui/guard_test.go`

**Interfaces:**

- Consumes: `app.State.RequestClose/ConfirmClose/CancelClose/RequestQuit`, `PendingClose`, `PendingQuit`; MyGo: `ui.AlertDialog(c, open *bool, title, message string, buttons ...string) int`, `ui.AlertDismissed`, `(*mygo.Window).OnClose`, `CloseEvent.PreventDefault`, `(*Window).Invalidate`, `(*Window).Destroy` (E11, E23).
- Produces: hộp thoại `Discard changes?` khi đóng tab dirty (nút `Cancel`, `Discard`); hộp thoại khi đóng cửa sổ có tab dirty (`Cancel`, `Discard and exit`); Escape = Cancel; log thời gian khởi động khi đặt biến môi trường `SBDB_TRACE_STARTUP=1` (ghi stderr thời gian từ lúc process vào `main` đến `OnReadyToShow`, ghi chú "không gồm khởi tạo runtime Go").

- [ ] **Step 1: Viết `guard_test.go`:**
  - `TestClosingDirtyTabAsksFirst`: sửa ô, click `Close users` → có text "Discard changes?"; tab còn; `Cancel` giữ nguyên draft; mở lại và `Discard` → tab đóng.
  - `TestEscapeCancelsDiscardPrompt`: `KeyEscape` khi có prompt → tab và draft còn nguyên.
  - `TestClosingCleanTabDoesNotAsk`.
  - `TestQuitPromptWhenDirty`: `State.RequestQuit()` false → view hiển thị "Discard and exit"; `Cancel` đặt `PendingQuit=false`.
  - `TestPromptOnlyForTabsOnClosing`: với hai tab dirty, đóng một tab chỉ hỏi cho tab đó.
- [ ] **Step 2: Implement.** Trong `main.go`: `win.OnClose(func(e *mygo.CloseEvent){ if !st.RequestQuit() { e.PreventDefault(); win.Invalidate() } })`; khi người dùng chọn `Discard and exit` gọi `win.Destroy()` sau `saver.Flush()`. Trạng thái prompt nằm trong `State`, UI chỉ đọc.
- [ ] **Step 3: Người dùng chạy** `go test ./...` và `go vet ./...`. Expected: PASS. Tùy chọn: `go test -race ./...`.
- [ ] **Step 4: Người dùng nghiệm thu thủ công** (`go tool mygo dev`; ghi `User reported`), bám checklist roadmap mục 5:
  - Mở app, kéo sidebar/cửa sổ, đổi System/Light/Dark, đóng rồi mở lại → giữ nguyên; xóa `~\.dbl\settings.json` → app mở với mặc định; ghi nội dung rác vào file → app mở, có `settings.json.bad`, status bar có thông báo.
  - Mở nhiều tab, mở lại table đã mở → focus tab cũ; sửa ô ở một tab, tab kia độc lập; đóng tab/cửa sổ khi có edit → có hỏi.
  - Cuộn `orders` (1 triệu hàng) và `wide_events`; copy ô; F2/double-click/Enter/Escape.
  - Baseline: đặt `SBDB_TRACE_STARTUP=1` rồi chạy build thường (không `dev`): ghi thời gian khởi động; ghi RAM/CPU lúc idle từ Task Manager sau khi mở app vài giây. Đây là baseline trên máy tham chiếu, không phải kết luận so với mục tiêu <1 giây/<100 MB.
  - (Tùy chọn) `go tool mygo build` rồi chạy file trong `build/windows-amd64/` để xác nhận không cần WebView2.
- [ ] **Step 5: Executor tổng hợp kết quả** vào báo cáo bàn giao: danh sách file đã tạo/sửa, các test, kết quả probe PR1–PR4 (`User reported`), API khác với roadmap (nếu có). Dừng chờ người dùng; **không commit**. Nếu người dùng yêu cầu, cập nhật `docs/implementation-phases.md` mục 4/5 bằng kết quả probe.

**Điều kiện hoàn thành:** mọi test PASS do người dùng chạy; checklist thủ công được người dùng xác nhận; các khoảng trống `B06/B10/B12/B19` được ghi lại là đã đạt hoặc cần quyết định.

---

## Self-review

- **Spec/roadmap coverage:** Initialize project (Task 1); main window (Task 6); resizable sidebar (Task 6); mock connection tree (Task 4, 6); workspace tabs, state riêng, focus table đã mở (Task 5, 6); mock virtualized grid, scroll hai chiều, resize cột, chọn row/cell, copy (Task 7); editable-cell interaction (Task 8); theme Light/Dark/System dùng semantic colors và persist (Task 3, 6, 8); `settings.json` có version, thay thế an toàn (Task 2); startup chỉ đọc local state (Task 6); đóng tab/cửa sổ có dirty (Task 5, 10); SQL tab (Task 9).
- **Không thuộc phase này (cố ý):** connection CRUD/credential, database adapter, filter/paging/sort thật, Properties, SQL execution/file, Save to DB, Redis, packaging cuối.
- **Type consistency:** `TableKey`, `Tab`, `Workspace`, `State`, `Drafts`, `CellRef`, `Value`, `Dataset`, `Palette`, `Shell` được định nghĩa ở Task 2–6 và dùng nhất quán ở Task 6–10; `ThemePref` dùng cả ở `storage`, `app.State.SetTheme` và `ui.ThemeSource`.
- **Rủi ro đã biết, chuyển thành điểm dừng rõ ràng:** `B06` (hiệu năng 200 cột), `B10` (click ô vs chọn hàng), `B12` (focus editor ô), `B19` (route phím) — nếu test/thử tay thất bại, executor dừng và báo, không tự đổi stack. API MyGo là `v0.x`; không nâng version trong phase này.

## Execution Handoff

Đề xuất **Native** (một agent thực hiện tuần tự): các task chia sẻ interface (`State`, `Tab`, `gridView`) nên cần nhất quán, và agent không chạy lệnh nên không có lợi ích từ nhiều subagent song song; một reviewer độc lập ở cuối. Chờ người dùng duyệt plan, trả lời 8 mục "Quyết định cần người dùng duyệt" và chọn cách thực thi trước khi bắt đầu Task 1. Task 1 bắt đầu bằng lệnh người dùng chạy (cài toolchain và `init`).

---

## Tiến độ thực thi (Native, cập nhật bởi executor)

Không dùng worktree, ledger trong git hay commit (chỉ dẫn của người dùng); bản ghi tiến độ nằm ở đây.

| Task | Trạng thái |
| --- | --- |
| 1 | `go.mod` (module `dbl`, `go 1.27.2`, `github.com/egoist/mygo v0.5.5`, `tool`), `go.sum`, `mygo.json` (SBDb), `resources/icon.png` đã vào repo; `main.go` + `main_test.go` đã viết (smoke test gộp vào `TestShellShowsApplicationName`) và `.gitignore` đã tạo |
| 2–5 | Người dùng xác nhận đã chạy đúng (`go test ./internal/...` ok) |
| 6–10 | Code + test đã viết. Executor đã chạy (người dùng cho phép): `go vet ./...` sạch, `go build ./...` OK, `go test ./... -count=1` → 89 test PASS ở 6 package. **Còn lại: người dùng thử tay** (Task 10 Step 4) |

**Rulings (executor):**

1. Task 2 — `Normalize` coi giá trị `<= 0` là "chưa đặt" và dùng mặc định; chỉ giá trị dương nhỏ hơn mức tối thiểu mới bị kẹp lên. Lý do: plan tự mâu thuẫn (`width 0 → MinWindowWidth` vs `{"version":1}` → mặc định). Nếu sai: file `width: 0` mở ở 1280 thay vì 800.
2. Task 4 — `TestDatasetIsLazy` cho phép ≤ 4 allocation thay vì ≤ 2. Nếu sai: không ảnh hưởng hành vi.
3. Task 5 — thêm `Workspace.Find` và `State.CancelQuit` (Task 6/10 cần).
4. Thứ tự — Task 2–5 được viết trước khi Task 1 xong (chỉ phụ thuộc module path `dbl`).
5. Task 1 — bỏ `main.go` placeholder "DBL": smoke test nằm trong `main_test.go` (`TestShellShowsApplicationName`) sau khi Task 6 xong, tránh viết rồi thay ngay.
6. Task 6 — `TestSidebarWidthComesFromSettings`: văn bản trạng thái rỗng nằm giữa pane phải nên khi sidebar hẹp lại chỉ dịch ~50 DIP; test chỉ khẳng định `X` giảm, không khẳng định `X < 300` như plan.
7. Task 7 — phím Ctrl/Cmd+C và F2 của grid dùng `c.Shortcut` cấp cửa sổ có điều kiện (đang xem tab bảng, có ô được chọn, không có editor mở) thay vì shortcut gắn vào phần tử bảng: không phụ thuộc việc bảng đang giữ focus bàn phím (`B11`/`B19`). Rủi ro: Ctrl+C khi focus ở explorer vẫn copy ô đã chọn. Copy dùng `c.WriteClipboard` (không phải `mygo.Clipboard`, vì cần chạy được trong `ui.Tester`).
8. Task 7 — bỏ `TestColumnResizeKeepsOtherState` (kéo mép header qua `Tester` cần toạ độ header chưa xác minh); resize cột kiểm tra bằng tay ở bước nghiệm thu.
9. Task 8 — cell editor là `TextInput` trong ô (không dùng `EditableText`); mở bằng double-click ô hoặc F2; `Enter` mà chữ không đổi thì không tạo draft (tránh biến NULL thành chuỗi rỗng); click ô khác = xác nhận.
10. Mọi nơi thay đổi state ngay trong lúc dựng view đều gọi `c.Invalidate()` để chắc chắn có frame mới (không dựa vào việc widget tự yêu cầu).
11. Task 6 — thêm `Label("Grid")` cho bảng (không dùng "Data" để khỏi trùng với nút Data/Properties).

12. Task 8 — dấu chưa lưu `●` có `Label("Unsaved changes")` nên `Tester` tìm theo nhãn; test dùng `HasText("Unsaved changes")` (2 test đã sửa sau lần chạy đầu).
13. Sau nghiệm thu (yêu cầu của người dùng: chuyển theme và mở dropdown "hơi nhanh") — thêm `internal/ui/themefade.go` (nội suy toàn bộ màu theme 220 ms, smoothstep, bỏ qua khi `ReduceMotion`) và hiệu ứng mờ dần + trượt nhẹ 160 ms cho các hàng explorer vừa được mở (`explorer.reveal`). Đóng hàng không có hiệu ứng vì `Outline` chỉ dựng hàng đang hiện (docs `transitions.md`: hàng của List không tự trượt/đến/đi). Hằng số `themeFadeDuration`, `revealDuration` chỉnh được. Đây là mở rộng ngoài plan ban đầu; xóa bằng cách bỏ `sh.fader.apply(c)` và các lời gọi `e.reveal/e.endPass`.

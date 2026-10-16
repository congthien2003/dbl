# Nghiên cứu hiệu ứng nhẹ và chuyển động mượt cho SBDb

Ngày nghiên cứu: 2026-10-16.

Phạm vi: UI native của SBDb, dùng `github.com/egoist/mygo v0.5.5` theo [go.mod](../go.mod). Tài liệu tổng hợp source hiện tại, tài liệu chính thức và đề xuất cho lần triển khai sau. Chưa triển khai các đề xuất hoặc đo hiệu năng thực tế của app trong nghiên cứu này.

## 1. Mục tiêu

- Phản hồi rõ khi hover, nhấn nút, chuyển trạng thái và mở nội dung.
- Chuyển động ngắn, biên độ nhỏ, phù hợp với công cụ làm việc có nhiều dữ liệu.
- Hướng tới thời gian frame ổn định trong ngân sách khoảng 16,7ms trên màn hình 60Hz; màn hình 120Hz có ngân sách khoảng 8,3ms.
- Dừng yêu cầu frame khi hiệu ứng kết thúc và tôn trọng tùy chọn giảm chuyển động của hệ điều hành.

Animation có thể cải thiện cảm giác phản hồi. Việc đạt 60fps thực tế còn phụ thuộc vào chi phí dựng view, layout, paint, present, dữ liệu và thiết bị. Thời lượng hiệu ứng 150ms tự nó không chứng minh hiệu ứng chạy mượt.

Nguồn: [MyGo Inspector — Performance and issues](https://mygo.egoist.dev/docs/ui/inspector#performance-and-issues), [Motion in Windows](https://learn.microsoft.com/en-us/windows/apps/design/motion/).

## 2. Hiện trạng trong app

| Thành phần | Source | Hành vi hiện tại |
|---|---|---|
| Đổi theme | [themefade.go](../internal/ui/themefade.go) | Nội suy màu theme trong 220ms, dùng `c.Now()`, `smoothstep` và `c.AnimationFrame()`; bỏ chuyển tiếp khi đổi theme trong chế độ `ReduceMotion`. |
| Explorer | [explorer.go](../internal/ui/explorer.go) | Khi mở ancestor, hàng vừa lộ ra fade trong 160ms và dịch từ -6 DIP về vị trí cuối; `ReduceMotion` hiển thị ngay. |
| Shell | [shell.go](../internal/ui/shell.go) | Áp dụng theme fader trước khi dựng các phần còn lại của cửa sổ. |
| Workspace tabs | [tabs.go](../internal/ui/tabs.go) | Dùng `TabsBase`, hiển thị nền cho tab active, nút đóng và tạo SQL tab. |
| Data grid | [grid.go](../internal/ui/grid.go) | Dùng `Table` và `ListState`; giữ state chọn hàng, chọn ô và editor của ô. |

Theme fade và Explorer reveal hiện yêu cầu frame qua `c.AnimationFrame()`. Theo mô hình MyGo, animation có giá trị được view đọc sẽ chạy lại view trong mỗi frame chuyển động. Đây là cơ hội khảo sát tối ưu, chưa phải bằng chứng rằng hai hiệu ứng đang gây giật.

Nguồn: [MyGo Motion — Poses that only paint](https://mygo.egoist.dev/docs/ui/motion#poses-that-only-paint).

## 3. Hiệu ứng đề xuất

Các thông số dưới đây là đề xuất khởi đầu cho SBDb, cần điều chỉnh sau khi thử trên app. Đây không phải thông số bắt buộc của MyGo hoặc cam kết về hiệu năng.

| Vị trí | Hiệu ứng | Thông số khởi đầu | Ưu tiên |
|---|---|---|---|
| Button và tab | Chuyển màu nền khi hover/pressed | 90–120ms, không đổi kích thước | Cao |
| Explorer | Fade và dịch nhẹ cho hàng vừa được mở ra | 140–160ms, dịch 4–6 DIP | Cao |
| Trạng thái connection | Fade khi đổi Connecting → Connected/Error | 100–150ms, giữ thông tin trạng thái rõ ràng | Cao |
| Loading | Một spinner nhỏ trong vùng đang chờ | Chỉ chạy trong thời gian có tác vụ | Cao |
| Dialog | Fade kết hợp scale nhẹ | 140–180ms, scale 0.98 → 1 khi mở | Vừa |
| Chuyển workspace tab | Fade ngắn cho nội dung vừa chọn | 100–140ms, hạn chế dựng đồng thời hai grid | Vừa |

Với app làm việc, nên bắt đầu bằng easing giảm tốc và chuyển động ít hoặc không nảy. Khi thao tác lặp nhanh, hiệu ứng cần chuyển tiếp từ trạng thái đang hiển thị thay vì gây nhảy hình hoặc trì hoãn thao tác tiếp theo. Spring của MyGo có thể giữ vận tốc khi target thay đổi.

Nguồn tham khảo thiết kế: [Motion in Windows](https://learn.microsoft.com/en-us/windows/apps/design/motion/). Nguồn API: [MyGo Motion](https://mygo.egoist.dev/docs/ui/motion).

## 4. Chọn cơ chế animation trong MyGo

### 4.1. Ưu tiên `AnimatePose` cho transform và fade

`AnimatePose` hỗ trợ dịch chuyển, scale, xoay và fade. Các frame chuyển động có thể chỉ paint, không chạy lại hàm view. Đây là lựa chọn nên khảo sát trước khi thêm chuyển động vào vùng UI lớn.

Lưu ý `Pose.Fade`: `0` là hiện đầy đủ, `1` là biến mất. Các element cần identity ổn định giữa các frame; khi phần tử có thể thay đổi thứ tự, dùng key phù hợp với identity của dữ liệu.

Chỉ paint vẫn có chi phí vẽ. Nên dùng chung một pose cho các phần tử chuyển động cùng nhau khi phù hợp, thay vì tạo animation độc lập cho từng phần tử.

Nguồn: [MyGo Motion — Poses that only paint](https://mygo.egoist.dev/docs/ui/motion#poses-that-only-paint).

### 4.2. Giới hạn thuộc tính của `Transition`

Với hover, dùng `ElementTransition` giới hạn `Colors: true` khi chỉ cần chuyển màu nền hoặc viền. Nếu không chọn thuộc tính, transition có thể animate position, size và colors.

Animation size khiến nội dung layout lại theo kích thước trung gian. Với grid hoặc editor lớn, cần cân nhắc chi phí trước khi animate chiều rộng sidebar hay kích thước workspace.

Các hàng của `List` được đặt và dựng theo vùng nhìn. Nội dung bên trong có thể có transition, nhưng transition không tự làm hàng của danh sách trượt vào hoặc ra. Vì Explorer dùng danh sách ảo hóa, không nên thay reveal hiện tại bằng `Transition.Enter` mà chưa xem lại lifecycle và identity của hàng.

Nguồn: [MyGo Transitions](https://mygo.egoist.dev/docs/ui/transitions).

### 4.3. Dùng animation trong `Draw` cho spinner tùy chỉnh

Nếu hình vẽ thay đổi nhưng layout giữ nguyên, có thể tính chuyển động từ `p.Now()` và yêu cầu frame qua `p.AnimationFrame()` bên trong `Draw`. Khi không có thay đổi khác, MyGo chỉ paint các element của frame trước; phần tử ngoài vùng nhìn không được paint nên không yêu cầu frame từ thao tác vẽ này.

Chỉ yêu cầu frame khi tác vụ còn chạy. Nếu cần cập nhật state từ goroutine, đưa thay đổi về UI bằng cơ chế `Window.Update` của app.

Nguồn: [Drawing and animation — Animation](https://mygo.egoist.dev/docs/ui/drawing#animation), [MyGo Views](https://mygo.egoist.dev/docs/ui/views).

## 5. Nguyên tắc giữ hiệu ứng nhẹ

- Ưu tiên transform và fade không làm thay đổi layout; hạn chế animation width, height và collapse quanh grid lớn.
- Không gắn animation độc lập lên mọi cell; ưu tiên phản hồi ở phần tử người dùng đang tương tác.
- Giữ callback dựng cell rẻ; tránh format hoặc cấp phát lại dữ liệu không đổi trong mỗi frame.
- Hạn chế stagger nhiều hàng, blur lớn và hiệu ứng trang trí lặp vô hạn. Đây là giới hạn thiết kế đề xuất, không phải kết luận từ phép đo của app.
- Dùng thời gian đã trôi qua hoặc API motion của MyGo; tránh tăng vị trí theo một lượng cố định mỗi frame.
- Dùng cơ chế yêu cầu frame của MyGo; tránh timer 16ms chạy liên tục chỉ để giữ animation.
- Tôn trọng `c.Preferences().ReduceMotion` trong animation tự tính. MyGo vẫn có thể giữ fade/chuyển màu và spinner tùy loại API; không mặc định rằng tùy chọn này tắt toàn bộ animation.
- Khi thêm loading, giữ dữ liệu đang có nếu phù hợp với luồng nghiệp vụ; chỉ indicator cần chuyển động.

MyGo v0.5.5 dùng Direct3D 11 trên Windows hoặc WARP khi không có GPU driver phù hợp. Tuy nhiên, hỗ trợ GPU không tự bảo đảm ngân sách frame. Framework chỉ vẽ khi có thay đổi, nên trạng thái idle không cần duy trì vòng lặp render liên tục.

Nguồn: [MyGo Motion — Less motion and frames](https://mygo.egoist.dev/docs/ui/motion#less-motion-and-frames), [Rendering](https://mygo.egoist.dev/docs/ui/rendering).

## 6. Thứ tự triển khai đề xuất

1. Hover cho button/tab và chuyển trạng thái connection: phạm vi trực quan nhỏ, phản hồi thường xuyên.
2. Khảo sát Explorer reveal theo hướng chỉ paint: giữ timing hiện có, identity của hàng và hành vi `ReduceMotion`.
3. Loading indicator tại vùng có tác vụ: bảo đảm dừng khi hoàn tất hoặc vùng đó không còn hiển thị.
4. Dialog và chuyển nội dung tab: thử trên workspace có grid lớn trước khi áp dụng rộng.

Theme fade là ứng viên đo riêng vì đổi theme ảnh hưởng màu của toàn bộ UI; chưa có cơ sở để giả định có thể thay toàn bộ nội suy theme bằng một pose.

Đây là thứ tự đề xuất cho công việc sau, chưa phải plan chỉnh sửa code đã được duyệt.

## 7. Đo và đánh giá khi triển khai

Phần này là hướng dẫn để người dùng tự chạy. Nghiên cứu hiện tại chưa chạy app, build, test, lint hoặc lệnh verify.

### Bật log frame chậm

Chạy từ root repository bằng PowerShell:

```powershell
$env:MYGO_FRAME_STATS = '8'
go run .
```

Giá trị `8` ghi các frame vượt 8ms. Log phân bổ thời gian build, layout, paint, present, cùng thông tin allocation, text layout và GC. Allocation/GC được ghi theo toàn process, có thể bao gồm goroutine khác. Lệnh `go run .` có thể biên dịch app trước khi khởi chạy.

Sau khi dừng app, bỏ cấu hình log khỏi phiên PowerShell nếu không cần nữa:

```powershell
Remove-Item Env:MYGO_FRAME_STATS
```

Nguồn: [MyGo Rendering](https://mygo.egoist.dev/docs/ui/rendering).

### Quan sát bằng Inspector

Trong cửa sổ có developer tools được bật, dùng `F12` hoặc `Ctrl+Shift+I` trên Windows. Mục Performance hiển thị thời gian build, layout và paint so với chu kỳ refresh của màn hình. Inspector chiếm một phần chiều rộng cửa sổ, vì vậy cần lưu ý layout thay đổi khi so sánh các lần đo.

Nguồn: [MyGo Inspector](https://mygo.egoist.dev/docs/ui/inspector).

### Kịch bản đánh giá

- Hover nhanh qua nhiều button/tab và lặp thao tác khi animation chưa kết thúc.
- Mở/đóng Explorer với nhiều connection và cuộn khi reveal còn chạy.
- Đổi theme khi đang mở grid nhiều hàng/cột.
- Chuyển workspace tab liên tục, nhập SQL và sửa ô trong grid.
- Hoàn tất/hủy connection trong khi loading còn hiển thị.
- Bật giảm chuyển động trong hệ điều hành; kiểm tra tín hiệu trạng thái vẫn rõ.
- Để app idle sau thao tác; kiểm tra không có animation hoặc yêu cầu frame ngoài ý muốn.

So sánh trước/sau trên cùng máy, dữ liệu, kích thước cửa sổ và refresh rate. Quan sát các frame chậm và độ trễ tương tác, không chỉ FPS trung bình. Frame diagnostics hỗ trợ tìm chi phí, chưa thay thế phép đo đầy đủ về frame pacing và thời điểm frame thực sự xuất hiện trên màn hình.

## 8. Giới hạn bằng chứng và tài liệu tham khảo

Benchmark công bố của MyGo đo steady frames bằng CPU với `ui.Tester` trên Apple M5/macOS. Kết quả đó chưa chứng minh SBDb đạt 60fps trên Windows hoặc trên dữ liệu thực. Cần đo trên app và thiết bị mục tiêu trước khi kết luận.

| Tài liệu chính thức | Mục đích sử dụng |
|---|---|
| [Motion](https://mygo.egoist.dev/docs/ui/motion) | Pose, spring, identity, reduced motion và lịch frame |
| [Transitions](https://mygo.egoist.dev/docs/ui/transitions) | Chuyển màu, position/size, enter/exit và giới hạn với List |
| [Drawing and animation](https://mygo.egoist.dev/docs/ui/drawing#animation) | Animation tự vẽ và frame chỉ paint |
| [Views](https://mygo.egoist.dev/docs/ui/views) | Lifecycle của view và cập nhật state từ goroutine |
| [Rendering](https://mygo.egoist.dev/docs/ui/rendering) | Backend render, idle và `MYGO_FRAME_STATS` |
| [Inspector](https://mygo.egoist.dev/docs/ui/inspector) | Quan sát element và thời gian frame |
| [Native UI performance](https://mygo.egoist.dev/docs/ui/performance) | Benchmark tham khảo và phạm vi đo |
| [Motion in Windows](https://learn.microsoft.com/en-us/windows/apps/design/motion/) | Nguyên tắc chuyển động và timing tham khảo |

Các API cốt lõi được đối chiếu với dependency MyGo v0.5.5 trong nghiên cứu. Website có thể thay đổi sau thời điểm ghi tài liệu; khi nâng phiên bản MyGo, cần đối chiếu lại semantics của API trước khi triển khai.

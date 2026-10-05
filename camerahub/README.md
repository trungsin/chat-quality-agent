# CameraHub

Một video gateway để xem camera Dahua, Imou và drone trên cùng một màn hình:

- **Nhận luồng vào** từ nhiều nguồn:
  - kéo RTSP từ đầu ghi hoặc camera Dahua/Imou có IP, hoặc nằm sau VPN,
  - lấy luồng từ camera Imou **không có IP** qua Imou Open Platform,
  - nhận RTMP/SRT do drone, OBS hoặc encoder đẩy lên.
- **Màn hình giám sát nhiều ô**: chia 1, 4, 9, 16 hoặc 25 ô, phát bằng WebRTC với độ trễ thấp, lọc theo khu vực.
- **Restream**: đẩy một nguồn tới nhiều đích cùng lúc (YouTube, Facebook, máy chủ khác) qua RTMP, RTMPS hoặc SRT.
- **Giám sát**: hiển thị nguồn online/offline và gửi cảnh báo Telegram khi mất tín hiệu quá 90 giây.
- Thêm **cả đầu ghi trong một lần**: nhập dải kênh `1-16`, hệ thống tự tạo 16 nguồn.

MediaMTX lo phần xử lý video. CameraHub quản lý danh sách nguồn, giao diện, đồng bộ cấu hình và cảnh báo.

```
Dahua NVR/IPC (IP/VPN) ──RTSP pull──┐
Imou (IP/VPN) ───────────RTSP pull──┤
Imou không IP ──Imou Open API (HLS)─┼──► MediaMTX ──► WebRTC/HLS ──► Màn hình giám sát (trình duyệt)
Camera khác (ONVIF/RTSP) ───────────┤        │
Drone / OBS ──RTMP/SRT push─────────┘        └──► ffmpeg tee ──► YouTube / Facebook / RTMP / SRT
                     ▲
          CameraHub (API, giao diện, cảnh báo) ── điều khiển MediaMTX qua API v3
```

## Chạy

```bash
cd camerahub
cp .env.example .env        # sửa PUBLIC_HOST, HUB_ADMIN_PASSWORD
# đổi mật khẩu "publisher" trong mediamtx.yml
docker compose up -d --build
```

Sau đó mở `http://PUBLIC_HOST:8080`.

| Cổng | Dùng cho |
|---|---|
| 8080/tcp | Giao diện CameraHub |
| 8889/tcp, 8189/udp | WebRTC (xem trên trình duyệt) |
| 8888/tcp | HLS |
| 1935/tcp | Nhận RTMP |
| 8890/udp | Nhận SRT |
| 8554/tcp | RTSP (xem bằng VLC hoặc VMS khác) |

Không mở cổng 9997 (API của MediaMTX) ra Internet.

## Thêm nguồn

| Loại | Khi nào dùng | Cần điền |
|---|---|---|
| `dahua` | Đầu ghi/camera Dahua truy cập được bằng IP (IP tĩnh, mở port, hoặc qua VPN) | IP, cổng RTSP (554), tài khoản, mật khẩu, kênh hoặc dải kênh |
| `imou_local` | Camera Imou trong LAN/VPN | IP, mật khẩu là **mã an toàn** trên tem (user `admin`) |
| `imou_cloud` | Camera Imou **không có IP** | Serial thiết bị, kênh (camera lẻ = 0) |
| `rtsp` | Mọi camera có RTSP (ONVIF, hãng khác) | URL RTSP |
| `push` | Drone, OBS, encoder | Không cần. Thiết bị đẩy lên địa chỉ bên dưới |

Dahua và Imou dùng chung dạng URL RTSP:
`rtsp://user:pass@IP:554/cam/realmonitor?channel=1&subtype=1`. Giá trị `subtype` là 0 cho luồng chính, 1 cho luồng phụ. **Nên dùng luồng phụ cho màn hình nhiều ô** để tiết kiệm băng thông.

Thiết bị đẩy luồng (`push`) cấu hình như sau:
```
RTMP: rtmp://PUBLIC_HOST:1935/<id>?user=publisher&pass=<mật khẩu>
SRT:  srt://PUBLIC_HOST:8890?streamid=publish:<id>:publisher:<mật khẩu>
```
Với drone DJI: vào *Live Streaming → RTMP tùy chỉnh* và dán địa chỉ RTMP ở trên.

### Đầu ghi Dahua không có IP

Bản này kéo luồng trực tiếp, nên trung tâm phải truy cập được tới đầu ghi. Với site không có IP, có hai cách:

1. **VPN (đơn giản nhất):** đặt một mini PC hoặc router chạy Tailscale/WireGuard tại site, rồi thêm nguồn `dahua` bằng IP nội bộ của VPN.
2. **GB28181:** đầu ghi tự đăng ký về trung tâm. Chưa có trong bản này (xem lộ trình bên dưới).

### Imou không có IP (Imou Open Platform)

1. Đăng ký tài khoản developer tại open.imoulife.com và lấy `AppId` và `AppSecret`, đặt vào `.env`.
2. Gắn thiết bị vào tài khoản developer bằng serial và mã an toàn.
3. Thêm nguồn loại `imou_cloud` với serial thiết bị.

> ⚠️ Phần client Imou viết theo cấu trúc API công khai của Imou (`accessToken`, `bindDeviceLive`, `getLiveStreamInfo`) nhưng **chưa được kiểm thử với tài khoản thật**. Hãy chạy thử với một camera trước. Nếu tài khoản của bạn thuộc vùng khác, đổi `IMOU_API_URL`. Video đi qua cloud Imou nên có độ trễ vài giây và có thể bị giới hạn hạn mức.

## API

| Method | Đường dẫn | Mô tả |
|---|---|---|
| GET | `/api/sources` | Danh sách nguồn kèm trạng thái online, codec, số người xem, lỗi |
| POST | `/api/sources` | Tạo nguồn. Thêm `"channels": "1-16"` để tạo cả đầu ghi |
| PUT | `/api/sources/{id}` | Sửa nguồn. Bỏ trống mật khẩu để giữ mật khẩu cũ |
| DELETE | `/api/sources/{id}` | Xóa nguồn |
| POST | `/api/sync` | Đẩy lại toàn bộ cấu hình sang MediaMTX |

Ví dụ thêm một đầu ghi Dahua 8 kênh:
```bash
curl -u admin:$PASS -X POST http://localhost:8080/api/sources -H 'Content-Type: application/json' -d '{
  "id": "nvr-hn", "name": "NVR Hà Nội", "site": "Hà Nội", "kind": "dahua",
  "host": "100.64.0.12", "username": "admin", "password": "***",
  "channels": "1-8", "subtype": 1, "onDemand": true
}'
```

## Ghi chú vận hành

- Danh sách nguồn lưu trong `data/sources.json`. Mật khẩu camera đang lưu dạng rõ, nên cần bảo vệ thư mục này.
- MediaMTX không lưu các path được thêm qua API. CameraHub kiểm tra mỗi 15 giây và tự thêm lại path còn thiếu, kể cả sau khi MediaMTX khởi động lại.
- Bật "Chỉ kéo khi có người xem" thì camera chỉ bị kéo luồng khi có người mở xem. Nguồn có restream luôn được kéo liên tục.
- Người xem hiện không cần mật khẩu để đọc luồng. Khi đưa ra Internet, hãy đặt hệ thống sau VPN hoặc thêm tài khoản `read` trong `mediamtx.yml`.

## Lộ trình

- [ ] GB28181 (WVP-GB28181-pro hoặc ZLMediaKit) cho đầu ghi Dahua không có IP
- [ ] Giám sát sức khỏe Dahua qua HTTP API: ổ cứng, firmware, lệch giờ
- [ ] Xem lại bản ghi trên đầu ghi (`/cam/playback`)
- [ ] Ghi hình ở trung tâm (MediaMTX record) và tải clip
- [ ] Bản đồ vị trí camera, phân quyền theo khu vực, PostgreSQL thay file JSON

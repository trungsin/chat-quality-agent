# Kết nối CRM / Webhook / Browser agent

Kênh **Webhook / CRM** cho phép bất kỳ hệ thống nào đẩy cuộc chat vào CQA — CRM (HubSpot, Salesforce, Odoo, Bitrix24...), tổng đài chat, nền tảng không có API công khai (qua browser agent [jev-ultrafast](https://github.com/trungsin/jev-ultrafast)). CQA dùng chung các công việc **Đánh giá chất lượng** và **Phân loại** như với Zalo/Facebook.

```
CRM / nền tảng chat ──(API/webhook)──┐
                                     ├──> POST /api/v1/ingest/:channelId ──> CQA: QC + phân loại
jev-ultrafast (đọc màn hình inbox) ──┘                                         │
                                                                               ▼
Claude / agent CRM  <──── MCP: cqa_get_customer_profile, cqa_list_customers_by_tag
```

## 1. Tạo kênh

**Kênh chat → Kết nối kênh → Webhook / CRM / Browser agent**. Sau khi tạo, CQA hiện **Ingest URL** và **Ingest token** đúng một lần — lưu lại ngay.

## 2. Đẩy cuộc chat

```bash
curl -X POST "$CQA_URL/api/v1/ingest/$CHANNEL_ID" \
  -H "Authorization: Bearer $INGEST_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "conversations": [{
      "external_id": "ticket-1024",
      "external_user_id": "crm-contact-42",
      "customer_name": "Nguyễn Lan",
      "metadata": {"crm": "hubspot", "stage": "lead"},
      "messages": [
        {"sender_type": "customer", "sender_name": "Lan", "content": "Sản phẩm này giá bao nhiêu?", "sent_at": "2026-09-25T09:00:00+07:00"},
        {"sender_type": "agent", "sender_name": "Shop", "content": "Dạ 200k ạ", "sent_at": "2026-09-25T09:01:00+07:00"}
      ]
    }]
  }'
```

| Trường | Bắt buộc | Ghi chú |
|---|---|---|
| `external_id` | Có | Mã cuộc chat ổn định bên hệ thống nguồn. Đẩy lại cùng mã sẽ cập nhật, không tạo bản trùng |
| `external_user_id` | Không | Mã khách hàng bên CRM — dùng để gộp hồ sơ khách hàng |
| `messages[].sender_type` | Có | `customer`, `agent` hoặc `system` |
| `messages[].external_id` | Không | Không có thì CQA tự sinh mã từ nội dung, nên gửi lại tin cũ không bị trùng |
| `messages[].sent_at` | Không | RFC3339. Không có thì giữ theo thứ tự trong payload |

Giới hạn mỗi request: 100 cuộc chat, 1000 tin nhắn, 5 MB.

Công việc có lịch **Sau khi đồng bộ** (`after_sync`) sẽ tự chạy ngay sau mỗi lần đẩy.

## 3. Tích hợp qua MCP

Ngoài các tool sẵn có, MCP server của CQA có thêm:

| Tool | Dùng để |
|---|---|
| `cqa_ingest_conversation` | Đẩy một cuộc chat vào kênh webhook (cần quyền ghi kênh) |
| `cqa_get_customer_profile` | Hồ sơ khách hàng theo `external_user_id` hoặc tên: số cuộc chat, kênh, **nhãn phân loại (segments)**, tỉ lệ đạt, điểm trung bình, vi phạm gần đây |
| `cqa_list_customers_by_tag` | Danh sách khách hàng theo nhãn phân loại (ví dụ "Khiếu nại", "Hỏi giá") để đồng bộ phân khúc về CRM |

Ví dụ yêu cầu cho Claude: *"Lấy hồ sơ khách crm-contact-42 trong CQA rồi cập nhật lifecycle stage bên HubSpot theo nhãn phân loại."*

## 4. Nền tảng không có API: dùng browser agent

[jev-ultrafast](https://github.com/trungsin/jev-ultrafast) có MCP server `jev-mcp` với tool `capture_conversation`: mở inbox trên trình duyệt đã đăng nhập, tìm cuộc chat theo mô tả tự nhiên, trích transcript rồi đẩy vào kênh webhook này. Cấu hình trong `.env` của jev-ultrafast:

```
CQA_URL=https://cqa.yourdomain.com
CQA_CHANNEL_ID=<id kênh webhook>
CQA_INGEST_TOKEN=<ingest token>
```

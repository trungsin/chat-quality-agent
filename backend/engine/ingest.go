package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// Giới hạn cho một lần đẩy dữ liệu, để một nguồn ngoài (CRM, browser agent)
// không thể nhồi quá nhiều bản ghi vào một request.
const (
	MaxIngestConversations = 100
	MaxIngestMessages      = 1000
	maxIngestContentLen    = 20000
)

// IngestMessage là một tin nhắn do hệ thống ngoài đẩy vào.
type IngestMessage struct {
	ExternalID  string                `json:"external_id"`
	SenderType  string                `json:"sender_type"` // customer | agent | system
	SenderName  string                `json:"sender_name"`
	Content     string                `json:"content"`
	ContentType string                `json:"content_type"`
	Attachments []channels.Attachment `json:"attachments"`
	SentAt      *time.Time            `json:"sent_at"`
}

// IngestConversation là một cuộc chat do hệ thống ngoài đẩy vào.
type IngestConversation struct {
	ExternalID     string                 `json:"external_id"`
	ExternalUserID string                 `json:"external_user_id"`
	CustomerName   string                 `json:"customer_name"`
	Metadata       map[string]interface{} `json:"metadata"`
	Messages       []IngestMessage        `json:"messages"`
}

// IngestPayload là nội dung một lần đẩy: một hoặc nhiều cuộc chat.
type IngestPayload struct {
	Conversations []IngestConversation `json:"conversations"`
}

// IngestResult tóm tắt một lần đẩy.
type IngestResult struct {
	Conversations []IngestedConversation `json:"conversations"`
	Messages      int                    `json:"messages"`
}

type IngestedConversation struct {
	ID         string `json:"id"`
	ExternalID string `json:"external_id"`
	Messages   int    `json:"messages"`
}

var validSenderTypes = map[string]bool{"customer": true, "agent": true, "system": true}

// NormalizeIngest kiểm tra và chuẩn hoá payload thành dạng mà bộ đồng bộ đang
// dùng. Tin nhắn thiếu external_id được gán mã băm ổn định từ nội dung, nên đẩy
// lại cùng một cuộc chat (ví dụ browser agent đọc lại màn hình) không tạo bản
// trùng.
func NormalizeIngest(p IngestPayload, now time.Time) ([]channels.SyncedConversation, [][]channels.SyncedMessage, error) {
	if len(p.Conversations) == 0 {
		return nil, nil, errors.New("conversations is empty")
	}
	if len(p.Conversations) > MaxIngestConversations {
		return nil, nil, fmt.Errorf("too many conversations (max %d)", MaxIngestConversations)
	}
	total := 0
	convs := make([]channels.SyncedConversation, 0, len(p.Conversations))
	msgs := make([][]channels.SyncedMessage, 0, len(p.Conversations))
	for i, c := range p.Conversations {
		externalID := strings.TrimSpace(c.ExternalID)
		if externalID == "" {
			return nil, nil, fmt.Errorf("conversations[%d].external_id is required", i)
		}
		if len(c.Messages) == 0 {
			return nil, nil, fmt.Errorf("conversations[%d].messages is empty", i)
		}
		total += len(c.Messages)
		if total > MaxIngestMessages {
			return nil, nil, fmt.Errorf("too many messages (max %d)", MaxIngestMessages)
		}

		// Tin không có giờ gửi được xếp liên tiếp theo thứ tự trong payload,
		// lùi về trước "now" để giữ đúng trình tự hội thoại.
		out := make([]channels.SyncedMessage, 0, len(c.Messages))
		seen := map[string]int{}
		var last time.Time
		for j, m := range c.Messages {
			sender := strings.ToLower(strings.TrimSpace(m.SenderType))
			if !validSenderTypes[sender] {
				return nil, nil, fmt.Errorf("conversations[%d].messages[%d].sender_type must be customer, agent or system", i, j)
			}
			content := strings.TrimSpace(m.Content)
			if content == "" && len(m.Attachments) == 0 {
				return nil, nil, fmt.Errorf("conversations[%d].messages[%d] has no content", i, j)
			}
			if len(content) > maxIngestContentLen {
				content = content[:maxIngestContentLen]
			}
			sentAt := now.Add(time.Duration(j-len(c.Messages)) * time.Second)
			if m.SentAt != nil && !m.SentAt.IsZero() {
				sentAt = *m.SentAt
			}
			if sentAt.After(last) {
				last = sentAt
			}
			contentType := m.ContentType
			if contentType == "" {
				contentType = "text"
			}
			msgID := strings.TrimSpace(m.ExternalID)
			if msgID == "" {
				key := sender + "\x00" + content
				msgID = ingestMessageID(externalID, sender, content, seen[key])
				seen[key]++
			}
			out = append(out, channels.SyncedMessage{
				ExternalID:  msgID,
				SenderType:  sender,
				SenderName:  m.SenderName,
				Content:     content,
				ContentType: contentType,
				Attachments: m.Attachments,
				SentAt:      sentAt,
				RawData:     map[string]interface{}{"source": "ingest"},
			})
		}

		customer := strings.TrimSpace(c.CustomerName)
		if customer == "" {
			for _, m := range out {
				if m.SenderType == "customer" && m.SenderName != "" {
					customer = m.SenderName
					break
				}
			}
		}
		convs = append(convs, channels.SyncedConversation{
			ExternalID:     externalID,
			ExternalUserID: strings.TrimSpace(c.ExternalUserID),
			CustomerName:   customer,
			LastMessageAt:  last,
			Metadata:       c.Metadata,
		})
		msgs = append(msgs, out)
	}
	return convs, msgs, nil
}

// Mã không phụ thuộc vị trí trong payload: agent đọc màn hình có thể chỉ thấy
// một khung tin nhắn bị trượt đi, nhưng cùng một tin vẫn ra cùng một mã. Tin
// lặp lại y hệt (ví dụ "ok") được phân biệt bằng số lần xuất hiện.
func ingestMessageID(convID, sender, content string, occurrence int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", convID, sender, content, occurrence)))
	return "ingest-" + hex.EncodeToString(h[:12])
}

// Ingest lưu các cuộc chat được đẩy vào một kênh webhook, rồi kích hoạt các
// job "after_sync" giống như một lần đồng bộ bình thường.
func (s *SyncEngine) Ingest(ctx context.Context, channel models.Channel, p IngestPayload) (*IngestResult, error) {
	if channel.ChannelType != channels.WebhookChannelType {
		return nil, fmt.Errorf("channel %s is not a webhook channel", channel.ID)
	}
	convs, msgs, err := NormalizeIngest(p, time.Now())
	if err != nil {
		return nil, err
	}

	res := &IngestResult{}
	for i, conv := range convs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		convID, err := s.upsertConversation(channel.TenantID, channel.ID, conv)
		if err != nil {
			return res, fmt.Errorf("save conversation %s: %w", conv.ExternalID, err)
		}
		saved := 0
		for _, m := range msgs[i] {
			if err := s.upsertMessage(channel.TenantID, convID, m); err != nil {
				log.Printf("[ingest] error saving message %s: %v", m.ExternalID, err)
				continue
			}
			saved++
		}
		var count int64
		db.DB.Model(&models.Message{}).Where("conversation_id = ?", convID).Count(&count)
		db.DB.Model(&models.Conversation{}).Where("id = ?", convID).Update("message_count", count)
		res.Conversations = append(res.Conversations, IngestedConversation{ID: convID, ExternalID: conv.ExternalID, Messages: saved})
		res.Messages += saved
	}

	db.LogActivity(channel.TenantID, "", "system", "sync.completed", "channel", channel.ID,
		fmt.Sprintf("Ingest '%s': %d conversations, %d messages", channel.Name, len(res.Conversations), res.Messages), "", "")
	if sched := GetDefaultScheduler(); sched != nil {
		sched.TriggerAfterSyncJobs(channel.TenantID, channel.ID)
	}
	_ = s.updateSyncStatus(channel.ID, "success", "")
	return res, nil
}

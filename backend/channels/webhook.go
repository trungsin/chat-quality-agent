package channels

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"time"
)

// WebhookChannelType là kênh nhận dữ liệu do hệ thống ngoài đẩy vào (CRM,
// tổng đài chat, browser agent...) thay vì CQA tự kéo về. Dữ liệu đi qua
// POST /api/v1/ingest/:channelId hoặc tool MCP cqa_ingest_conversation.
const WebhookChannelType = "webhook"

// WebhookCredentials giữ token mà hệ thống ngoài dùng để đẩy dữ liệu.
type WebhookCredentials struct {
	IngestToken string `json:"ingest_token"`
}

// NewIngestToken sinh token ngẫu nhiên 32 byte.
func NewIngestToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "cqa_in_" + hex.EncodeToString(b), nil
}

// Matches so token theo thời gian hằng để không lộ token qua đo thời gian.
func (c WebhookCredentials) Matches(token string) bool {
	if c.IngestToken == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.IngestToken), []byte(token)) == 1
}

// WebhookAdapter không kéo gì về: dữ liệu được đẩy vào, nên đồng bộ định kỳ
// chỉ là thao tác rỗng.
type WebhookAdapter struct{}

func (WebhookAdapter) FetchRecentConversations(ctx context.Context, since time.Time, limit int) ([]SyncedConversation, error) {
	return nil, nil
}

func (WebhookAdapter) FetchMessages(ctx context.Context, conversationID string, since time.Time) ([]SyncedMessage, error) {
	return nil, nil
}

func (WebhookAdapter) HealthCheck(ctx context.Context) error { return nil }

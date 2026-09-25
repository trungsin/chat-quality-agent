package engine

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeIngestAssignsStableIDsAndOrder(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	p := IngestPayload{Conversations: []IngestConversation{{
		ExternalID: "ticket-1",
		Messages: []IngestMessage{
			{SenderType: "Customer", SenderName: "Lan", Content: "Giá bao nhiêu?"},
			{SenderType: "agent", Content: "ok"},
			{SenderType: "agent", Content: "ok"},
		},
	}}}
	convs, msgs, err := NormalizeIngest(p, now)
	if err != nil {
		t.Fatal(err)
	}
	if convs[0].CustomerName != "Lan" {
		t.Fatalf("customer name should fall back to first customer sender, got %q", convs[0].CustomerName)
	}
	m := msgs[0]
	if m[0].SenderType != "customer" || m[0].ContentType != "text" {
		t.Fatalf("sender/content type not normalized: %+v", m[0])
	}
	if !(m[0].SentAt.Before(m[1].SentAt) && m[1].SentAt.Before(m[2].SentAt)) {
		t.Fatal("messages without sent_at must keep payload order")
	}
	if !convs[0].LastMessageAt.Equal(m[2].SentAt) {
		t.Fatal("last_message_at must be the newest message")
	}
	if m[1].ExternalID == m[2].ExternalID {
		t.Fatal("repeated identical messages need distinct IDs")
	}

	// Đọc lại một khung tin nhắn bị trượt: cùng tin phải ra cùng mã.
	shifted := IngestPayload{Conversations: []IngestConversation{{
		ExternalID: "ticket-1",
		Messages:   p.Conversations[0].Messages[1:],
	}}}
	_, again, err := NormalizeIngest(shifted, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again[0][0].ExternalID != m[1].ExternalID || again[0][1].ExternalID != m[2].ExternalID {
		t.Fatal("generated IDs must not depend on position in the payload")
	}
}

func TestNormalizeIngestRejectsBadInput(t *testing.T) {
	cases := map[string]IngestPayload{
		"empty":       {},
		"no id":       {Conversations: []IngestConversation{{Messages: []IngestMessage{{SenderType: "agent", Content: "x"}}}}},
		"no messages": {Conversations: []IngestConversation{{ExternalID: "a"}}},
		"bad sender":  {Conversations: []IngestConversation{{ExternalID: "a", Messages: []IngestMessage{{SenderType: "bot", Content: "x"}}}}},
		"no content":  {Conversations: []IngestConversation{{ExternalID: "a", Messages: []IngestMessage{{SenderType: "agent", Content: "  "}}}}},
	}
	for name, p := range cases {
		if _, _, err := NormalizeIngest(p, time.Now()); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	many := IngestPayload{Conversations: []IngestConversation{{ExternalID: "a"}}}
	for i := 0; i <= MaxIngestMessages; i++ {
		many.Conversations[0].Messages = append(many.Conversations[0].Messages, IngestMessage{SenderType: "agent", Content: "x"})
	}
	if _, _, err := NormalizeIngest(many, time.Now()); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("expected message limit error, got %v", err)
	}
}

package mcp

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestBuildCustomerProfile(t *testing.T) {
	t1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(48 * time.Hour)
	convs := []models.Conversation{
		{ID: "c1", ChannelID: "ch1", ExternalUserID: "crm-42", CustomerName: "Lan", MessageCount: 4, LastMessageAt: &t2, CreatedAt: t1},
		{ID: "c2", ChannelID: "ch2", ExternalUserID: "crm-42", CustomerName: "Lan", MessageCount: 2, LastMessageAt: &t1, CreatedAt: t1.Add(time.Hour)},
	}
	results := []models.JobResult{
		{ConversationID: "c1", ResultType: "classification_tag", RuleName: "Hỏi giá", Confidence: 0.9},
		{ConversationID: "c2", ResultType: "classification_tag", RuleName: "Hỏi giá", Confidence: 0.7},
		{ConversationID: "c2", ResultType: "classification_tag", RuleName: "Khiếu nại", Confidence: 1},
		{ConversationID: "c1", ResultType: "conversation_evaluation", Severity: "PASS", Detail: `{"score":90}`},
		{ConversationID: "c2", ResultType: "conversation_evaluation", Severity: "FAIL", Detail: `{"score":40}`},
		// Bản ghi "đánh giá" của job phân loại không có điểm — không được tính vào chất lượng.
		{ConversationID: "c2", ResultType: "conversation_evaluation", Severity: "PASS", Detail: `{"summary":"x"}`},
		{ConversationID: "c2", ResultType: "qc_violation", Severity: "NGHIEM_TRONG", RuleName: "Chào hỏi", CreatedAt: t2},
	}
	p := BuildCustomerProfile(convs, map[string]string{"ch1": "Zalo", "ch2": "CRM"}, results)

	if p.Conversations != 2 || p.Messages != 6 || p.ExternalUserID != "crm-42" {
		t.Fatalf("bad totals: %+v", p)
	}
	if !p.FirstContactAt.Equal(t1) || !p.LastContactAt.Equal(t2) {
		t.Fatalf("bad contact range: %v %v", p.FirstContactAt, p.LastContactAt)
	}
	if len(p.Channels) != 2 {
		t.Fatalf("channels: %v", p.Channels)
	}
	if len(p.Segments) != 2 || p.Segments[0].Tag != "Hỏi giá" || p.Segments[0].Count != 2 || p.Segments[0].AvgConfidence != 0.8 {
		t.Fatalf("segments: %+v", p.Segments)
	}
	q := p.Quality
	if q.Evaluations != 2 || q.Passed != 1 || q.Failed != 1 || q.PassRate != 0.5 || q.AvgScore != 65 {
		t.Fatalf("quality: %+v", q)
	}
	if q.Violations["NGHIEM_TRONG"] != 1 || len(p.RecentIssues) != 1 {
		t.Fatalf("issues: %+v %+v", q.Violations, p.RecentIssues)
	}
}

func TestToolListIncludesCustomerTools(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range getAllTools() {
		names[tool.Name] = true
	}
	for _, n := range []string{"cqa_ingest_conversation", "cqa_get_customer_profile", "cqa_list_customers_by_tag", "cqa_list_tenants"} {
		if !names[n] {
			t.Errorf("missing tool %s", n)
		}
	}
}

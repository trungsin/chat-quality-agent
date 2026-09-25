package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

// Tool cho tích hợp CRM / nền tảng chat: đẩy cuộc chat vào CQA và đọc lại hồ
// sơ khách hàng (nhãn phân loại + chất lượng CSKH) để ghi ngược về CRM.

func customerTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "cqa_ingest_conversation",
			Description: "Push one conversation from a CRM, chat platform or browser agent into a webhook channel. " +
				"Messages are deduplicated, and after_sync QC/classification jobs run automatically.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id":        {Type: "string", Description: "Tenant UUID"},
					"channel_id":       {Type: "string", Description: "UUID of a channel with channel_type=webhook"},
					"external_id":      {Type: "string", Description: "Stable conversation ID in the source system (ticket ID, thread ID, URL)"},
					"external_user_id": {Type: "string", Description: "Customer ID in the source system (CRM contact ID, phone)"},
					"customer_name":    {Type: "string", Description: "Customer display name"},
					"messages": {Type: "array", Items: &Property{Type: "object", Description: "Message"}, Description: "Ordered messages: [{sender_type: customer|agent|system, " +
						"sender_name, content, sent_at (RFC3339, optional), external_id (optional)}]"},
					"metadata": {Type: "object", Description: "Optional source metadata (crm, url, pipeline stage...)"},
				},
				Required: []string{"tenant_id", "channel_id", "external_id", "messages"},
			},
		},
		{
			Name: "cqa_get_customer_profile",
			Description: "Customer profile for CRM sync: conversations, classification tags (segments) with counts, " +
				"and customer-service quality (evaluations, pass rate, average score, violations).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id":        {Type: "string", Description: "Tenant UUID"},
					"external_user_id": {Type: "string", Description: "Customer ID in the source system"},
					"customer_name":    {Type: "string", Description: "Exact customer name (used when external_user_id is empty)"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_list_customers_by_tag",
			Description: "List customers whose conversations were classified with a tag (e.g. 'Khiếu nại', 'Hỏi giá'), newest first.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
					"tag":       {Type: "string", Description: "Classification rule name"},
					"since":     {Type: "string", Description: "ISO8601 date filter on classification time"},
					"limit":     {Type: "string", Description: "Max customers (default 50)"},
				},
				Required: []string{"tenant_id", "tag"},
			},
		},
	}
}

// mcpCanWrite áp cùng quy tắc với middleware.RequirePermission: owner/admin
// được ghi, member cần quyền "w" trên tài nguyên.
func mcpCanWrite(userID, tenantID, resource string) bool {
	var ut models.UserTenant
	if db.DB.Where("user_id = ? AND tenant_id = ?", userID, tenantID).First(&ut).Error != nil {
		return false
	}
	if ut.Role == "owner" || ut.Role == "admin" {
		return true
	}
	var perms map[string]string
	if json.Unmarshal([]byte(ut.Permissions), &perms) != nil {
		return false
	}
	return strings.Contains(perms[resource], "w")
}

func toolIngestConversation(ctx context.Context, userID, tenantID string, args map[string]interface{}) (interface{}, *RPCError) {
	if !mcpCanWrite(userID, tenantID, "channels") {
		return errResult("access denied: channels write permission required")
	}
	channelID, _ := args["channel_id"].(string)
	var channel models.Channel
	if err := db.DB.Where("id = ? AND tenant_id = ?", channelID, tenantID).First(&channel).Error; err != nil {
		return errResult("Channel not found")
	}
	if channel.ChannelType != channels.WebhookChannelType {
		return errResult("channel must have channel_type=webhook")
	}
	if !channel.IsActive {
		return errResult("channel is inactive")
	}

	// Đối số MCP là JSON tuỳ ý; đi vòng qua JSON để dùng cùng kiểm tra với API ingest.
	raw, _ := json.Marshal(args)
	var conv engine.IngestConversation
	if err := json.Unmarshal(raw, &conv); err != nil {
		return errResult("invalid arguments: " + err.Error())
	}
	cfg, _ := config.Load()
	res, err := engine.NewSyncEngine(cfg).Ingest(ctx, channel, engine.IngestPayload{Conversations: []engine.IngestConversation{conv}})
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(res)
}

// CustomerProfile là bản tóm tắt một khách hàng để ghi ngược vào CRM.
type CustomerProfile struct {
	ExternalUserID string         `json:"external_user_id,omitempty"`
	CustomerName   string         `json:"customer_name,omitempty"`
	Conversations  int            `json:"conversations"`
	Messages       int            `json:"messages"`
	FirstContactAt *time.Time     `json:"first_contact_at,omitempty"`
	LastContactAt  *time.Time     `json:"last_contact_at,omitempty"`
	Channels       []string       `json:"channels"`
	Segments       []TagCount     `json:"segments"`
	Quality        QualitySummary `json:"quality"`
	RecentIssues   []IssueSummary `json:"recent_issues"`
}

type TagCount struct {
	Tag           string  `json:"tag"`
	Count         int     `json:"count"`
	AvgConfidence float64 `json:"avg_confidence"`
}

type QualitySummary struct {
	Evaluations int            `json:"evaluations"`
	Passed      int            `json:"passed"`
	Failed      int            `json:"failed"`
	PassRate    float64        `json:"pass_rate"`
	AvgScore    float64        `json:"avg_score"`
	Violations  map[string]int `json:"violations_by_severity"`
}

type IssueSummary struct {
	ConversationID string    `json:"conversation_id"`
	Severity       string    `json:"severity"`
	Rule           string    `json:"rule"`
	Evidence       string    `json:"evidence"`
	At             time.Time `json:"at"`
}

// BuildCustomerProfile gộp kết quả phân tích theo khách hàng. Tách riêng khỏi
// truy vấn DB để kiểm thử được.
func BuildCustomerProfile(convs []models.Conversation, channelNames map[string]string, results []models.JobResult) CustomerProfile {
	p := CustomerProfile{Channels: []string{}, Segments: []TagCount{}, RecentIssues: []IssueSummary{},
		Quality: QualitySummary{Violations: map[string]int{}}}
	seenChannel := map[string]bool{}
	for i := range convs {
		c := convs[i]
		p.Conversations++
		p.Messages += c.MessageCount
		if p.ExternalUserID == "" {
			p.ExternalUserID = c.ExternalUserID
		}
		if p.CustomerName == "" {
			p.CustomerName = c.CustomerName
		}
		if c.LastMessageAt != nil {
			if p.LastContactAt == nil || c.LastMessageAt.After(*p.LastContactAt) {
				p.LastContactAt = c.LastMessageAt
			}
		}
		created := c.CreatedAt
		if p.FirstContactAt == nil || created.Before(*p.FirstContactAt) {
			p.FirstContactAt = &created
		}
		if name := channelNames[c.ChannelID]; name != "" && !seenChannel[name] {
			seenChannel[name] = true
			p.Channels = append(p.Channels, name)
		}
	}

	tags := map[string]*TagCount{}
	scoreSum, scored := 0.0, 0
	for _, r := range results {
		switch r.ResultType {
		case "classification_tag":
			t := tags[r.RuleName]
			if t == nil {
				t = &TagCount{Tag: r.RuleName}
				tags[r.RuleName] = t
			}
			t.Count++
			t.AvgConfidence += r.Confidence
		case "conversation_evaluation":
			var d struct {
				Score *float64 `json:"score"`
			}
			if json.Unmarshal([]byte(r.Detail), &d) != nil || d.Score == nil {
				continue // bản ghi của job phân loại, không phải đánh giá QC
			}
			p.Quality.Evaluations++
			switch r.Severity {
			case "PASS":
				p.Quality.Passed++
			case "FAIL":
				p.Quality.Failed++
			}
			scoreSum += *d.Score
			scored++
		case "qc_violation":
			p.Quality.Violations[r.Severity]++
			p.RecentIssues = append(p.RecentIssues, IssueSummary{r.ConversationID, r.Severity, r.RuleName, r.Evidence, r.CreatedAt})
		}
	}
	for _, t := range tags {
		t.AvgConfidence = round2(t.AvgConfidence / float64(t.Count))
		p.Segments = append(p.Segments, *t)
	}
	sort.Slice(p.Segments, func(i, j int) bool {
		if p.Segments[i].Count != p.Segments[j].Count {
			return p.Segments[i].Count > p.Segments[j].Count
		}
		return p.Segments[i].Tag < p.Segments[j].Tag
	})
	if judged := p.Quality.Passed + p.Quality.Failed; judged > 0 {
		p.Quality.PassRate = round2(float64(p.Quality.Passed) / float64(judged))
	}
	if scored > 0 {
		p.Quality.AvgScore = round2(scoreSum / float64(scored))
	}
	sort.Slice(p.RecentIssues, func(i, j int) bool { return p.RecentIssues[i].At.After(p.RecentIssues[j].At) })
	if len(p.RecentIssues) > 10 {
		p.RecentIssues = p.RecentIssues[:10]
	}
	return p
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func toolGetCustomerProfile(tenantID string, args map[string]interface{}) (interface{}, *RPCError) {
	userKey, _ := args["external_user_id"].(string)
	name, _ := args["customer_name"].(string)
	q := db.DB.Where("tenant_id = ?", tenantID)
	switch {
	case strings.TrimSpace(userKey) != "":
		q = q.Where("external_user_id = ?", strings.TrimSpace(userKey))
	case strings.TrimSpace(name) != "":
		q = q.Where("customer_name = ?", strings.TrimSpace(name))
	default:
		return errResult("external_user_id or customer_name is required")
	}
	var convs []models.Conversation
	q.Order("last_message_at DESC").Limit(500).Find(&convs)
	if len(convs) == 0 {
		return errResult("Customer not found")
	}

	ids := make([]string, len(convs))
	for i, c := range convs {
		ids[i] = c.ID
	}
	var chans []models.Channel
	db.DB.Where("tenant_id = ?", tenantID).Find(&chans)
	names := map[string]string{}
	for _, ch := range chans {
		names[ch.ID] = ch.Name
	}
	var results []models.JobResult
	db.DB.Select("id, conversation_id, result_type, severity, rule_name, evidence, detail, confidence, created_at").
		Where("tenant_id = ? AND conversation_id IN ?", tenantID, ids).Find(&results)
	return jsonResult(BuildCustomerProfile(convs, names, results))
}

func toolListCustomersByTag(tenantID string, args map[string]interface{}) (interface{}, *RPCError) {
	tag, _ := args["tag"].(string)
	if strings.TrimSpace(tag) == "" {
		return errResult("tag is required")
	}
	limit := getLimit(args, 50)
	q := db.DB.Table("job_results").
		Select("conversations.external_user_id, conversations.customer_name, COUNT(DISTINCT job_results.conversation_id) AS conversations, MAX(job_results.created_at) AS last_tagged_at").
		Joins("JOIN conversations ON conversations.id = job_results.conversation_id").
		Where("job_results.tenant_id = ? AND job_results.result_type = 'classification_tag' AND job_results.rule_name = ?", tenantID, tag)
	if since, ok := args["since"].(string); ok && since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			q = q.Where("job_results.created_at > ?", t)
		}
	}
	type row struct {
		ExternalUserID string    `json:"external_user_id"`
		CustomerName   string    `json:"customer_name"`
		Conversations  int       `json:"conversations"`
		LastTaggedAt   time.Time `json:"last_tagged_at"`
	}
	var rows []row
	q.Group("conversations.external_user_id, conversations.customer_name").
		Order("last_tagged_at DESC").Limit(limit).Scan(&rows)
	return jsonResult(map[string]interface{}{"tag": tag, "customers": rows})
}

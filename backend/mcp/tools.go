package mcp

// ToolDefinition describes an MCP tool.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]Property    `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

type Property struct {
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Items       *Property `json:"items,omitempty"`
}

func handleToolsList() interface{} {
	return map[string]interface{}{
		"tools": getAllTools(),
	}
}

func getAllTools() []ToolDefinition {
	return append(coreTools(), customerTools()...)
}

func coreTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "cqa_list_tenants",
			Description: "List all companies the user has access to, with summary stats (channels, jobs, conversations).",
			InputSchema: InputSchema{Type: "object"},
		},
		{
			Name:        "cqa_get_tenant",
			Description: "Get details of a specific company including settings and stats overview.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_list_channels",
			Description: "List chat channels for a company with status, last sync time, and message count.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_list_conversations",
			Description: "List conversations, optionally filtered by channel, date range, or customer name.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id":  {Type: "string", Description: "Tenant UUID"},
					"channel_id": {Type: "string", Description: "Optional channel filter"},
					"since":      {Type: "string", Description: "ISO8601 date, filter conversations updated after this"},
					"limit":      {Type: "string", Description: "Max results (default 20)"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_get_messages",
			Description: "Get messages for a specific conversation with pagination.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id":       {Type: "string", Description: "Tenant UUID"},
					"conversation_id": {Type: "string", Description: "Conversation UUID"},
					"limit":           {Type: "string", Description: "Max results (default 50)"},
				},
				Required: []string{"tenant_id", "conversation_id"},
			},
		},
		{
			Name:        "cqa_search_messages",
			Description: "Search messages by keyword across all conversations.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
					"query":     {Type: "string", Description: "Search keyword"},
					"limit":     {Type: "string", Description: "Max results (default 20)"},
				},
				Required: []string{"tenant_id", "query"},
			},
		},
		{
			Name:        "cqa_list_jobs",
			Description: "List analysis jobs for a company with status and last run info.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_get_job_results",
			Description: "Get analysis results for a specific job run.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id":  {Type: "string", Description: "Tenant UUID"},
					"job_run_id": {Type: "string", Description: "Job run UUID"},
				},
				Required: []string{"tenant_id", "job_run_id"},
			},
		},
		{
			Name:        "cqa_search_violations",
			Description: "Search QC violations by severity, date, channel, or keyword.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
					"severity":  {Type: "string", Description: "NGHIEM_TRONG or CAN_CAI_THIEN"},
					"since":     {Type: "string", Description: "ISO8601 date filter"},
					"limit":     {Type: "string", Description: "Max results (default 20)"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_get_stats",
			Description: "Get overall statistics: conversations, issues, tags by time period.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
					"period":    {Type: "string", Description: "today, week, month (default: today)"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_get_notification_logs",
			Description: "Get notification history filtered by date, channel type, or status.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
					"status":    {Type: "string", Description: "sent or failed"},
					"limit":     {Type: "string", Description: "Max results (default 20)"},
				},
				Required: []string{"tenant_id"},
			},
		},
		{
			Name:        "cqa_trigger_job",
			Description: "Manually trigger an analysis job to run immediately.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"tenant_id": {Type: "string", Description: "Tenant UUID"},
					"job_id":    {Type: "string", Description: "Job UUID to trigger"},
				},
				Required: []string{"tenant_id", "job_id"},
			},
		},
	}
}

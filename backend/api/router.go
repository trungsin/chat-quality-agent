package api

import (
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/handlers"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/mcp"
	"github.com/vietbui/chat-quality-agent/storage"
)

func SetupRouter(cfg *config.Config) *gin.Engine {
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Serve static frontend files in production
	if cfg.IsProduction() {
		r.Static("/assets", "./static/assets")
		r.Static("/guides", "./static/guides")
		r.StaticFile("/favicon.png", "./static/favicon.png")
		r.StaticFile("/", "./static/index.html")
		r.NoRoute(func(c *gin.Context) {
			// SPA fallback: serve index.html for non-API routes
			if len(c.Request.URL.Path) > 4 && c.Request.URL.Path[:4] == "/api" {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			c.File("./static/index.html")
		})
	}

	// Serve uploaded files (requires JWT auth + tenant ownership)
	r.GET("/api/v1/files/*filepath", middleware.JWTAuth(), func(c *gin.Context) {
		fp := c.Param("filepath")
		// Security: clean path and verify it stays within base directory
		cleanPath := filepath.Clean(fp)
		if strings.Contains(cleanPath, "..") || strings.HasPrefix(cleanPath, "/") && strings.Contains(cleanPath[1:], "..") {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		// Security: verify user belongs to the tenant owning this file
		// Path structure: /{tenantID}/{convID}/{filename}
		pathParts := strings.SplitN(strings.TrimPrefix(cleanPath, "/"), "/", 3)
		if len(pathParts) < 1 || pathParts[0] == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		fileTenantID := pathParts[0]
		userID := middleware.GetUserID(c)
		var count int64
		db.DB.Model(&models.UserTenant{}).Where("user_id = ? AND tenant_id = ?", userID, fileTenantID).Count(&count)
		if count == 0 {
			log.Printf("[security] tenant access denied: user=%s tenant=%s ip=%s path=%s", userID, fileTenantID, c.ClientIP(), fp)
			c.JSON(http.StatusForbidden, gin.H{"error": "tenant_access_denied"})
			return
		}
		// Đọc từ nơi cất file đang cấu hình. Với S3, lớp đọc dự phòng sẽ tìm
		// tiếp trên đĩa khi object chưa được chuyển lên.
		key := strings.TrimPrefix(cleanPath, "/")
		store, err := storage.ForTenant(fileTenantID)
		if err != nil {
			log.Printf("[storage] không lấy được nơi cất file của công ty %s: %v", fileTenantID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "storage_error"})
			return
		}
		body, contentType, size, err := store.Get(c.Request.Context(), key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			log.Printf("[storage] đọc file %s hỏng: %v", key, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "storage_error"})
			return
		}
		defer body.Close()
		c.DataFromReader(http.StatusOK, size, contentType, body, nil)
	})

	// CORS
	r.Use(corsMiddleware(cfg))

	// Rate limiting (500 req/min per IP by default)
	r.Use(middleware.RateLimit(cfg.RateLimitPerIP))

	// Security headers
	r.Use(securityHeaders())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": handlers.AppVersion})
	})

	// Public API
	api := r.Group("/api/v1")
	{
		// Version check (public, no auth required)
		api.GET("/version/check", handlers.CheckVersion)

		// Initial setup (only works when no users exist)
		api.GET("/setup/status", handlers.SetupStatus)
		api.POST("/setup", handlers.Setup)

		auth := api.Group("/auth")
		{
			auth.POST("/login", handlers.Login)
			auth.POST("/refresh", handlers.RefreshTokenHandler)
			auth.POST("/logout", handlers.Logout)
		}
	}

	// Ingest từ hệ thống ngoài (CRM, nền tảng chat, browser agent) — xác thực
	// bằng ingest token của kênh webhook, không dùng JWT.
	api.POST("/ingest/:channelId", handlers.IngestConversations)

	// OAuth callbacks (public — platforms redirect here)
	api.GET("/channels/zalo/callback", handlers.ZaloOAuthCallback)
	api.GET("/channels/facebook/callback", handlers.FacebookOAuthCallback)

	// Authenticated API
	authed := api.Group("")
	authed.Use(middleware.JWTAuth())
	{
		authed.GET("/profile", handlers.GetProfile)
		authed.PUT("/profile", handlers.UpdateProfile)
		authed.PUT("/profile/password", handlers.ChangeProfilePassword)

		// Tenants (list + create don't need tenant context)
		authed.GET("/tenants", handlers.ListTenants)
		authed.POST("/tenants", handlers.CreateTenant)

		// Tenant-scoped routes
		tenant := authed.Group("/tenants/:tenantId")
		tenant.Use(middleware.TenantContext())
		{
			tenant.GET("", handlers.GetTenant)
			tenant.GET("/me", handlers.GetTenantMe)
			tenant.PUT("", middleware.RequireRole("owner", "admin"), handlers.UpdateTenant)
			tenant.DELETE("", middleware.RequireRole("owner"), handlers.DeleteTenant)

			// Channels
			tenant.GET("/channels", middleware.RequirePermission("channels", "r"), handlers.ListChannels)
			tenant.POST("/channels", middleware.RequirePermission("channels", "w"), handlers.CreateChannel)
			tenant.GET("/channels/:channelId", middleware.RequirePermission("channels", "r"), handlers.GetChannel)
			tenant.PUT("/channels/:channelId", middleware.RequirePermission("channels", "w"), handlers.UpdateChannel)
			tenant.DELETE("/channels/:channelId", middleware.RequirePermission("channels", "d"), handlers.DeleteChannel)
			tenant.POST("/channels/:channelId/test", middleware.RequirePermission("channels", "r"), handlers.TestChannelConnection)
			tenant.POST("/channels/:channelId/sync", middleware.RequirePermission("channels", "w"), handlers.SyncChannelNow)
			tenant.POST("/channels/:channelId/reauth", middleware.RequirePermission("channels", "w"), handlers.ReauthChannel)
			tenant.GET("/channels/:channelId/sync-history", middleware.RequirePermission("channels", "r"), handlers.GetChannelSyncHistory)
			tenant.DELETE("/channels/:channelId/conversations", middleware.RequirePermission("channels", "d"), handlers.PurgeChannelConversations)

			// Conversations & Messages
			tenant.GET("/onboarding-status", handlers.GetOnboardingStatus)
			tenant.GET("/conversations", middleware.RequirePermission("messages", "r"), handlers.ListConversations)
			tenant.GET("/conversations/export", middleware.RequirePermission("messages", "w"), handlers.ExportMessages)
			tenant.GET("/conversations/evaluated", middleware.RequirePermission("messages", "r"), handlers.ListEvaluatedConversations)
			tenant.GET("/conversations/:conversationId/messages", middleware.RequirePermission("messages", "r"), handlers.GetConversationMessages)
			tenant.GET("/conversations/:conversationId/evaluations", middleware.RequirePermission("messages", "r"), handlers.GetConversationEvaluations)
			tenant.GET("/conversations/:conversationId/page", middleware.RequirePermission("messages", "r"), handlers.GetConversationPage)

			// Dashboard
			tenant.GET("/dashboard", handlers.GetDashboard)

			// Jobs
			tenant.GET("/jobs", middleware.RequirePermission("jobs", "r"), handlers.ListJobs)
			tenant.POST("/jobs", middleware.RequirePermission("jobs", "w"), handlers.CreateJob)
			tenant.GET("/jobs/:jobId", middleware.RequirePermission("jobs", "r"), handlers.GetJob)
			tenant.PUT("/jobs/:jobId", middleware.RequirePermission("jobs", "w"), handlers.UpdateJob)
			tenant.DELETE("/jobs/:jobId", middleware.RequirePermission("jobs", "d"), handlers.DeleteJob)
			tenant.POST("/jobs/:jobId/trigger", middleware.RequirePermission("jobs", "w"), handlers.TriggerJob)
			tenant.POST("/jobs/:jobId/test-run", middleware.RequirePermission("jobs", "w"), handlers.TestRunJob)
			tenant.POST("/jobs/:jobId/cancel", middleware.RequirePermission("jobs", "w"), handlers.CancelJob)
			tenant.GET("/jobs/:jobId/runs", middleware.RequirePermission("jobs", "r"), handlers.ListJobRuns)
			tenant.GET("/jobs/:jobId/runs/:runId/results", middleware.RequirePermission("jobs", "r"), handlers.ListJobResults)
			tenant.POST("/test-output", middleware.RequirePermission("jobs", "w"), handlers.TestOutput)

			// Activity Logs — nhật ký có email và IP đăng nhập của người dùng,
			// khoá theo cùng quyền mà menu bên giao diện vốn đã dùng để ẩn/hiện.
			tenant.GET("/activity-logs", middleware.RequirePermission("settings", "r"), handlers.ListActivityLogs)

			// Cost Logs
			tenant.GET("/cost-logs", middleware.RequirePermission("settings", "r"), handlers.ListCostLogs)

			// Users (tenant members management)
			tenant.GET("/users", handlers.ListTenantUsers)
			tenant.POST("/users/invite", middleware.RequireRole("owner", "admin"), handlers.InviteUser)
			tenant.PUT("/users/:userId/role", middleware.RequireRole("owner"), handlers.UpdateUserRole)
			tenant.PUT("/users/:userId/reset-password", middleware.RequireRole("owner", "admin"), handlers.ResetUserPassword)
			tenant.DELETE("/users/:userId", middleware.RequireRole("owner"), handlers.RemoveUserFromTenant)

			// Job all results + export
			tenant.GET("/jobs/:jobId/results", middleware.RequirePermission("jobs", "r"), handlers.ListAllJobResults)
			tenant.GET("/jobs/:jobId/results/export", middleware.RequirePermission("jobs", "r"), handlers.ExportJobResults)
			tenant.DELETE("/jobs/:jobId/results", middleware.RequirePermission("jobs", "d"), handlers.ClearJobResults)
			tenant.DELETE("/jobs/:jobId/runs", middleware.RequirePermission("jobs", "d"), handlers.ClearJobRuns)

			// Kết quả toàn công ty — lọc và phân trang chạy dưới database
			tenant.GET("/results", middleware.RequirePermission("jobs", "r"), handlers.ListResults)
			tenant.GET("/results/facets", middleware.RequirePermission("jobs", "r"), handlers.ResultsFacets)
			tenant.GET("/results/export", middleware.RequirePermission("jobs", "r"), handlers.ExportResults)

			// Settings
			tenant.GET("/settings", middleware.RequirePermission("settings", "r"), handlers.GetSettings)
			tenant.PUT("/settings", middleware.RequirePermission("settings", "w"), handlers.SaveSetting)
			tenant.PUT("/settings/ai", middleware.RequirePermission("settings", "w"), handlers.SaveAISettings)
			tenant.PUT("/settings/analysis", middleware.RequirePermission("settings", "w"), handlers.SaveAnalysisSettings)
			tenant.GET("/settings/storage", middleware.RequirePermission("settings", "r"), handlers.GetStorageStatus)
			tenant.PUT("/settings/storage", middleware.RequirePermission("settings", "w"), handlers.SaveStorageSettings)
			tenant.POST("/settings/storage/test", middleware.RequirePermission("settings", "w"), handlers.TestStorageSettings)
			tenant.POST("/settings/ai/test", middleware.RequirePermission("settings", "w"), handlers.TestAIKey)
			tenant.GET("/settings/ai/models", middleware.RequirePermission("settings", "r"), handlers.ListAIModels)
			tenant.POST("/settings/ai/models/refresh", middleware.RequirePermission("settings", "w"), handlers.RefreshAIModels)
			tenant.PUT("/settings/general", middleware.RequirePermission("settings", "w"), handlers.SaveGeneralSettings)
			tenant.PUT("/settings/password", handlers.ChangePassword)

			// Notification logs
			tenant.GET("/notification-logs", handlers.ListNotificationLogs)

			// Demo data
			tenant.GET("/demo/status", handlers.GetDemoStatus)
			tenant.POST("/demo/import", middleware.RequireRole("owner", "admin"), handlers.ImportDemoData)
			tenant.DELETE("/demo/reset", middleware.RequireRole("owner", "admin"), handlers.ResetDemoData)
		}
	}

	// Agent API (for Company OS integration, requires auth)
	agentAPI := api.Group("/agents", middleware.JWTAuth())
	{
		agentAPI.GET("", handlers.ListAgents)
		agentAPI.GET("/capabilities", handlers.ListAgents)
		agentAPI.POST("/:agentName/run", handlers.AgentRun)
		agentAPI.GET("/:agentName/query", handlers.AgentQuery)
		agentAPI.GET("/:agentName/health", handlers.AgentHealth)
	}

	// MCP Server (Streamable HTTP transport)
	mcp.SetupMCPRoutes(r)

	// OAuth 2.0 (for Claude Web MCP authentication)
	mcp.SetupOAuthRoutes(r)

	// MCP Client management (requires JWT auth)
	mcpAPI := api.Group("/mcp/clients", middleware.JWTAuth())
	{
		mcpAPI.GET("", mcp.ListMCPClients)
		mcpAPI.POST("", mcp.CreateMCPClient)
		mcpAPI.DELETE("/:id", mcp.DeleteMCPClient)
	}

	return r
}

func corsMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestOrigin := c.GetHeader("Origin")
		allowedOrigin := requestOrigin

		if cfg.IsProduction() && requestOrigin != "" {
			// In production, only allow same-origin or configured origins
			host := c.Request.Host
			if !strings.Contains(requestOrigin, host) {
				allowedOrigin = ""
			}
		}

		if allowedOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; connect-src 'self' https:; font-src 'self' data:; frame-ancestors 'none'")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
	}
}

package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const maxIngestBodyBytes = 5 << 20

// IngestConversations nhận cuộc chat do hệ thống ngoài (CRM, nền tảng chat,
// browser agent) đẩy vào một kênh webhook. Xác thực bằng ingest token của
// kênh, không dùng JWT, để máy chủ ngoài gọi được mà không cần tài khoản.
//
//	POST /api/v1/ingest/:channelId
//	Authorization: Bearer <ingest_token>
func IngestConversations(c *gin.Context) {
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if token == "" {
		token = c.GetHeader("X-CQA-Ingest-Token")
	}
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "ingest_token_required"})
		return
	}

	var channel models.Channel
	if err := db.DB.Where("id = ? AND channel_type = ?", c.Param("channelId"), channels.WebhookChannelType).
		First(&channel).Error; err != nil {
		// Không phân biệt "không có kênh" với "sai token" để tránh dò ID kênh.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_ingest_token"})
		return
	}
	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	var creds channels.WebhookCredentials
	if err != nil || json.Unmarshal(credBytes, &creds) != nil || !creds.Matches(token) {
		log.Printf("[security] ingest auth failed: channel=%s ip=%s", channel.ID, c.ClientIP())
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_ingest_token"})
		return
	}
	if !channel.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "channel_inactive"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxIngestBodyBytes)
	var payload engine.IngestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	result, err := engine.NewSyncEngine(cfg).Ingest(c.Request.Context(), channel, payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ingest_failed", "details": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

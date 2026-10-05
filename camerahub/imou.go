package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Imou is a client for the Imou Open Platform (open.imoulife.com), used for Imou
// cameras that have no reachable IP. Devices must first be bound to the developer
// account (serial + safety code) through the platform or the Imou Life app.
type Imou struct {
	endpoint  string // e.g. https://openapi.easy4ip.com/openapi
	appID     string
	appSecret string
	http      *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func NewImou(endpoint, appID, appSecret string) *Imou {
	if appID == "" || appSecret == "" {
		return nil
	}
	return &Imou{
		endpoint:  strings.TrimRight(endpoint, "/"),
		appID:     appID,
		appSecret: appSecret,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// imouSign implements the platform signature: md5("time:<t>,nonce:<n>,appSecret:<s>").
func imouSign(t int64, nonce, secret string) string {
	sum := md5.Sum([]byte(fmt.Sprintf("time:%d,nonce:%s,appSecret:%s", t, nonce, secret)))
	return hex.EncodeToString(sum[:])
}

type imouError struct{ Code, Msg string }

func (e *imouError) Error() string { return "imou " + e.Code + ": " + e.Msg }

func (c *Imou) call(ctx context.Context, method string, params map[string]any, out any) error {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes)
	now := time.Now().Unix()
	body := map[string]any{
		"system": map[string]any{
			"ver":   "1.0",
			"appId": c.appID,
			"sign":  imouSign(now, nonce, c.appSecret),
			"time":  now,
			"nonce": nonce,
		},
		"id":     nonce,
		"params": params,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res struct {
		Result struct {
			Code string          `json:"code"`
			Msg  string          `json:"msg"`
			Data json.RawMessage `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return fmt.Errorf("imou %s: HTTP %d, phản hồi không hợp lệ", method, resp.StatusCode)
	}
	if res.Result.Code != "0" {
		return &imouError{Code: res.Result.Code, Msg: res.Result.Msg}
	}
	if out != nil && len(res.Result.Data) > 0 {
		return json.Unmarshal(res.Result.Data, out)
	}
	return nil
}

func (c *Imou) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}
	var data struct {
		AccessToken string `json:"accessToken"`
		ExpireTime  int64  `json:"expireTime"` // seconds
	}
	if err := c.call(ctx, "accessToken", map[string]any{}, &data); err != nil {
		return "", err
	}
	if data.AccessToken == "" {
		return "", errors.New("imou: accessToken rỗng")
	}
	ttl := time.Duration(data.ExpireTime) * time.Second
	if ttl <= 0 || ttl > 72*time.Hour {
		ttl = 24 * time.Hour
	}
	c.token = data.AccessToken
	c.tokenExp = time.Now().Add(ttl - 10*time.Minute)
	return c.token, nil
}

type imouStream struct {
	HLS      string `json:"hls"`
	StreamID int    `json:"streamId"` // 0 = HD, 1 = SD
}

// LiveURL returns an HLS URL for the device channel, creating the live
// address on first use. streamID 0 = main (HD), 1 = sub (SD).
func (c *Imou) LiveURL(ctx context.Context, deviceID string, channel, streamID int) (string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}
	ch := fmt.Sprint(channel)
	var data struct {
		Streams []imouStream `json:"streams"`
	}
	err = c.call(ctx, "bindDeviceLive", map[string]any{
		"token": token, "deviceId": deviceID, "channelId": ch, "streamId": streamID,
	}, &data)
	if err != nil {
		// The live address usually already exists: read it instead.
		data.Streams = nil
		if err2 := c.call(ctx, "getLiveStreamInfo", map[string]any{
			"token": token, "deviceId": deviceID, "channelId": ch,
		}, &data); err2 != nil {
			return "", fmt.Errorf("%v; %v", err, err2)
		}
	}
	var fallback string
	for _, s := range data.Streams {
		if s.HLS == "" {
			continue
		}
		if s.StreamID == streamID {
			return s.HLS, nil
		}
		fallback = s.HLS
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", errors.New("imou: không có luồng HLS cho thiết bị " + deviceID)
}

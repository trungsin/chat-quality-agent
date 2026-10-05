package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Hub keeps MediaMTX in sync with the store and tracks stream health.
type Hub struct {
	store    *Store
	mtx      *MediaMTX
	imou     *Imou
	notifier *Telegram

	mu       sync.RWMutex
	status   map[string]PathStatus
	mtxErr   string
	syncErrs map[string]string
	wentDown map[string]time.Time // when an online source went offline
	alerted  map[string]bool
	everUp   map[string]bool
}

func NewHub(store *Store, mtx *MediaMTX, imou *Imou, notifier *Telegram) *Hub {
	return &Hub{
		store: store, mtx: mtx, imou: imou, notifier: notifier,
		status:   map[string]PathStatus{},
		syncErrs: map[string]string{},
		wentDown: map[string]time.Time{},
		alerted:  map[string]bool{},
		everUp:   map[string]bool{},
	}
}

// Apply pushes one source to MediaMTX, resolving cloud URLs when needed.
func (h *Hub) Apply(ctx context.Context, src Source) error {
	resolved := ""
	if src.Kind == KindImouCloud {
		if h.imou == nil {
			return h.setSyncErr(src.ID, fmt.Errorf("chưa cấu hình IMOU_APP_ID / IMOU_APP_SECRET"))
		}
		u, err := h.imou.LiveURL(ctx, src.ImouDeviceID, src.ImouChannel, src.Subtype)
		if err != nil {
			return h.setSyncErr(src.ID, err)
		}
		resolved = u
	}
	return h.setSyncErr(src.ID, h.mtx.UpsertPath(ctx, src.ID, src.PathConfig(resolved)))
}

func (h *Hub) Remove(ctx context.Context, id string) error {
	h.mu.Lock()
	delete(h.syncErrs, id)
	delete(h.wentDown, id)
	delete(h.alerted, id)
	delete(h.everUp, id)
	h.mu.Unlock()
	return h.mtx.DeletePath(ctx, id)
}

func (h *Hub) setSyncErr(id string, err error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.syncErrs[id] = err.Error()
	} else {
		delete(h.syncErrs, id)
	}
	return err
}

// SyncAll re-applies every source.
func (h *Hub) SyncAll(ctx context.Context) {
	for _, src := range h.store.List() {
		if err := h.Apply(ctx, src); err != nil {
			log.Printf("sync %s: %v", src.ID, err)
		}
	}
}

// Run reconciles and monitors until ctx is cancelled. MediaMTX does not persist
// paths added through the API, so missing paths are re-added after a restart.
func (h *Hub) Run(ctx context.Context, every time.Duration) {
	h.SyncAll(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		h.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (h *Hub) tick(ctx context.Context) {
	configured, err := h.mtx.ConfiguredPaths(ctx)
	if err != nil {
		h.mu.Lock()
		h.mtxErr = err.Error()
		h.mu.Unlock()
		log.Printf("mediamtx: %v", err)
		return
	}
	status, err := h.mtx.Paths(ctx)
	if err != nil {
		h.mu.Lock()
		h.mtxErr = err.Error()
		h.mu.Unlock()
		return
	}

	sources := h.store.List()
	for _, src := range sources {
		st := status[src.ID]
		needsApply := !configured[src.ID]
		// Imou cloud live URLs can expire: re-resolve when an always-on stream is down.
		if src.Kind == KindImouCloud && !st.Ready && !src.OnDemand {
			needsApply = true
		}
		if needsApply {
			if err := h.Apply(ctx, src); err != nil {
				log.Printf("reconcile %s: %v", src.ID, err)
			}
		}
	}

	h.mu.Lock()
	h.status = status
	h.mtxErr = ""
	var alerts []string
	now := time.Now()
	for _, src := range sources {
		// On-demand sources are idle when nobody watches; push sources are offline
		// whenever nobody publishes. Only alert on always-on pulled streams.
		if src.OnDemand && len(src.Destinations) == 0 || src.Kind == KindPush {
			continue
		}
		if status[src.ID].Ready {
			if h.alerted[src.ID] {
				alerts = append(alerts, fmt.Sprintf("✅ %s (%s) đã online trở lại", src.Name, src.Site))
			}
			h.everUp[src.ID] = true
			delete(h.wentDown, src.ID)
			delete(h.alerted, src.ID)
			continue
		}
		if !h.everUp[src.ID] {
			continue
		}
		down, ok := h.wentDown[src.ID]
		if !ok {
			h.wentDown[src.ID] = now
			continue
		}
		if !h.alerted[src.ID] && now.Sub(down) >= 90*time.Second {
			h.alerted[src.ID] = true
			alerts = append(alerts, fmt.Sprintf("🔴 %s (%s) mất tín hiệu từ %s", src.Name, src.Site, down.Format("15:04:05 02/01")))
		}
	}
	h.mu.Unlock()

	for _, a := range alerts {
		h.notifier.Send(ctx, a)
	}
}

// SourceView is a source plus its live state, as returned to the UI.
type SourceView struct {
	Source
	Online    bool       `json:"online"`
	ReadyTime *time.Time `json:"readyTime,omitempty"`
	Tracks    []string   `json:"tracks,omitempty"`
	Viewers   int        `json:"viewers"`
	Error     string     `json:"error,omitempty"`
}

func (h *Hub) Views() ([]SourceView, string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	list := h.store.List()
	out := make([]SourceView, 0, len(list))
	for _, src := range list {
		st := h.status[src.ID]
		out = append(out, SourceView{
			Source:    src.Redacted(),
			Online:    st.Ready,
			ReadyTime: st.ReadyTime,
			Tracks:    st.Tracks,
			Viewers:   len(st.Readers),
			Error:     h.syncErrs[src.ID],
		})
	}
	return out, h.mtxErr
}

// Telegram sends alerts to a chat. A nil *Telegram only logs.
type Telegram struct {
	token, chatID string
	http          *http.Client
}

func NewTelegram(token, chatID string) *Telegram {
	if token == "" || chatID == "" {
		return nil
	}
	return &Telegram{token: token, chatID: chatID, http: &http.Client{Timeout: 10 * time.Second}}
}

func (t *Telegram) Send(ctx context.Context, text string) {
	log.Printf("alert: %s", text)
	if t == nil {
		return
	}
	form := url.Values{"chat_id": {t.chatID}, "text": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.telegram.org/bot"+t.token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.http.Do(req)
	if err != nil {
		log.Printf("telegram: %v", err)
		return
	}
	resp.Body.Close()
}

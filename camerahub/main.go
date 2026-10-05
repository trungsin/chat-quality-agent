// Command camerahub is a video gateway: it pulls Dahua / Imou / RTSP cameras,
// accepts RTMP/SRT pushes from drones and encoders, shows them on one multiview
// screen and restreams them to several destinations. MediaMTX does the media work;
// camerahub manages sources, health and alerts.
package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

type config struct {
	Listen     string
	DataFile   string
	MTXAPI     string
	AdminUser  string
	AdminPass  string
	Public     publicConfig
	ImouURL    string
	ImouID     string
	ImouSecret string
	TGToken    string
	TGChat     string
}

// publicConfig tells the browser where viewers and publishers reach MediaMTX.
type publicConfig struct {
	WebRTC string `json:"webrtc"` // e.g. http://hub.example.com:8889
	HLS    string `json:"hls"`    // e.g. http://hub.example.com:8888
	RTMP   string `json:"rtmp"`   // e.g. rtmp://hub.example.com:1935
	SRT    string `json:"srt"`    // e.g. srt://hub.example.com:8890
	RTSP   string `json:"rtsp"`   // e.g. rtsp://hub.example.com:8554
	Imou   bool   `json:"imou"`
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	host := env("HUB_PUBLIC_HOST", "localhost")
	c := config{
		Listen:     env("HUB_LISTEN", ":8080"),
		DataFile:   env("HUB_DATA_FILE", "data/sources.json"),
		MTXAPI:     strings.TrimRight(env("MTX_API", "http://localhost:9997"), "/"),
		AdminUser:  env("HUB_ADMIN_USER", "admin"),
		AdminPass:  os.Getenv("HUB_ADMIN_PASSWORD"),
		ImouURL:    env("IMOU_API_URL", "https://openapi.easy4ip.com/openapi"),
		ImouID:     os.Getenv("IMOU_APP_ID"),
		ImouSecret: os.Getenv("IMOU_APP_SECRET"),
		TGToken:    os.Getenv("TELEGRAM_BOT_TOKEN"),
		TGChat:     os.Getenv("TELEGRAM_CHAT_ID"),
	}
	c.Public = publicConfig{
		WebRTC: env("HUB_PUBLIC_WEBRTC", "http://"+host+":8889"),
		HLS:    env("HUB_PUBLIC_HLS", "http://"+host+":8888"),
		RTMP:   env("HUB_PUBLIC_RTMP", "rtmp://"+host+":1935"),
		SRT:    env("HUB_PUBLIC_SRT", "srt://"+host+":8890"),
		RTSP:   env("HUB_PUBLIC_RTSP", "rtsp://"+host+":8554"),
		Imou:   c.ImouID != "" && c.ImouSecret != "",
	}
	return c
}

func main() {
	cfg := loadConfig()
	store, err := OpenStore(cfg.DataFile)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	hub := NewHub(store, NewMediaMTX(cfg.MTXAPI),
		NewImou(cfg.ImouURL, cfg.ImouID, cfg.ImouSecret), NewTelegram(cfg.TGToken, cfg.TGChat))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go hub.Run(ctx, 15*time.Second)

	if cfg.AdminPass == "" {
		log.Printf("CẢNH BÁO: HUB_ADMIN_PASSWORD trống, giao diện quản trị không có mật khẩu")
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           basicAuth(cfg.AdminUser, cfg.AdminPass, newMux(cfg, store, hub)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	log.Printf("camerahub listening on %s (mediamtx api %s)", cfg.Listen, cfg.MTXAPI)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// createRequest is a Source plus an optional channel range: one request can
// register every channel of a Dahua NVR as "<id>-ch<N>".
type createRequest struct {
	Source
	Channels string `json:"channels,omitempty"`
}

func newMux(cfg config, store *Store, hub *Hub) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cfg.Public)
	})

	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		views, mtxErr := hub.Views()
		writeJSON(w, http.StatusOK, map[string]any{"sources": views, "mediamtxError": mtxErr})
	})

	mux.HandleFunc("POST /api/sources", func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		srcs := []Source{req.Source}
		if req.Channels != "" && (req.Kind == KindDahua || req.Kind == KindImouLocal) {
			chs, err := ParseChannels(req.Channels)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			srcs = srcs[:0]
			for _, ch := range chs {
				s := req.Source
				s.Channel = ch
				s.ID = fmt.Sprintf("%s-ch%d", req.ID, ch)
				s.Name = fmt.Sprintf("%s - Kênh %d", firstNonEmpty(req.Name, req.ID), ch)
				srcs = append(srcs, s)
			}
		}
		now := time.Now().UTC()
		for i := range srcs {
			srcs[i].CreatedAt = now
			if err := srcs[i].Validate(); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
		}
		if err := store.Create(srcs...); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrExists) {
				code = http.StatusConflict
			}
			writeErr(w, code, err)
			return
		}
		var warnings []string
		for _, s := range srcs {
			if err := hub.Apply(r.Context(), s); err != nil {
				warnings = append(warnings, s.ID+": "+err.Error())
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"created": len(srcs), "warnings": warnings})
	})

	mux.HandleFunc("PUT /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		old, ok := store.Get(id)
		if !ok {
			writeErr(w, http.StatusNotFound, ErrNotFound)
			return
		}
		var src Source
		if err := decodeJSON(r, &src); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		src.ID, src.CreatedAt = id, old.CreatedAt
		if src.Password == "" || src.Password == "********" {
			src.Password = old.Password
		}
		if err := src.Validate(); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := store.Update(src); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		resp := map[string]any{"updated": id}
		if err := hub.Apply(r.Context(), src); err != nil {
			resp["warnings"] = []string{err.Error()}
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("DELETE /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := store.Delete(id); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrNotFound) {
				code = http.StatusNotFound
			}
			writeErr(w, code, err)
			return
		}
		if err := hub.Remove(r.Context(), id); err != nil {
			log.Printf("delete path %s: %v", id, err)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		hub.SyncAll(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServerFS(static))
	return mux
}

func basicAuth(user, pass string, next http.Handler) http.Handler {
	if pass == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="camerahub"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

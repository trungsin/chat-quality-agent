package main

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Source kinds.
const (
	KindRTSP      = "rtsp"       // any RTSP URL (ONVIF cameras, other brands)
	KindDahua     = "dahua"      // Dahua NVR/IPC reachable by IP (direct or via VPN)
	KindImouLocal = "imou_local" // Imou camera reachable by IP on the LAN/VPN
	KindImouCloud = "imou_cloud" // Imou camera without IP, pulled through Imou Open Platform
	KindPush      = "push"       // drone / OBS / encoder pushes RTMP or SRT to the hub
)

var validKinds = map[string]bool{
	KindRTSP: true, KindDahua: true, KindImouLocal: true, KindImouCloud: true, KindPush: true,
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Source is one video stream managed by the hub. Its ID is also the MediaMTX path name.
type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Site string `json:"site,omitempty"`
	Kind string `json:"kind"`

	// rtsp
	URL string `json:"url,omitempty"`

	// dahua / imou_local
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Channel  int    `json:"channel,omitempty"`
	Subtype  int    `json:"subtype"` // 0 = main stream, 1 = sub stream

	// imou_cloud
	ImouDeviceID string `json:"imouDeviceId,omitempty"`
	ImouChannel  int    `json:"imouChannel"`

	// OnDemand pulls the camera only while someone is watching (saves bandwidth).
	// Ignored when Destinations is set, because restreaming needs the stream always on.
	OnDemand bool `json:"onDemand"`

	// Destinations to restream to: rtmp://, rtmps:// or srt:// URLs.
	Destinations []string `json:"destinations,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// Validate normalizes and checks a source before it is stored.
func (s *Source) Validate() error {
	s.ID = strings.ToLower(strings.TrimSpace(s.ID))
	if !idPattern.MatchString(s.ID) {
		return errors.New("id chỉ gồm a-z, 0-9, '-', '_' và tối đa 64 ký tự")
	}
	if strings.TrimSpace(s.Name) == "" {
		s.Name = s.ID
	}
	if !validKinds[s.Kind] {
		return fmt.Errorf("kind không hợp lệ: %q", s.Kind)
	}
	if s.Subtype != 0 && s.Subtype != 1 {
		return errors.New("subtype phải là 0 (main) hoặc 1 (sub)")
	}
	switch s.Kind {
	case KindRTSP:
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "rtsp" && u.Scheme != "rtsps") || u.Host == "" {
			return errors.New("url phải là rtsp://... hợp lệ")
		}
	case KindDahua, KindImouLocal:
		if s.Host == "" {
			return errors.New("thiếu host")
		}
		if s.Port == 0 {
			s.Port = 554
		}
		if s.Channel == 0 {
			s.Channel = 1
		}
		if s.Kind == KindImouLocal && s.Username == "" {
			s.Username = "admin"
		}
	case KindImouCloud:
		if s.ImouDeviceID == "" {
			return errors.New("thiếu imouDeviceId (serial thiết bị)")
		}
	}
	for _, d := range s.Destinations {
		u, err := url.Parse(d)
		if err != nil || u.Host == "" || (u.Scheme != "rtmp" && u.Scheme != "rtmps" && u.Scheme != "srt") {
			return fmt.Errorf("đích restream không hợp lệ: %q (chỉ rtmp://, rtmps://, srt://)", d)
		}
		if strings.ContainsAny(d, "'|") {
			return fmt.Errorf("đích restream chứa ký tự không được phép: %q", d)
		}
	}
	return nil
}

// PullURL returns the RTSP URL the hub pulls for IP-reachable Dahua/Imou devices.
// Dahua and Imou share the same RTSP layout.
func (s *Source) PullURL() string {
	switch s.Kind {
	case KindRTSP:
		return s.URL
	case KindDahua, KindImouLocal:
		u := url.URL{
			Scheme:   "rtsp",
			Host:     s.Host + ":" + strconv.Itoa(s.Port),
			Path:     "/cam/realmonitor",
			RawQuery: fmt.Sprintf("channel=%d&subtype=%d", s.Channel, s.Subtype),
		}
		if s.Username != "" {
			u.User = url.UserPassword(s.Username, s.Password)
		}
		return u.String()
	}
	return ""
}

// PathConfig builds the MediaMTX path configuration for this source.
// resolved is the upstream URL for kinds that need it resolved at runtime (imou_cloud).
func (s *Source) PathConfig(resolved string) map[string]any {
	cfg := map[string]any{}
	switch s.Kind {
	case KindPush:
		cfg["source"] = "publisher"
	case KindImouCloud:
		cfg["source"] = resolved
	default:
		cfg["source"] = s.PullURL()
		cfg["rtspTransport"] = "tcp"
	}
	if s.Kind != KindPush {
		cfg["sourceOnDemand"] = s.OnDemand && len(s.Destinations) == 0
	}
	if len(s.Destinations) > 0 {
		cfg["runOnReady"] = RestreamCommand(s.ID, s.Destinations)
		cfg["runOnReadyRestart"] = true
	}
	return cfg
}

// RestreamCommand builds an ffmpeg command that copies the stream to every destination
// in one process using the tee muxer. MediaMTX splits the command shell-style.
func RestreamCommand(path string, dests []string) string {
	outs := make([]string, 0, len(dests))
	for _, d := range dests {
		format := "flv"
		if strings.HasPrefix(d, "srt://") {
			format = "mpegts"
		}
		outs = append(outs, fmt.Sprintf("[f=%s:onfail=ignore]%s", format, d))
	}
	return fmt.Sprintf("ffmpeg -hide_banner -loglevel error -rtsp_transport tcp -i rtsp://localhost:$RTSP_PORT/%s "+
		"-map 0 -c copy -f tee '%s'", path, strings.Join(outs, "|"))
}

// ParseChannels parses "1-8", "1,3,5" or "1-4,9" into a sorted unique channel list.
func ParseChannels(spec string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi := part, part
		if i := strings.Index(part, "-"); i >= 0 {
			lo, hi = part[:i], part[i+1:]
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || a < 1 || b < a || b > 256 {
			return nil, fmt.Errorf("dải kênh không hợp lệ: %q", part)
		}
		for c := a; c <= b; c++ {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("không có kênh nào")
	}
	return out, nil
}

// Redacted returns a copy safe to send to the browser.
func (s Source) Redacted() Source {
	if s.Password != "" {
		s.Password = "********"
	}
	return s
}

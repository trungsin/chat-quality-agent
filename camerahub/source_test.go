package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDahuaPullURL(t *testing.T) {
	s := Source{ID: "kho-hn-ch2", Kind: KindDahua, Host: "10.8.0.12", Username: "admin", Password: "p@ss:w/rd", Channel: 2, Subtype: 1}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	want := "rtsp://admin:p%40ss%3Aw%2Frd@10.8.0.12:554/cam/realmonitor?channel=2&subtype=1"
	if got := s.PullURL(); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestImouLocalDefaults(t *testing.T) {
	s := Source{ID: "cua-hang", Kind: KindImouLocal, Host: "192.168.1.20", Password: "L2ABCDEF"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Username != "admin" || s.Port != 554 || s.Channel != 1 {
		t.Fatalf("defaults not applied: %+v", s)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []Source{
		{ID: "Bad ID!", Kind: KindRTSP, URL: "rtsp://h/x"},
		{ID: "a", Kind: "hikvision"},
		{ID: "a", Kind: KindRTSP, URL: "http://h/x"},
		{ID: "a", Kind: KindDahua},
		{ID: "a", Kind: KindImouCloud},
		{ID: "a", Kind: KindPush, Destinations: []string{"http://x"}},
		{ID: "a", Kind: KindPush, Destinations: []string{"rtmp://x/live/a|b"}},
		{ID: "a", Kind: KindPush, Subtype: 3},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: expected error for %+v", i, c)
		}
	}
}

func TestPathConfig(t *testing.T) {
	push := Source{ID: "drone1", Kind: KindPush}
	if cfg := push.PathConfig(""); cfg["source"] != "publisher" || cfg["sourceOnDemand"] != nil {
		t.Fatalf("push cfg: %v", cfg)
	}

	cam := Source{ID: "cam", Kind: KindRTSP, URL: "rtsp://h/x", OnDemand: true}
	if cfg := cam.PathConfig(""); cfg["sourceOnDemand"] != true || cfg["rtspTransport"] != "tcp" {
		t.Fatalf("on-demand cfg: %v", cfg)
	}

	// Restreaming forces the stream to stay on.
	cam.Destinations = []string{"rtmp://a.rtmp.youtube.com/live2/KEY", "srt://1.2.3.4:9000"}
	cfg := cam.PathConfig("")
	if cfg["sourceOnDemand"] != false || cfg["runOnReadyRestart"] != true {
		t.Fatalf("restream cfg: %v", cfg)
	}
	cmd := cfg["runOnReady"].(string)
	for _, want := range []string{
		"rtsp://localhost:$RTSP_PORT/cam",
		"[f=flv:onfail=ignore]rtmp://a.rtmp.youtube.com/live2/KEY|[f=mpegts:onfail=ignore]srt://1.2.3.4:9000",
		"-f tee '",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q missing %q", cmd, want)
		}
	}

	cloud := Source{ID: "imou1", Kind: KindImouCloud, ImouDeviceID: "6E0"}
	if cfg := cloud.PathConfig("https://cdn/live.m3u8"); cfg["source"] != "https://cdn/live.m3u8" {
		t.Fatalf("cloud cfg: %v", cfg)
	}
}

func TestParseChannels(t *testing.T) {
	got, err := ParseChannels("1-4, 3, 9")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 2, 3, 4, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, bad := range []string{"", "0", "4-2", "a-b", "1-300"} {
		if _, err := ParseChannels(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestImouSign(t *testing.T) {
	// md5("time:1599013514,nonce:abc,appSecret:secret")
	if got := imouSign(1599013514, "abc", "secret"); len(got) != 32 || got != imouSign(1599013514, "abc", "secret") {
		t.Fatalf("bad sign %q", got)
	}
	if imouSign(1, "a", "s") == imouSign(2, "a", "s") {
		t.Fatal("sign must depend on time")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sources.json")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	a := Source{ID: "a", Kind: KindPush, Site: "HN"}
	b := Source{ID: "b", Kind: KindPush, Site: "HCM"}
	if err := st.Create(a, b); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(Source{ID: "c"}, a); err != ErrExists {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if _, ok := st.Get("c"); ok {
		t.Fatal("partial create must not be stored")
	}
	if err := st.Delete("a"); err != nil {
		t.Fatal(err)
	}
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if list := st2.List(); len(list) != 1 || list[0].ID != "b" {
		t.Fatalf("reloaded: %+v", list)
	}
}

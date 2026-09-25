package channels

import (
	"context"
	"testing"
	"time"
)

func TestNewAdapterZaloOA(t *testing.T) {
	creds := `{"app_id":"123","app_secret":"abc","access_token":"tok","refresh_token":"ref"}`
	adapter, err := NewAdapter("zalo_oa", []byte(creds))
	if err != nil {
		t.Fatalf("NewAdapter zalo_oa failed: %v", err)
	}
	if adapter == nil {
		t.Fatal("Adapter should not be nil")
	}
}

func TestNewAdapterFacebook(t *testing.T) {
	creds := `{"page_id":"123","access_token":"tok"}`
	adapter, err := NewAdapter("facebook", []byte(creds))
	if err != nil {
		t.Fatalf("NewAdapter facebook failed: %v", err)
	}
	if adapter == nil {
		t.Fatal("Adapter should not be nil")
	}
}

func TestNewAdapterUnsupported(t *testing.T) {
	_, err := NewAdapter("whatsapp", []byte("{}"))
	if err == nil {
		t.Fatal("Should fail for unsupported channel type")
	}
}

func TestNewAdapterInvalidJSON(t *testing.T) {
	_, err := NewAdapter("zalo_oa", []byte("not json"))
	if err == nil {
		t.Fatal("Should fail for invalid JSON")
	}
}

func TestWebhookAdapterAndToken(t *testing.T) {
	a, err := NewAdapter(WebhookChannelType, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if convs, err := a.FetchRecentConversations(context.Background(), time.Time{}, 10); err != nil || convs != nil {
		t.Fatal("webhook adapter must not pull anything")
	}
	tok, err := NewIngestToken()
	if err != nil || len(tok) < 32 {
		t.Fatalf("bad token %q %v", tok, err)
	}
	creds := WebhookCredentials{IngestToken: tok}
	if !creds.Matches(tok) || creds.Matches(tok+"x") || creds.Matches("") || (WebhookCredentials{}).Matches("") {
		t.Fatal("token comparison is wrong")
	}
}

package channels

import (
	"encoding/json"
	"fmt"
)

// NewAdapter creates a ChannelAdapter from channel type and decrypted credentials JSON.
func NewAdapter(channelType string, credentialsJSON []byte) (ChannelAdapter, error) {
	switch channelType {
	case "zalo_oa":
		var creds ZaloOACredentials
		if err := json.Unmarshal(credentialsJSON, &creds); err != nil {
			return nil, fmt.Errorf("invalid zalo_oa credentials: %w", err)
		}
		return NewZaloOAAdapter(creds), nil
	case "facebook":
		var creds FacebookCredentials
		if err := json.Unmarshal(credentialsJSON, &creds); err != nil {
			return nil, fmt.Errorf("invalid facebook credentials: %w", err)
		}
		return NewFacebookAdapter(creds), nil
	case "pancake":
		var creds PancakeCredentials
		if err := json.Unmarshal(credentialsJSON, &creds); err != nil {
			return nil, fmt.Errorf("invalid pancake credentials: %w", err)
		}
		return NewPancakeAdapter(creds), nil
	case WebhookChannelType:
		return WebhookAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported channel type: %s", channelType)
	}
}

package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// #region TestLoadSetupConfig_OAuthCallbackExtra
// Confirms the new OAuthCallbackParams.Extra field round-trips through
// LoadSetupConfig, and that existing fixtures without it (every connector
// besides amazon-sp) still load cleanly with a nil Extra map.
func TestLoadSetupConfig_OAuthCallbackExtra(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantCode  string
		wantExtra map[string]string
	}{
		{
			name: "existing fixture without extra (e.g. googleads, tiktok, shopify)",
			json: `{
				"sku": "googleads",
				"oauthCallback": {
					"code": "abc123",
					"redirect_uri": "http://localhost/callback"
				}
			}`,
			wantCode:  "abc123",
			wantExtra: nil,
		},
		{
			name: "amazon-sp fixture with selling_partner_id in extra",
			json: `{
				"sku": "amazon-sp",
				"oauthCallback": {
					"code": "amzn-code-123",
					"redirect_uri": "http://localhost/callback",
					"extra": {"selling_partner_id": "A1B2C3D4E5"}
				}
			}`,
			wantCode:  "amzn-code-123",
			wantExtra: map[string]string{"selling_partner_id": "A1B2C3D4E5"},
		},
		{
			name: "no oauthCallback block at all (e.g. list-accounts config)",
			json: `{"sku": "googleads"}`,
			wantCode:  "",
			wantExtra: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte(tt.json), 0o644); err != nil {
				t.Fatalf("failed to write fixture: %v", err)
			}

			cfg, err := LoadSetupConfig(path)
			if err != nil {
				t.Fatalf("LoadSetupConfig() error = %v", err)
			}

			var gotCode string
			var gotExtra map[string]string
			if cfg.OAuthCallback != nil {
				gotCode = cfg.OAuthCallback.Code
				gotExtra = cfg.OAuthCallback.Extra
			}

			if gotCode != tt.wantCode {
				t.Errorf("code = %q, want %q", gotCode, tt.wantCode)
			}
			if len(gotExtra) != len(tt.wantExtra) {
				t.Errorf("extra = %#v, want %#v", gotExtra, tt.wantExtra)
			}
			for k, v := range tt.wantExtra {
				if gotExtra[k] != v {
					t.Errorf("extra[%q] = %q, want %q", k, gotExtra[k], v)
				}
			}
		})
	}
}

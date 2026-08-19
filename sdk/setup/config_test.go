package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
			name:      "no oauthCallback block at all (e.g. list-accounts config)",
			json:      `{"sku": "googleads"}`,
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

// #endregion

// #region TestTestRequestParams_KeepsNamedFieldsAndRaw
// Les 4 champs nommés restent la voie normale ; Raw les double sans les remplacer.
func TestTestRequestParams_KeepsNamedFieldsAndRaw(t *testing.T) {
	var cfg SetupConfig
	input := `{
		"sku": "matomo",
		"testRequest": {"report": "Visits", "fields": ["nb_visits"], "filters": ["a"], "sorts": ["b"]}
	}`
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	tr := cfg.TestRequest
	if tr == nil {
		t.Fatal("testRequest not decoded")
	}
	if tr.Report != "Visits" || len(tr.Fields) != 1 || tr.Fields[0] != "nb_visits" {
		t.Errorf("named fields lost: %#v", tr)
	}
	if len(tr.Raw) == 0 {
		t.Fatal("Raw must keep the original bytes")
	}
}

// #endregion

// #region TestTestRequestParams_UnknownKeysSurviveInRaw
// LE point de la modification : sans Raw, une spec arbitraire (api-rest-v2 :
// source/auth/pagination/records) serait silencieusement perdue au décodage, et
// test-query testerait… rien.
func TestTestRequestParams_UnknownKeysSurviveInRaw(t *testing.T) {
	var cfg SetupConfig
	input := `{
		"sku": "api-rest-v2",
		"testRequest": {
			"source": {"url": "https://api.example.com/sales", "query": {"date": "{{date}}"}},
			"auth": {"mode": "bearer", "value": "{{credentials.apikey}}"},
			"records": {"path": "data", "explode": "items"}
		}
	}`
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	m, err := cfg.TestRequest.AsMap()
	if err != nil {
		t.Fatalf("AsMap: %v", err)
	}

	source, _ := m["source"].(map[string]any)
	if source == nil || source["url"] != "https://api.example.com/sales" {
		t.Fatalf("source lost: %#v", m)
	}
	records, _ := m["records"].(map[string]any)
	if records == nil || records["explode"] != "items" {
		t.Errorf("records lost: %#v", m)
	}
}

// #endregion

// #region TestTestRequestParams_DecodeIntoTypedStruct
func TestTestRequestParams_DecodeIntoTypedStruct(t *testing.T) {
	var cfg SetupConfig
	input := `{"testRequest": {"source": {"url": "https://x.com", "timeoutSeconds": 30}}}`
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var target struct {
		Source struct {
			URL            string `json:"url"`
			TimeoutSeconds int    `json:"timeoutSeconds"`
		} `json:"source"`
	}
	if err := cfg.TestRequest.Decode(&target); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if target.Source.URL != "https://x.com" || target.Source.TimeoutSeconds != 30 {
		t.Errorf("decoded: %#v", target)
	}
}

// #endregion

// #region TestTestRequestParams_MissingRequestIsAnExplicitError
// Un "spec vide" silencieux se traduirait par un test réussi sur rien — le pire
// retour possible pour l'admin qui vient d'écrire sa requête.
func TestTestRequestParams_MissingRequestIsAnExplicitError(t *testing.T) {
	var nilParams *TestRequestParams
	if err := nilParams.Decode(&struct{}{}); err == nil {
		t.Fatal("expected an error on a nil TestRequestParams")
	}

	empty := &TestRequestParams{}
	err := empty.Decode(&struct{}{})
	if err == nil {
		t.Fatal("expected an error when Raw is empty")
	}
	if !strings.Contains(err.Error(), "no testRequest") {
		t.Errorf("message should be explicit, got %q", err.Error())
	}

	if _, err := empty.AsMap(); err == nil {
		t.Error("AsMap should fail the same way")
	}
}

// #endregion

// #region TestTestRequestParams_AbsentFromConfigStaysNil
func TestTestRequestParams_AbsentFromConfigStaysNil(t *testing.T) {
	var cfg SetupConfig
	if err := json.Unmarshal([]byte(`{"sku": "brevo"}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.TestRequest != nil {
		t.Errorf("testRequest should stay nil when absent, got %#v", cfg.TestRequest)
	}
}

// #endregion

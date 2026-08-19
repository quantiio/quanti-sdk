package httpsource

import (
	"strings"
	"testing"
)

// #region TestParseSpec_MinimalSpecGetsDefaults
// Un conf.yml minimal (url + records.path) doit suffire : c'est ce qui garde une spec
// courante à ~6 lignes au lieu de 40.
func TestParseSpec_MinimalSpecGetsDefaults(t *testing.T) {
	spec, err := ParseSpec(map[string]any{
		"source":  map[string]any{"url": "https://api.example.com/sales"},
		"records": map[string]any{"path": "data"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spec.Source.Method != "GET" {
		t.Errorf("method: got %q, want GET", spec.Source.Method)
	}
	if spec.Source.TimeoutSeconds != 60 {
		t.Errorf("timeout: got %d, want 60", spec.Source.TimeoutSeconds)
	}
	if spec.Auth.Mode != AuthNone {
		t.Errorf("auth.mode: got %q, want none", spec.Auth.Mode)
	}
	if spec.Pagination.Type != PageNone {
		t.Errorf("pagination.type: got %q, want none", spec.Pagination.Type)
	}
	if spec.Pagination.MaxPages != 1000 {
		t.Errorf("maxPages: got %d, want 1000", spec.Pagination.MaxPages)
	}
	if spec.Retry.MaxAttempts != 5 {
		t.Errorf("maxAttempts: got %d, want 5", spec.Retry.MaxAttempts)
	}
	if spec.Records.Format != FormatJSON {
		t.Errorf("format: got %q, want json", spec.Records.Format)
	}
	// Défaut critique : une commande sans ligne de détail ne doit pas disparaître.
	if spec.Records.EmitWhenExplodeEmpty == nil || !*spec.Records.EmitWhenExplodeEmpty {
		t.Error("emitWhenExplodeEmpty must default to true")
	}
	// Défaut sain même sans bloc retry : une API sans 429 ne le déclenchera jamais.
	if spec.Retry.On429 == nil || !spec.Retry.On429.RespectRetryAfter {
		t.Error("on429.respectRetryAfter must default to true")
	}
}

// #endregion

// #region TestParseSpec_YAMLInterfaceKeys
// yaml.v2 produit des map[interface{}]interface{} que encoding/json refuse. Sans
// normalisation, la spec échouerait en PRODUCTION tout en passant en test (où elle
// arrive déjà en JSON) — exactement le genre de bug qu'on ne voit qu'au déploiement.
func TestParseSpec_YAMLInterfaceKeys(t *testing.T) {
	raw := map[any]any{
		"source": map[any]any{
			"url":   "https://api.example.com/sales",
			"query": map[any]any{"date": "{{date}}"},
		},
		"records": map[any]any{"path": "data"},
	}

	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Source.Query["date"] != "{{date}}" {
		t.Errorf("query.date: got %q", spec.Source.Query["date"])
	}
}

// #endregion

// #region TestParseSpec_NonStringKeyIsRejected
func TestParseSpec_NonStringKeyIsRejected(t *testing.T) {
	raw := map[any]any{"source": map[any]any{42: "https://x"}}
	if _, err := ParseSpec(raw); err == nil {
		t.Fatal("expected an error on a non-string key")
	}
}

// #endregion

// #region TestParseSpec_NilIsRejected
func TestParseSpec_NilIsRejected(t *testing.T) {
	_, err := ParseSpec(nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if KindOf(err) != KindInvalidSpec {
		t.Errorf("kind: got %v, want invalid_spec", KindOf(err))
	}
	if !strings.Contains(err.Error(), "conf.yml") {
		t.Errorf("the message should point to conf.yml, got %q", err.Error())
	}
}

// #endregion

// #region TestValidate_Rejections
// Chaque cas ici est une erreur qu'un admin peut réellement écrire, et que `setup
// validate` doit attraper au Save plutôt qu'au cron de 3 h.
func TestValidate_Rejections(t *testing.T) {
	cases := []struct {
		name string
		spec map[string]any
		want string
	}{
		{
			name: "no url",
			spec: map[string]any{"records": map[string]any{"path": "data"}},
			want: "source.url is required",
		},
		{
			name: "url without scheme",
			spec: map[string]any{"source": map[string]any{"url": "api.example.com/sales"}},
			want: "must start with http",
		},
		{
			name: "unsupported method",
			spec: map[string]any{"source": map[string]any{"url": "https://x.com", "method": "TRACE"}},
			want: "method",
		},
		{
			name: "unknown auth mode",
			spec: map[string]any{
				"source": map[string]any{"url": "https://x.com"},
				"auth":   map[string]any{"mode": "magic"},
			},
			want: "auth.mode",
		},
		{
			name: "header auth without name",
			spec: map[string]any{
				"source": map[string]any{"url": "https://x.com"},
				"auth":   map[string]any{"mode": "header", "value": "k"},
			},
			want: "auth.name is required",
		},
		{
			name: "bearer auth without value",
			spec: map[string]any{
				"source": map[string]any{"url": "https://x.com"},
				"auth":   map[string]any{"mode": "bearer"},
			},
			want: "auth.value is required",
		},
		{
			name: "oauth2 client credentials incomplete",
			spec: map[string]any{
				"source": map[string]any{"url": "https://x.com"},
				"auth":   map[string]any{"mode": AuthOAuth2ClientCredentials, "clientId": "a"},
			},
			want: "auth.tokenUrl",
		},
		{
			name: "cursor pagination without cursorPath",
			spec: map[string]any{
				"source":     map[string]any{"url": "https://x.com"},
				"pagination": map[string]any{"type": "cursor"},
			},
			want: "cursorPath is required",
		},
		{
			// Sans taille de page, l'incrément d'offset serait une supposition.
			name: "offset pagination without size",
			spec: map[string]any{
				"source":     map[string]any{"url": "https://x.com"},
				"pagination": map[string]any{"type": "offset"},
			},
			want: "pagination.size is required",
		},
		{
			name: "next_url pagination without path",
			spec: map[string]any{
				"source":     map[string]any{"url": "https://x.com"},
				"pagination": map[string]any{"type": "next_url"},
			},
			want: "nextUrlPath is required",
		},
		{
			name: "unknown pagination type",
			spec: map[string]any{
				"source":     map[string]any{"url": "https://x.com"},
				"pagination": map[string]any{"type": "telepathy"},
			},
			want: "pagination.type",
		},
		{
			name: "unknown record format",
			spec: map[string]any{
				"source":  map[string]any{"url": "https://x.com"},
				"records": map[string]any{"format": "xml"},
			},
			want: "records.format",
		},
		{
			// Sans en-tête ET sans colonnes, les colonnes seraient anonymes → schéma
			// inexploitable côté BigQuery.
			name: "csv without header and without columns",
			spec: map[string]any{
				"source":  map[string]any{"url": "https://x.com"},
				"records": map[string]any{"format": "csv", "csv": map[string]any{"hasHeader": false}},
			},
			want: "columns is required",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseSpec(c.spec)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("message %q should contain %q", err.Error(), c.want)
			}
			if KindOf(err) != KindInvalidSpec {
				t.Errorf("kind: got %v, want invalid_spec", KindOf(err))
			}
		})
	}
}

// #endregion

// #region TestSecretTemplates
// Le rédacteur se construit à partir de cette liste : rater une valeur ici, c'est un
// secret en clair dans Loki et dans qm_process.
func TestSecretTemplates(t *testing.T) {
	spec, err := ParseSpec(map[string]any{
		"source": map[string]any{
			"url":     "https://api.example.com/{{credentials.tenant}}/sales",
			"query":   map[string]any{"key": "{{credentials.apikey}}", "date": "{{date}}"},
			"headers": map[string]any{"X-Plain": "static"},
		},
		"auth": map[string]any{"mode": "bearer", "value": "{{credentials.token}}"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := spec.SecretTemplates()
	if len(got) != 3 {
		t.Fatalf("got %d secret templates, want 3: %v", len(got), got)
	}
	for _, v := range got {
		if !strings.Contains(v, "credentials.") {
			t.Errorf("%q is not a credential template", v)
		}
	}
}

// #endregion

// #region TestRedactURL
func TestRedactURL(t *testing.T) {
	got := redactURL("https://api.example.com/v1/sales?apikey=supersecret&date=2026-08-12")
	if strings.Contains(got, "supersecret") {
		t.Fatalf("the query string must not leak: %q", got)
	}
	if !strings.Contains(got, "api.example.com/v1/sales") {
		t.Errorf("the path should remain readable: %q", got)
	}
}

// #endregion

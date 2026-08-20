package httpsource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fastEngine : moteur de test dont les attentes sont instantanées mais COMPTABILISÉES.
// Sans ça, un scénario "429 avec Retry-After: 60" prendrait une minute en CI et ne
// serait jamais lancé — donc jamais couvert.
func fastEngine(slept *[]time.Duration) *Engine {
	return New(WithSleeper(func(_ context.Context, d time.Duration) error {
		if slept != nil {
			*slept = append(*slept, d)
		}
		return nil
	}))
}

func mustSpec(t *testing.T, raw map[string]any) *Spec {
	t.Helper()
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	return spec
}

func collect(t *testing.T, e *Engine, spec *Spec, vars Vars) ([]map[string]any, Stats) {
	t.Helper()
	var rows []map[string]any
	stats, err := e.Fetch(context.Background(), spec, vars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return rows, stats
}

// ============================================================================
// Parité avec les 2 connecteurs v1 — ce sont LES tests qui autorisent la bascule
// ============================================================================

// #region TestFetch_ParityLesEchos
// Les Échos : GET daté, auth bearer, records.path=data, pas de pagination.
func TestFetch_ParityLesEchos(t *testing.T) {
	var gotAuth, gotFrom, gotTo string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotFrom = r.URL.Query().Get("fromDate")
		gotTo = r.URL.Query().Get("toDate")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[
			{"orderXref":"A1","amountUntaxed":230.32,"orderStatus":"CONFIRMED"},
			{"orderXref":"A2","amountUntaxed":12.5,"orderStatus":"PENDING"}
		]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source": map[string]any{
			"url":     srv.URL + "/v1/export/sales",
			"query":   map[string]any{"fromDate": "{{date}}", "toDate": "{{date}}"},
			"headers": map[string]any{"Content-Type": "application/json"},
		},
		"auth": map[string]any{"mode": "bearer", "value": "{{credentials.apikey}}"},
		"retry": map[string]any{
			"maxAttempts": 20,
			"on429":       map[string]any{"respectRetryAfter": true, "retryAfterPath": "retry_after", "extraWaitSeconds": 600},
		},
		"records": map[string]any{"path": "data"},
	})

	rows, stats := collect(t, fastEngine(nil), spec, Vars{
		Date:        "2026-08-12",
		Credentials: map[string]any{"apikey": "LE-KEY"},
	})

	if gotAuth != "Bearer LE-KEY" {
		t.Errorf("Authorization: got %q", gotAuth)
	}
	if gotFrom != "2026-08-12" || gotTo != "2026-08-12" {
		t.Errorf("date params: got from=%q to=%q", gotFrom, gotTo)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0]["orderXref"] != "A1" || rows[1]["orderXref"] != "A2" {
		t.Errorf("unexpected rows: %#v", rows)
	}
	// Les montants doivent rester des float64 : les convertir en string ici casserait
	// le typage FLOAT de la table BigQuery existante.
	if _, ok := rows[0]["amountUntaxed"].(float64); !ok {
		t.Errorf("amountUntaxed should stay a float64, got %T", rows[0]["amountUntaxed"])
	}
	if stats.Pages != 1 || stats.Rows != 2 {
		t.Errorf("stats: %+v", stats)
	}
}

// #endregion

// #region TestFetch_ParityMedusaExplode
// Peyce/medusa : records.path=carts, explode=items, inject date.
//
// Vérifie le point de parité le plus fragile : `items` doit devenir un OBJET singulier
// pour que le flatten de processor-v2 produise `data.items.<champ>` et non
// `data.items.0.<champ>` — sinon aucun fieldPath du schéma ne matche et la table se
// remplit de NULL.
func TestFetch_ParityMedusaExplode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Token") != "MEDUSA-KEY" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"carts":[
			{"transaction_id":"T1","total":100,"items":[
				{"race_id":"R1","bib_price":50},
				{"race_id":"R2","bib_price":50}
			]},
			{"transaction_id":"T2","total":0,"items":[]}
		]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source": map[string]any{
			"url":   srv.URL + "/webhook/registrations/549dfb7d",
			"query": map[string]any{"date": "{{date}}"},
		},
		"auth": map[string]any{"mode": "header", "name": "X-API-Token", "value": "{{credentials.apikey}}"},
		"records": map[string]any{
			"path":    "carts",
			"explode": "items",
			"inject":  map[string]any{"date": "{{date}}"},
		},
	})

	rows, _ := collect(t, fastEngine(nil), spec, Vars{
		Date:        "2026-08-12",
		Credentials: map[string]any{"apikey": "MEDUSA-KEY"},
	})

	// 2 lignes pour T1 (2 items) + 1 ligne pour T2 (items vide, émise quand même).
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %#v", len(rows), rows)
	}

	item, ok := rows[0]["items"].(map[string]any)
	if !ok {
		t.Fatalf("items must be a singular OBJECT after explode, got %T", rows[0]["items"])
	}
	if item["race_id"] != "R1" {
		t.Errorf("row 0 item: got %#v", item)
	}
	if second, _ := rows[1]["items"].(map[string]any); second == nil || second["race_id"] != "R2" {
		t.Errorf("row 1 item: got %#v", rows[1]["items"])
	}

	// La commande sans item doit survivre, avec items intact (tableau vide) → colonnes
	// exploded à NULL en base. La perdre serait une perte de donnée silencieuse.
	if rows[2]["transaction_id"] != "T2" {
		t.Errorf("the cart without items must still be emitted, got %#v", rows[2])
	}

	// inject s'applique à CHAQUE ligne produite, pas seulement à la première.
	for i, row := range rows {
		if row["date"] != "2026-08-12" {
			t.Errorf("row %d: inject date missing, got %#v", i, row["date"])
		}
	}
}

// #endregion

// #region TestFetch_ExplodeEmptyCanBeDropped
func TestFetch_ExplodeEmptyCanBeDropped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"carts":[{"id":"T1","items":[{"x":1}]},{"id":"T2","items":[]}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"records": map[string]any{"path": "carts", "explode": "items", "emitWhenExplodeEmpty": false},
	})

	rows, _ := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
	if len(rows) != 1 || rows[0]["id"] != "T1" {
		t.Fatalf("got %#v, want only T1", rows)
	}
}

// #endregion

// ============================================================================
// Pagination
// ============================================================================

// #region TestFetch_PaginationCursor
func TestFetch_PaginationCursor(t *testing.T) {
	var seenCursors []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		seenCursors = append(seenCursors, cursor)
		switch cursor {
		case "":
			fmt.Fprint(w, `{"items":[{"id":1}],"has_more":true,"next_cursor":"c2"}`)
		case "c2":
			fmt.Fprint(w, `{"items":[{"id":2}],"has_more":true,"next_cursor":"c3"}`)
		default:
			fmt.Fprint(w, `{"items":[{"id":3}],"has_more":false,"next_cursor":""}`)
		}
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source": map[string]any{"url": srv.URL},
		"pagination": map[string]any{
			"type": "cursor", "param": "cursor",
			"cursorPath": "next_cursor", "hasMorePath": "has_more",
		},
		"records": map[string]any{"path": "items"},
	})

	rows, stats := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
	if len(rows) != 3 || stats.Pages != 3 {
		t.Fatalf("got %d rows over %d pages, want 3/3", len(rows), stats.Pages)
	}
	// Première page SANS paramètre cursor : envoyer `cursor=` vide fait répondre 400 à
	// certaines API.
	if len(seenCursors) != 3 || seenCursors[0] != "" || seenCursors[1] != "c2" || seenCursors[2] != "c3" {
		t.Errorf("cursors seen: %#v", seenCursors)
	}
}

// #endregion

// #region TestFetch_PaginationCursorStopsOnRepeatedCursor
// Garde-fou : une API qui renvoie toujours le même curseur ferait boucler jusqu'à
// maxPages en dupliquant les lignes à chaque tour.
func TestFetch_PaginationCursorStopsOnRepeatedCursor(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, `{"items":[{"id":1}],"has_more":true,"next_cursor":"same"}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":     map[string]any{"url": srv.URL},
		"pagination": map[string]any{"type": "cursor", "cursorPath": "next_cursor", "hasMorePath": "has_more", "maxPages": 50},
		"records":    map[string]any{"path": "items"},
	})

	_, stats := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
	if stats.Pages != 2 {
		t.Fatalf("should stop on the second identical cursor, got %d pages (%d calls)", stats.Pages, calls)
	}
}

// #endregion

// #region TestFetch_PaginationPage
func TestFetch_PaginationPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, `{"data":[{"id":1},{"id":2}]}`)
		case "2":
			fmt.Fprint(w, `{"data":[{"id":3}]}`) // page incomplète → dernière
		default:
			fmt.Fprint(w, `{"data":[]}`)
		}
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":     map[string]any{"url": srv.URL},
		"pagination": map[string]any{"type": "page", "param": "page", "sizeParam": "per_page", "size": 2},
		"records":    map[string]any{"path": "data"},
	})

	rows, stats := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	// 2 pages seulement : la page incomplète évite un aller-retour inutile — ça compte
	// quand on collecte 365 jours d'historique.
	if stats.Pages != 2 {
		t.Errorf("got %d pages, want 2 (incomplete page should end pagination)", stats.Pages)
	}
}

// #endregion

// #region TestFetch_PaginationOffsetAndLinkHeader
func TestFetch_PaginationOffsetAndLinkHeader(t *testing.T) {
	t.Run("offset", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("offset") == "0" {
				fmt.Fprint(w, `{"data":[{"id":1},{"id":2}]}`)
				return
			}
			fmt.Fprint(w, `{"data":[{"id":3}]}`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":     map[string]any{"url": srv.URL},
			"pagination": map[string]any{"type": "offset", "param": "offset", "size": 2},
			"records":    map[string]any{"path": "data"},
		})
		rows, _ := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
		if len(rows) != 3 {
			t.Fatalf("got %d rows, want 3", len(rows))
		}
	})

	t.Run("link header", func(t *testing.T) {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("p") == "" {
				w.Header().Set("Link", `<`+srv.URL+`?p=2>; rel="next", <`+srv.URL+`?p=9>; rel="last"`)
				fmt.Fprint(w, `{"data":[{"id":1}]}`)
				return
			}
			fmt.Fprint(w, `{"data":[{"id":2}]}`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":     map[string]any{"url": srv.URL},
			"pagination": map[string]any{"type": "link_header"},
			"records":    map[string]any{"path": "data"},
		})
		rows, stats := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
		if len(rows) != 2 || stats.Pages != 2 {
			t.Fatalf("got %d rows over %d pages, want 2/2", len(rows), stats.Pages)
		}
	})
}

// #endregion

// #region TestFetch_MaxPagesIsASafetyNetNotAnError
// Le plafond doit couper avec un warning, PAS échouer : les lignes déjà émises sont
// valides, et une erreur ferait rejouer toute la date au run suivant.
func TestFetch_MaxPagesIsASafetyNetNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":1},{"id":2}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":     map[string]any{"url": srv.URL},
		"pagination": map[string]any{"type": "page", "param": "page", "maxPages": 3},
		"records":    map[string]any{"path": "data"},
	})

	rows, stats := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
	if stats.Pages != 3 {
		t.Errorf("got %d pages, want the 3-page cap", stats.Pages)
	}
	if len(rows) != 6 {
		t.Errorf("got %d rows, want 6 (rows collected before the cap are kept)", len(rows))
	}
}

// #endregion

// ============================================================================
// Retry / rate limit
// ============================================================================

// #region TestFetch_RateLimitRespectsRetryAfterPlusExtra
// Le comportement Les Échos : l'API MENT sur son Retry-After, on ajoute 600 s.
func TestFetch_RateLimitRespectsRetryAfterPlusExtra(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"rate limited","retry_after":30,"limit_type":"daily"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":1}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source": map[string]any{"url": srv.URL},
		"retry": map[string]any{
			"maxAttempts": 20,
			"on429": map[string]any{
				"respectRetryAfter": true,
				"retryAfterPath":    "retry_after",
				"extraWaitSeconds":  600,
				"maxWaitSeconds":    900,
			},
		},
		"records": map[string]any{"path": "data"},
	})

	var slept []time.Duration
	rows, stats := collect(t, fastEngine(&slept), spec, Vars{Date: "2026-08-12"})

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if stats.Attempts != 2 {
		t.Errorf("got %d attempts, want 2", stats.Attempts)
	}
	if len(slept) != 1 {
		t.Fatalf("got %d waits, want 1", len(slept))
	}
	// 30 s annoncées + 600 s de contournement = 630 s.
	if slept[0] != 630*time.Second {
		t.Errorf("wait: got %s, want 630s (30 announced + 600 extra)", slept[0])
	}
}

// #endregion

// #region TestFetch_RateLimitIsCappedByMaxWait
// Sans plafond, une API renvoyant `Retry-After: 86400` immobiliserait un worker toute
// la journée.
func TestFetch_RateLimitIsCappedByMaxWait(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "86400")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"retry":   map[string]any{"on429": map[string]any{"respectRetryAfter": true, "maxWaitSeconds": 900}},
		"records": map[string]any{"path": "data"},
	})

	var slept []time.Duration
	collect(t, fastEngine(&slept), spec, Vars{Date: "2026-08-12"})

	if len(slept) != 1 || slept[0] != 900*time.Second {
		t.Fatalf("wait should be capped at 900s, got %v", slept)
	}
}

// #endregion

// #region TestFetch_RateLimitExhausted
func TestFetch_RateLimitExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"nope"}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"retry":   map[string]any{"maxAttempts": 3, "on429": map[string]any{"backoffSeconds": 1}},
		"records": map[string]any{"path": "data"},
	})

	var slept []time.Duration
	_, err := fastEngine(&slept).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	// Le Kind doit être RateLimit et non Unavailable : le proc le traduit en
	// ERR_TMP_RATE_LIMIT_EXCEEDED, qui est une erreur TEMPORAIRE côté Quanti.
	if KindOf(err) != KindRateLimit {
		t.Errorf("kind: got %v, want rate_limit", KindOf(err))
	}
	if len(slept) != 2 {
		t.Errorf("got %d waits for 3 attempts, want 2", len(slept))
	}
}

// #endregion

// #region TestFetch_ServerErrorRetriedThenOK
func TestFetch_ServerErrorRetriedThenOK(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":1}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"retry":   map[string]any{"maxAttempts": 5, "on5xx": map[string]any{"backoffSeconds": 2, "exponential": true}},
		"records": map[string]any{"path": "data"},
	})

	var slept []time.Duration
	rows, stats := collect(t, fastEngine(&slept), spec, Vars{Date: "2026-08-12"})
	if len(rows) != 1 || stats.Attempts != 3 {
		t.Fatalf("got %d rows in %d attempts, want 1/3", len(rows), stats.Attempts)
	}
	// Backoff exponentiel : 2s puis 4s.
	if len(slept) != 2 || slept[0] != 2*time.Second || slept[1] != 4*time.Second {
		t.Errorf("exponential backoff expected 2s then 4s, got %v", slept)
	}
}

// #endregion

// #region TestFetch_ClientErrorsAreNotRetried
// Réessayer un 400 ou un 401 épuise les tentatives pour rien et retarde de plusieurs
// minutes le seul message d'erreur utile.
func TestFetch_ClientErrorsAreNotRetried(t *testing.T) {
	cases := []struct {
		status int
		want   Kind
	}{
		{http.StatusUnauthorized, KindAuth},
		{http.StatusForbidden, KindAuth},
		{http.StatusBadRequest, KindInvalidSpec},
		{http.StatusUnprocessableEntity, KindInvalidSpec},
		// 404 → Unavailable : sur une API sur-mesure, c'est bien plus souvent
		// "l'endpoint a bougé" qu'une faute dans notre conf.yml.
		{http.StatusNotFound, KindUnavailable},
	}

	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(c.status)
				fmt.Fprint(w, `{"error":"boom"}`)
			}))
			defer srv.Close()

			spec := mustSpec(t, map[string]any{
				"source":  map[string]any{"url": srv.URL},
				"retry":   map[string]any{"maxAttempts": 5},
				"records": map[string]any{"path": "data"},
			})

			_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
			if err == nil {
				t.Fatal("expected an error")
			}
			if KindOf(err) != c.want {
				t.Errorf("kind: got %v, want %v", KindOf(err), c.want)
			}
			if calls != 1 {
				t.Errorf("got %d calls, want 1 (no retry on a client error)", calls)
			}
		})
	}
}

// #endregion

// ============================================================================
// Auth
// ============================================================================

// #region TestFetch_AuthModes
func TestFetch_AuthModes(t *testing.T) {
	t.Run("query", func(t *testing.T) {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Query().Get("api_key")
			fmt.Fprint(w, `{"data":[]}`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":  map[string]any{"url": srv.URL, "query": map[string]any{"date": "{{date}}"}},
			"auth":    map[string]any{"mode": "query", "name": "api_key", "value": "{{credentials.apikey}}"},
			"records": map[string]any{"path": "data"},
		})
		collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12", Credentials: map[string]any{"apikey": "QK"}})
		if got != "QK" {
			t.Errorf("api_key: got %q", got)
		}
	})

	t.Run("basic", func(t *testing.T) {
		var user, pass string
		var ok bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok = r.BasicAuth()
			fmt.Fprint(w, `{"data":[]}`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":  map[string]any{"url": srv.URL},
			"auth":    map[string]any{"mode": "basic", "username": "{{credentials.user}}", "password": "{{credentials.pass}}"},
			"records": map[string]any{"path": "data"},
		})
		collect(t, fastEngine(nil), spec, Vars{
			Date:        "2026-08-12",
			Credentials: map[string]any{"user": "alice", "pass": "s3cret!"},
		})
		if !ok || user != "alice" || pass != "s3cret!" {
			t.Errorf("basic auth: ok=%v user=%q pass=%q", ok, user, pass)
		}
	})
}

// #endregion

// #region TestFetch_OAuth2ClientCredentials
// Le token doit être obtenu UNE fois et réutilisé sur toutes les pages : certains
// providers rate-limitent durement l'endpoint token.
func TestFetch_OAuth2ClientCredentials(t *testing.T) {
	tokenCalls := 0
	var gotAuth string

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls++
			if err := r.ParseForm(); err != nil {
				t.Errorf("cannot parse the token form: %v", err)
			}
			if r.Form.Get("grant_type") != "client_credentials" {
				t.Errorf("grant_type: got %q", r.Form.Get("grant_type"))
			}
			fmt.Fprint(w, `{"access_token":"AT-123","expires_in":3600}`)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"data":[{"id":2}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":1},{"id":9}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source": map[string]any{"url": srv.URL + "/data"},
		"auth": map[string]any{
			"mode": AuthOAuth2ClientCredentials, "tokenUrl": srv.URL + "/token",
			"clientId": "{{credentials.client_id}}", "clientSecret": "{{credentials.client_secret}}",
			"scopes": []any{"read"},
		},
		"pagination": map[string]any{"type": "page", "param": "page", "size": 2},
		"records":    map[string]any{"path": "data"},
	})

	rows, stats := collect(t, fastEngine(nil), spec, Vars{
		Date:        "2026-08-12",
		Credentials: map[string]any{"client_id": "cid", "client_secret": "csecret"},
	})

	if gotAuth != "Bearer AT-123" {
		t.Errorf("Authorization: got %q", gotAuth)
	}
	if tokenCalls != 1 {
		t.Errorf("got %d token calls over %d pages, want 1", tokenCalls, stats.Pages)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3", len(rows))
	}
}

// #endregion

// #region TestFetch_OAuth2RefreshedOnceOn401
// Un token peut être révoqué avant son expiration annoncée : on renouvelle et on
// reprend UNE fois. Un second 401 est une vraie erreur d'auth.
func TestFetch_OAuth2RefreshedOnceOn401(t *testing.T) {
	tokenCalls, dataCalls := 0, 0

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls++
			fmt.Fprintf(w, `{"access_token":"AT-%d","expires_in":3600}`, tokenCalls)
			return
		}
		dataCalls++
		if dataCalls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":1}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source": map[string]any{"url": srv.URL + "/data"},
		"auth": map[string]any{
			"mode": AuthOAuth2Refresh, "tokenUrl": srv.URL + "/token",
			"refreshToken": "{{credentials.refresh_token}}",
		},
		"records": map[string]any{"path": "data"},
	})

	rows, _ := collect(t, fastEngine(nil), spec, Vars{
		Date:        "2026-08-12",
		Credentials: map[string]any{"refresh_token": "RT"},
	})

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if tokenCalls != 2 {
		t.Errorf("got %d token calls, want 2 (initial + refresh after the 401)", tokenCalls)
	}
}

// #endregion

// #region TestFetch_OAuth2Persistent401IsAuthError
func TestFetch_OAuth2Persistent401IsAuthError(t *testing.T) {
	dataCalls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"access_token":"AT","expires_in":3600}`)
			return
		}
		dataCalls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL + "/data"},
		"auth":    map[string]any{"mode": AuthOAuth2Refresh, "tokenUrl": srv.URL + "/token", "refreshToken": "RT"},
		"records": map[string]any{"path": "data"},
	})

	_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	if KindOf(err) != KindAuth {
		t.Errorf("kind: got %v, want auth", KindOf(err))
	}
	if dataCalls != 2 {
		t.Errorf("got %d data calls, want 2 (one refresh attempt, then give up)", dataCalls)
	}
}

// #endregion

// #region TestFetch_TokenEndpointRefusalIsAuthNotUnavailable
// Un token refusé ne doit pas être retenté : il faut de nouvelles credentials.
func TestFetch_TokenEndpointRefusalIsAuthNotUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_client"}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL + "/data"},
		"auth":    map[string]any{"mode": AuthOAuth2ClientCredentials, "tokenUrl": srv.URL + "/token", "clientId": "a", "clientSecret": "b"},
		"records": map[string]any{"path": "data"},
	})

	_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
	if KindOf(err) != KindAuth {
		t.Fatalf("kind: got %v, want auth (err=%v)", KindOf(err), err)
	}
}

// #endregion

// ============================================================================
// POST / body / CSV / erreurs de données
// ============================================================================

// #region TestFetch_PostWithTemplatedBody
func TestFetch_PostWithTemplatedBody(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method: got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type: got %q", ct)
		}
		json.NewDecoder(r.Body).Decode(&received)
		fmt.Fprint(w, `{"rows":[{"id":1}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source": map[string]any{
			"url":    srv.URL,
			"method": "POST",
			"body": map[string]any{
				"dateRange": map[string]any{"start": "{{startDate}}", "end": "{{endDate}}"},
				"metrics":   []any{"clicks", "impressions"},
				"limit":     float64(500),
			},
		},
		"records": map[string]any{"path": "rows"},
	})

	collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12", StartDate: "2026-08-01", EndDate: "2026-08-31"})

	dr, _ := received["dateRange"].(map[string]any)
	if dr == nil || dr["start"] != "2026-08-01" || dr["end"] != "2026-08-31" {
		t.Errorf("templated body: got %#v", received)
	}
	// Les non-strings doivent traverser intacts : templater un nombre n'a pas de sens.
	if received["limit"] != float64(500) {
		t.Errorf("limit: got %#v, want 500", received["limit"])
	}
}

// #endregion

// #region TestFetch_CSV
func TestFetch_CSV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		fmt.Fprint(w, "order_id,amount,label\nA1,230.32,Foo\nA2,12.5,\n")
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"records": map[string]any{"format": "csv"},
	})

	rows, _ := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %#v", len(rows), rows)
	}
	// Tout reste STRING : le typage appartient au schéma, pas au moteur — deviner ici
	// produirait des types différents selon le contenu de la première page.
	if rows[0]["amount"] != "230.32" {
		t.Errorf("amount: got %#v, want the string \"230.32\"", rows[0]["amount"])
	}
	if rows[1]["label"] != "" {
		t.Errorf("empty CSV cell should be an empty string, got %#v", rows[1]["label"])
	}
}

// #endregion

// #region TestFetch_DataErrors
func TestFetch_DataErrors(t *testing.T) {
	t.Run("not json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `<html>maintenance</html>`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":  map[string]any{"url": srv.URL},
			"records": map[string]any{"path": "data"},
		})
		_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
		if KindOf(err) != KindInvalidData {
			t.Fatalf("kind: got %v, want invalid_data (err=%v)", KindOf(err), err)
		}
	})

	t.Run("path not found lists available keys", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"results":[],"meta":{}}`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":  map[string]any{"url": srv.URL},
			"records": map[string]any{"path": "data"},
		})
		_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
		if err == nil {
			t.Fatal("expected an error")
		}
		// Lister les clés disponibles fait gagner le prochain aller-retour : l'admin
		// voit tout de suite qu'il fallait écrire "results".
		if !strings.Contains(err.Error(), "results") {
			t.Errorf("the message should list the available keys, got %q", err.Error())
		}
		// …et le corps est joint : une API qui répond 200 avec une enveloppe d'erreur
		// (webhook n8n non enregistré, clé refusée) ne se diagnostique QUE par la
		// valeur de message/error, pas par la liste des clés.
		if !strings.Contains(err.Error(), "response body:") {
			t.Errorf("the message should include a body excerpt, got %q", err.Error())
		}
	})

	t.Run("null path is not an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"data":null}`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":  map[string]any{"url": srv.URL},
			"records": map[string]any{"path": "data"},
		})
		// Une journée sans donnée est un cas métier normal, pas un incident.
		rows, _ := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})

	t.Run("single object is accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"data":{"id":1}}`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{
			"source":  map[string]any{"url": srv.URL},
			"records": map[string]any{"path": "data"},
		})
		rows, _ := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
	})

	t.Run("empty path reads the root array", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `[{"id":1},{"id":2}]`)
		}))
		defer srv.Close()

		spec := mustSpec(t, map[string]any{"source": map[string]any{"url": srv.URL}})
		rows, _ := collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2", len(rows))
		}
	})
}

// #endregion

// ============================================================================
// Secrets, streaming, arrêt
// ============================================================================

// #region TestFetch_ErrorsNeverLeakSecrets
// Les erreurs du moteur remontent dans qm_process.last_execution_message (visible
// client ET admin) et dans Loki. Une clé en query param s'y retrouverait archivée en
// clair aux deux endroits.
func TestFetch_ErrorsNeverLeakSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// Le pire cas : l'API renvoie la clé reçue dans son message d'erreur.
		fmt.Fprintf(w, `{"error":"bad key %s"}`, r.URL.Query().Get("api_key"))
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL, "query": map[string]any{"api_key": "{{credentials.apikey}}"}},
		"records": map[string]any{"path": "data"},
	})

	_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{
		Date:        "2026-08-12",
		Credentials: map[string]any{"apikey": "SUPER-SECRET-VALUE"},
	}, func(map[string]any) error { return nil })

	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SUPER-SECRET-VALUE") {
		t.Fatalf("the error leaks the credential: %q", err.Error())
	}
	if !strings.Contains(err.Error(), redactionMask) {
		t.Errorf("the error should show the redaction mask, got %q", err.Error())
	}
}

// #endregion

// #region TestFetch_OAuthTokenIsRedactedToo
// Le token obtenu à l'exécution n'est pas dans les credentials du compte : sans
// Redactor.Add, il apparaîtrait en clair au premier message d'erreur.
func TestFetch_OAuthTokenIsRedactedToo(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"access_token":"LEAKY-TOKEN-VALUE","expires_in":3600}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"rejected token %s"}`, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL + "/data"},
		"auth":    map[string]any{"mode": AuthOAuth2ClientCredentials, "tokenUrl": srv.URL + "/token", "clientId": "a", "clientSecret": "b"},
		"records": map[string]any{"path": "data"},
	})

	_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "LEAKY-TOKEN-VALUE") {
		t.Fatalf("the error leaks the runtime OAuth token: %q", err.Error())
	}
}

// #endregion

// #region TestFetch_EmitStopsIteration
// C'est le mécanisme de test-query : prendre un échantillon sans dérouler 400 pages.
func TestFetch_EmitStopsIteration(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, `{"data":[{"id":1},{"id":2},{"id":3}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":     map[string]any{"url": srv.URL},
		"pagination": map[string]any{"type": "page", "param": "page", "maxPages": 100},
		"records":    map[string]any{"path": "data"},
	})

	rows, _, err := fastEngine(nil).FetchN(context.Background(), spec, Vars{Date: "2026-08-12"}, 2)
	if err != nil {
		t.Fatalf("FetchN: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
	if calls != 1 {
		t.Errorf("got %d HTTP calls, want 1 (stop must not keep paginating)", calls)
	}
}

// #endregion

// #region TestFetch_EmitErrorAborts
// Une erreur d'upsert doit remonter telle quelle, sans être confondue avec ErrStop.
func TestFetch_EmitErrorAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":1},{"id":2}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"records": map[string]any{"path": "data"},
	})

	boom := fmt.Errorf("upsert failed")
	_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error {
		return boom
	})
	if err != boom {
		t.Fatalf("got %v, want the emit error verbatim", err)
	}
}

// #endregion

// #region TestFetch_ContextCancellation
func TestFetch_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":1}]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"records": map[string]any{"path": "data"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fastEngine(nil).Fetch(ctx, spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
	if err == nil {
		t.Fatal("expected an error on a cancelled context")
	}
}

// #endregion

// #region TestFetch_ExistingQueryStringIsPreserved
// Beaucoup d'URL sur-mesure embarquent déjà un paramètre fixe (id de webhook, version
// d'API) : l'écraser casserait la requête.
func TestFetch_ExistingQueryStringIsPreserved(t *testing.T) {
	var fixed, dated string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixed = r.URL.Query().Get("v")
		dated = r.URL.Query().Get("date")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL + "/export?v=2", "query": map[string]any{"date": "{{date}}"}},
		"records": map[string]any{"path": "data"},
	})

	collect(t, fastEngine(nil), spec, Vars{Date: "2026-08-12"})
	if fixed != "2" || dated != "2026-08-12" {
		t.Errorf("got v=%q date=%q, want v=2 and the date", fixed, dated)
	}
}

// #endregion

// #region TestFetch_ErrorEnvelopeOn200IsDiagnosable
// Cas réel rencontré sur stg (2026-08-20) : un webhook n8n non enregistré répond
// HTTP 200 avec {"error","hint","message"} au lieu des données. Le moteur voit un 200
// dont le records.path est absent — il ne peut pas deviner que c'est une erreur. Le
// message doit donc porter la VALEUR du corps, sinon le diagnostic est impossible
// (lister "available: error, hint, message" ne dit pas quoi corriger).
func TestFetch_ErrorEnvelopeOn200IsDiagnosable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"error":"unauthorized","hint":"check your token","message":"webhook not registered"}`)
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"records": map[string]any{"path": "carts"},
	})

	_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{Date: "2026-08-12"}, func(map[string]any) error { return nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "webhook not registered") {
		t.Errorf("the actionable value of the envelope must appear, got %q", err.Error())
	}
	if KindOf(err) != KindInvalidData {
		t.Errorf("kind: got %v, want invalid_data", KindOf(err))
	}
}

// #endregion

// #region TestFetch_ErrorEnvelopeExcerptIsRedacted
// L'extrait de corps joint au message d'erreur traverse le rédacteur. Une API qui
// renvoie la clé reçue en écho dans son enveloppe d'erreur ne doit pas la faire
// atterrir dans qm_process.last_execution_message ni dans Loki.
func TestFetch_ErrorEnvelopeExcerptIsRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HTTP 200 + enveloppe d'erreur qui répète le token reçu.
		fmt.Fprintf(w, `{"error":"bad token %s","message":"denied"}`, r.Header.Get("X-Api-Token"))
	}))
	defer srv.Close()

	spec := mustSpec(t, map[string]any{
		"source":  map[string]any{"url": srv.URL},
		"auth":    map[string]any{"mode": "header", "name": "X-Api-Token", "value": "{{credentials.apikey}}"},
		"records": map[string]any{"path": "carts"},
	})

	_, err := fastEngine(nil).Fetch(context.Background(), spec, Vars{
		Date:        "2026-08-12",
		Credentials: map[string]any{"apikey": "SECRET-ECHOED-BACK"},
	}, func(map[string]any) error { return nil })

	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SECRET-ECHOED-BACK") {
		t.Fatalf("the body excerpt leaks the credential: %q", err.Error())
	}
	// Le reste du corps doit rester lisible, sinon l'extrait ne sert à rien.
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("the actionable part of the body should survive redaction, got %q", err.Error())
	}
}

// #endregion

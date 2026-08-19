package httpsource

import (
	"fmt"
	"strings"
	"testing"
)

// #region TestRedactor_MasksValues
func TestRedactor_MasksValues(t *testing.T) {
	r := NewRedactor(map[string]any{
		"apikey": "SUPER-SECRET",
		"tenant": "acme-corp",
	})

	got := r.String("call to /v1/sales failed with key SUPER-SECRET for acme-corp")
	if strings.Contains(got, "SUPER-SECRET") || strings.Contains(got, "acme-corp") {
		t.Fatalf("values not masked: %q", got)
	}
	if !strings.Contains(got, "/v1/sales") {
		t.Errorf("the rest of the message must stay readable: %q", got)
	}
}

// #endregion

// #region TestRedactor_LongestFirst
// Un secret court contenu dans un secret long doit être masqué APRÈS lui, sinon il le
// découpe et laisse des fragments du long en clair.
func TestRedactor_LongestFirst(t *testing.T) {
	r := NewRedactor(map[string]any{
		"short": "abcd",
		"long":  "abcd-efgh-ijkl",
	})

	got := r.String("token=abcd-efgh-ijkl")
	if strings.Contains(got, "efgh") {
		t.Fatalf("fragments of the long secret leaked: %q", got)
	}
}

// #endregion

// #region TestRedactor_NestedAndArrayValues
// Une credential peut être un objet ou un tableau (headers, certificat en morceaux) :
// les feuilles doivent être masquées, pas seulement le premier niveau.
func TestRedactor_NestedAndArrayValues(t *testing.T) {
	r := NewRedactor(map[string]any{
		"headers": map[string]any{"X-Token": "NESTED-SECRET"},
		"keys":    []any{"ARRAY-SECRET"},
	})

	got := r.String("used NESTED-SECRET and ARRAY-SECRET")
	if strings.Contains(got, "NESTED-SECRET") || strings.Contains(got, "ARRAY-SECRET") {
		t.Fatalf("nested values not masked: %q", got)
	}
}

// #endregion

// #region TestRedactor_IgnoresVeryShortValues
// Masquer "eu" ou "1" remplacerait ces caractères partout et rendrait les messages
// illisibles, pour une valeur qui n'est de toute façon pas un secret exploitable.
func TestRedactor_IgnoresVeryShortValues(t *testing.T) {
	r := NewRedactor(map[string]any{"region": "eu", "version": "1"})

	got := r.String("region eu version 1 queue full")
	if got != "region eu version 1 queue full" {
		t.Fatalf("short values should not be masked, got %q", got)
	}
}

// #endregion

// #region TestRedactor_Add
func TestRedactor_Add(t *testing.T) {
	r := NewRedactor(nil)
	r.Add("RUNTIME-TOKEN")
	if got := r.String("bearer RUNTIME-TOKEN"); strings.Contains(got, "RUNTIME-TOKEN") {
		t.Fatalf("added value not masked: %q", got)
	}

	// Une valeur trop courte reste ignorée, même ajoutée à la main.
	r.Add("ab")
	if got := r.String("ab cd"); got != "ab cd" {
		t.Errorf("short added value should be ignored, got %q", got)
	}
}

// #endregion

// #region TestRedactor_ErrMasksMessageAndCause
// Une erreur imbriquée (url.Error typiquement) réexpose l'URL complète — donc le token
// — via son propre Error(). Il faut donc masquer la cause aussi.
func TestRedactor_ErrMasksMessageAndCause(t *testing.T) {
	r := NewRedactor(map[string]any{"apikey": "SECRET-VALUE"})

	original := newErr(KindUnavailable, 502, fmt.Errorf("dial https://api?key=SECRET-VALUE"), "call failed with SECRET-VALUE")
	masked := r.Err(original)

	if strings.Contains(masked.Error(), "SECRET-VALUE") {
		t.Fatalf("the masked error still leaks: %q", masked.Error())
	}

	// L'erreur d'origine ne doit PAS être mutée : l'appelant peut en garder une copie.
	if !strings.Contains(original.Error(), "SECRET-VALUE") {
		t.Error("Err must return a new error, not mutate the original")
	}

	// Kind et Status doivent survivre au masquage, sinon le proc ne peut plus traduire
	// l'erreur en code Quanti.
	if KindOf(masked) != KindUnavailable {
		t.Errorf("kind lost: got %v", KindOf(masked))
	}
	if e, ok := masked.(*Error); !ok || e.Status != 502 {
		t.Errorf("status lost: %#v", masked)
	}
}

// #endregion

// #region TestRedactor_NilSafety
func TestRedactor_NilSafety(t *testing.T) {
	var r *Redactor
	if got := r.String("plain"); got != "plain" {
		t.Errorf("nil redactor should be a passthrough, got %q", got)
	}
	if err := r.Err(nil); err != nil {
		t.Errorf("nil error should stay nil, got %v", err)
	}
	if got := NewRedactor(nil).Err(nil); got != nil {
		t.Errorf("nil error should stay nil, got %v", got)
	}
}

// #endregion

package httpsource

import (
	"strings"
	"testing"
)

func testVars() Vars {
	return Vars{
		Date:          "2026-08-12",
		StartDate:     "2026-08-01",
		EndDate:       "2026-08-31",
		AdAccountID:   "acc-1",
		AdAccountName: "Account One",
		Credentials: map[string]any{
			"apikey": "SECRET-KEY",
			"tenant": "acme",
			"nested": map[string]any{"token": "NESTED-TOKEN"},
			"numid":  float64(1234567),
		},
		ConnectorConf: map[string]any{"region": "eu"},
		Extra:         map[string]any{"foo": "bar"},
	}
}

// #region TestRender_Substitutions
func TestRender_Substitutions(t *testing.T) {
	cases := []struct{ tmpl, want string }{
		{"", ""},
		{"no variable", "no variable"},
		{"{{date}}", "2026-08-12"},
		{"{{ date }}", "2026-08-12"},
		{"from={{startDate}}&to={{endDate}}", "from=2026-08-01&to=2026-08-31"},
		{"https://api/{{credentials.tenant}}/sales", "https://api/acme/sales"},
		{"{{credentials.nested.token}}", "NESTED-TOKEN"},
		{"{{adAccount.id}}", "acc-1"},
		{"{{adAccount.name}}", "Account One"},
		{"{{connectorConf.region}}", "eu"},
		{"{{extra.foo}}", "bar"},
		{"{{date}}/{{date}}", "2026-08-12/2026-08-12"},
		// Les entiers JSON arrivent en float64 : sans traitement, un gros ID sortirait
		// en "1.234567e+06" et l'API renverrait un 404 incompréhensible.
		{"{{credentials.numid}}", "1234567"},
	}

	for _, c := range cases {
		got, err := Render(c.tmpl, testVars())
		if err != nil {
			t.Errorf("Render(%q): unexpected error %v", c.tmpl, err)
			continue
		}
		if got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

// #endregion

// #region TestRender_FormatFilter
func TestRender_FormatFilter(t *testing.T) {
	cases := []struct{ tmpl, want string }{
		{"{{date|format:2006-01-02}}", "2026-08-12"},
		{"{{date|format:02/01/2006}}", "12/08/2026"},
		{"{{date|format:20060102}}", "20260812"},
		{"{{date | format:2006-01}}", "2026-08"},
		{"{{date|format:epoch}}", "1786492800"},
	}
	for _, c := range cases {
		got, err := Render(c.tmpl, testVars())
		if err != nil {
			t.Errorf("Render(%q): unexpected error %v", c.tmpl, err)
			continue
		}
		if got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

// #endregion

// #region TestRender_StrictOnUnknownAndEmpty
// LE test le plus important du fichier. Une variable inconnue ou vide qui se
// substituerait en chaîne vide produirait une URL du type `?date=` — que beaucoup
// d'API acceptent avec un 200 et un jeu de données FAUX. Des données silencieusement
// erronées en base sont pires qu'un échec.
func TestRender_StrictOnUnknownAndEmpty(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		vars Vars
		want string
	}{
		{"unknown variable", "{{nope}}", testVars(), "unknown template variable"},
		{"typo in prefix", "{{credential.apikey}}", testVars(), "unknown template variable"},
		{"empty date", "{{date}}", Vars{}, "is empty for this request"},
		{"missing credential key", "{{credentials.absent}}", testVars(), "not found"},
		{"no credentials at all", "{{credentials.apikey}}", Vars{Date: "2026-08-12"}, "no credentials available"},
		{"unknown filter", "{{date|upper:x}}", testVars(), "unknown filter"},
		{"format without layout", "{{date|format:}}", testVars(), "requires a layout"},
		{"unclosed braces", "{{date", testVars(), "unresolved template expression"},
		{"space inside name", "{{ad Account.id}}", testVars(), "unresolved template expression"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Render(c.tmpl, c.vars)
			if err == nil {
				t.Fatalf("Render(%q) should have failed", c.tmpl)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("message %q should contain %q", err.Error(), c.want)
			}
		})
	}
}

// #endregion

// #region TestRender_ErrorNeverLeaksSecretValues
// Les messages d'erreur de template remontent dans les logs : ils doivent citer le
// CHEMIN de la credential, jamais sa valeur.
func TestRender_ErrorNeverLeaksSecretValues(t *testing.T) {
	_, err := Render("{{credentials.nested.absent}}", testVars())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "NESTED-TOKEN") || strings.Contains(err.Error(), "SECRET-KEY") {
		t.Fatalf("the error leaks a credential value: %q", err.Error())
	}
}

// #endregion

// #region TestRenderMap
func TestRenderMap(t *testing.T) {
	out, err := RenderMap(map[string]string{
		"fromDate": "{{date}}",
		"toDate":   "{{date}}",
		"key":      "{{credentials.apikey}}",
	}, testVars())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["fromDate"] != "2026-08-12" || out["toDate"] != "2026-08-12" || out["key"] != "SECRET-KEY" {
		t.Errorf("unexpected result: %#v", out)
	}

	if out, err := RenderMap(nil, testVars()); err != nil || out != nil {
		t.Errorf("nil map should yield (nil, nil), got (%#v, %v)", out, err)
	}
}

// #endregion

// #region TestRenderMap_ErrorNamesTheKey
// Sur une spec à 10 paramètres, une erreur qui ne dit pas QUEL paramètre est fautif
// oblige à tout relire.
func TestRenderMap_ErrorNamesTheKey(t *testing.T) {
	_, err := RenderMap(map[string]string{"fromDate": "{{date}}", "broken": "{{nope}}"}, testVars())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `key "broken"`) {
		t.Errorf("the message should name the faulty key, got %q", err.Error())
	}
}

// #endregion

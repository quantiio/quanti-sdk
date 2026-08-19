package httpsource

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const credentialsPrefix = "credentials."

// Vars est le contexte de substitution d'une requête. Un seul jeu de variables pour
// toute la spec (URL, query, headers, body, auth, inject) : pas de règle différente
// selon l'endroit, sinon le conf.yml devient impossible à relire.
type Vars struct {
	// Date est la journée traitée (format 2006-01-02). Vide pour une dimension.
	Date string
	// StartDate / EndDate bornent le process en cours (utile aux API qui n'acceptent
	// qu'un intervalle, pas une date unique).
	StartDate string
	EndDate   string

	Credentials   map[string]any
	ConnectorConf map[string]any

	AdAccountID   string
	AdAccountName string

	// Extra permet à un connecteur d'exposer ses propres variables sans modifier le
	// moteur (accessibles via {{extra.<clé>}}).
	Extra map[string]any
}

// varPattern capture {{ nom }} et {{ nom|filtre:arg }}. Volontairement restrictif sur
// les caractères autorisés : un `{{` non fermé ou une expression exotique doit
// remonter comme variable inconnue, pas être ignoré silencieusement.
var varPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.\-]+)\s*(?:\|\s*([a-zA-Z0-9_]+)\s*:\s*([^}]*?)\s*)?\}\}`)

// #region Render
// Render substitue les variables de tmpl.
//
// STRICT par construction : une variable inconnue ou vide est une ERREUR, pas une
// chaîne vide. Une URL qui devient `?date=` au lieu de `?date=2026-08-12` renvoie
// souvent un 200 avec un jeu de données faux — c'est le pire scénario possible
// (données silencieusement erronées en base). Mieux vaut échouer bruyamment.
func Render(tmpl string, vars Vars) (string, error) {
	if tmpl == "" {
		return "", nil
	}

	var firstErr error
	out := varPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		groups := varPattern.FindStringSubmatch(match)
		name, filter, arg := groups[1], groups[2], groups[3]

		value, err := vars.lookup(name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return match
		}

		rendered, err := applyFilter(value, filter, arg)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("variable %q: %w", name, err)
			}
			return match
		}
		return rendered
	})

	if firstErr != nil {
		return "", firstErr
	}

	// Un `{{` résiduel signifie que le motif n'a pas reconnu l'expression (accolade
	// non fermée, espace parasite, caractère interdit). Laisser passer produirait une
	// URL contenant littéralement "{{date}}".
	if strings.Contains(out, "{{") {
		return "", newErr(KindInvalidSpec, 0, nil,
			"unresolved template expression in %q — check the {{…}} syntax", truncate(tmpl, 80))
	}

	return out, nil
}

// #endregion

// #region RenderMap
// RenderMap applique Render à toutes les valeurs d'une map. Les clés ne sont PAS
// templatées : un nom de header ou de paramètre dynamique rendrait le conf.yml
// illisible pour un gain nul.
func RenderMap(in map[string]string, vars Vars) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))

	// Ordre stable : deux specs identiques doivent produire la MÊME erreur, sinon on
	// obtient un message différent d'un run à l'autre (cauchemar à diagnostiquer).
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v, err := Render(in[k], vars)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

// #endregion

// #region lookup
func (v Vars) lookup(name string) (any, error) {
	switch {
	case name == "date":
		return v.requireNonEmpty("date", v.Date)
	case name == "startDate":
		return v.requireNonEmpty("startDate", v.StartDate)
	case name == "endDate":
		return v.requireNonEmpty("endDate", v.EndDate)
	case name == "adAccount.id":
		return v.requireNonEmpty("adAccount.id", v.AdAccountID)
	case name == "adAccount.name":
		return v.requireNonEmpty("adAccount.name", v.AdAccountName)

	case strings.HasPrefix(name, credentialsPrefix):
		return lookupNested(v.Credentials, strings.TrimPrefix(name, credentialsPrefix), "credentials")
	case strings.HasPrefix(name, "connectorConf."):
		return lookupNested(v.ConnectorConf, strings.TrimPrefix(name, "connectorConf."), "connectorConf")
	case strings.HasPrefix(name, "extra."):
		return lookupNested(v.Extra, strings.TrimPrefix(name, "extra."), "extra")

	default:
		return nil, newErr(KindInvalidSpec, 0, nil,
			"unknown template variable {{%s}} (available: date, startDate, endDate, adAccount.id, adAccount.name, credentials.*, connectorConf.*, extra.*)", name)
	}
}

// #endregion

// #region requireNonEmpty
func (v Vars) requireNonEmpty(name, value string) (any, error) {
	if value == "" {
		return nil, newErr(KindInvalidSpec, 0, nil,
			"template variable {{%s}} is empty for this request — remove it from the spec or check the report configuration", name)
	}
	return value, nil
}

// #endregion

// #region lookupNested
// lookupNested résout un chemin pointé dans une map (ex: credentials.oauth.token).
// Le message d'erreur ne cite JAMAIS la valeur, seulement le chemin : on est en train
// de manipuler des secrets.
func lookupNested(src map[string]any, path, root string) (any, error) {
	if len(src) == 0 {
		return nil, newErr(KindInvalidSpec, 0, nil, "{{%s.%s}}: no %s available for this account", root, path, root)
	}

	var current any = src
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, newErr(KindInvalidSpec, 0, nil, "{{%s.%s}}: %q is not an object", root, path, part)
		}
		current, ok = m[part]
		if !ok {
			return nil, newErr(KindInvalidSpec, 0, nil,
				"{{%s.%s}}: key %q not found (available: %s)", root, path, part, strings.Join(sortedMapKeys(m), ", "))
		}
	}

	if current == nil {
		return nil, newErr(KindInvalidSpec, 0, nil, "{{%s.%s}} is null", root, path)
	}
	if s, ok := current.(string); ok && s == "" {
		return nil, newErr(KindInvalidSpec, 0, nil, "{{%s.%s}} is empty", root, path)
	}
	return current, nil
}

// #endregion

// #region applyFilter
// applyFilter gère les filtres de template. Un seul pour l'instant : `format` sur une
// date. On garde le mécanisme extensible mais on n'ajoute un filtre que quand une API
// réelle l'exige — chaque filtre est une syntaxe de plus à connaître pour relire un
// conf.yml.
func applyFilter(value any, filter, arg string) (string, error) {
	str := stringify(value)

	switch filter {
	case "":
		return str, nil

	case "format":
		if arg == "" {
			return "", fmt.Errorf("filter format requires a layout, e.g. |format:2006-01-02")
		}
		// La valeur d'entrée est une date au format Quanti (2006-01-02) ; on la
		// reformate au layout Go demandé. `epoch` est traité à part car ce n'est pas
		// un layout Go.
		parsed, err := time.Parse("2006-01-02", str)
		if err != nil {
			return "", fmt.Errorf("value %q is not a YYYY-MM-DD date, cannot apply format", str)
		}
		if arg == "epoch" {
			return strconv.FormatInt(parsed.Unix(), 10), nil
		}
		return parsed.Format(arg), nil

	default:
		return "", fmt.Errorf("unknown filter %q (available: format)", filter)
	}
}

// #endregion

// #region stringify
// stringify convertit une valeur de credential/conf en chaîne. Les entiers JSON
// arrivent en float64 : les rendre via %v produirait "1.234567e+06" pour un gros ID.
func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return stringify(float64(t))
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// #endregion

// #region sortedMapKeys
func sortedMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// #endregion

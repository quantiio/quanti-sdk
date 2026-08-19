package httpsource

import (
	"encoding/csv"
	"encoding/json"
	"strings"
)

// #region extractRecords
// extractRecords transforme un corps de réponse en lignes prêtes à l'upsert.
//
// Le pipeline est : parse (json/csv) → navigation vers records.path → explode →
// inject. L'ordre compte : `inject` s'applique APRÈS l'explode pour que chaque ligne
// produite porte bien les champs injectés (sinon une commande à 3 articles aurait la
// date sur une seule de ses 3 lignes).
func extractRecords(body []byte, spec *Spec, vars Vars) ([]map[string]any, error) {
	var rows []map[string]any
	var err error

	switch spec.Records.Format {
	case FormatCSV:
		rows, err = parseCSV(body, spec.Records.CSV)
	default:
		rows, err = parseJSONRecords(body, spec.Records.Path)
	}
	if err != nil {
		return nil, err
	}

	if spec.Records.Explode != "" {
		rows = explodeRows(rows, spec.Records.Explode, *spec.Records.EmitWhenExplodeEmpty)
	}

	if len(spec.Records.Inject) > 0 {
		injected, err := RenderMap(spec.Records.Inject, vars)
		if err != nil {
			return nil, newErr(KindInvalidSpec, 0, err, "records.inject cannot be rendered")
		}
		for _, row := range rows {
			for k, v := range injected {
				row[k] = v
			}
		}
	}

	return rows, nil
}

// #endregion

// #region parseJSONRecords
// parseJSONRecords navigue jusqu'à records.path et normalise le résultat en liste de
// lignes. Accepte un tableau (cas normal) ou un objet unique (endpoint "détail" qui
// renvoie une seule ressource) — refuser l'objet obligerait à écrire un connecteur
// dédié pour un cas courant.
func parseJSONRecords(body []byte, path string) ([]map[string]any, error) {
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, newErr(KindInvalidData, 0, err, "response is not valid JSON (first bytes: %q)", truncate(string(body), 120))
	}

	target, err := navigatePath(parsed, path)
	if err != nil {
		return nil, err
	}

	switch t := target.(type) {
	case nil:
		// Chemin présent mais null : pas de donnée pour cette date, ce n'est pas une
		// erreur (une journée sans commande est un cas métier normal).
		return nil, nil

	case []any:
		rows := make([]map[string]any, 0, len(t))
		for i, item := range t {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, newErr(KindInvalidData, 0, nil,
					"records.path %q: element %d is a %T, expected an object", path, i, item)
			}
			rows = append(rows, m)
		}
		return rows, nil

	case map[string]any:
		return []map[string]any{t}, nil

	default:
		return nil, newErr(KindInvalidData, 0, nil,
			"records.path %q points to a %T, expected an array or an object", path, target)
	}
}

// #endregion

// #region navigatePath
// navigatePath descend un chemin pointé dans une structure JSON. Un path vide renvoie
// la racine (cas d'une API qui répond directement un tableau).
func navigatePath(data any, path string) (any, error) {
	if strings.TrimSpace(path) == "" {
		return data, nil
	}

	current := data
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, newErr(KindInvalidData, 0, nil,
				"records.path %q: %q is not reachable, the parent is a %T", path, part, current)
		}
		value, exists := m[part]
		if !exists {
			return nil, newErr(KindInvalidData, 0, nil,
				"records.path %q: key %q not found in the response (available: %s)",
				path, part, strings.Join(sortedMapKeys(m), ", "))
		}
		current = value
	}
	return current, nil
}

// #endregion

// #region navigateOptional
// navigateOptional est la variante tolérante utilisée par la pagination : un curseur
// ou un `has_more` absent signifie "fin de pagination", pas une erreur de spec.
func navigateOptional(data any, path string) (any, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}
	current := data
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, exists := m[part]
		if !exists {
			return nil, false
		}
		current = value
	}
	return current, current != nil
}

// #endregion

// #region explodeRows
// explodeRows produit une ligne par élément du champ `field`, en remplaçant ce champ
// par l'élément en OBJET SINGULIER.
//
// ⚠️ Le remplacement par un objet (et non par un tableau d'un élément) est le cœur du
// comportement : le flatten de processor-v2 produit alors `data.items.<champ>` et non
// `data.items.0.<champ>`, ce qui est indispensable pour que les `fieldPath` du schéma
// matchent. C'est reproduit à l'identique du connecteur medusa v1.
//
// Un élément non-objet (tableau de scalaires) est conservé tel quel : mieux vaut une
// colonne scalaire que perdre la ligne.
func explodeRows(rows []map[string]any, field string, emitWhenEmpty bool) []map[string]any {
	out := make([]map[string]any, 0, len(rows))

	for _, row := range rows {
		items, ok := row[field].([]any)
		if !ok || len(items) == 0 {
			if emitWhenEmpty {
				out = append(out, row)
			}
			continue
		}

		for _, item := range items {
			clone := make(map[string]any, len(row))
			for k, v := range row {
				clone[k] = v
			}
			clone[field] = item
			out = append(out, clone)
		}
	}

	return out
}

// #endregion

// #region parseCSV
// parseCSV lit un export CSV en lignes. Toutes les valeurs restent des STRINGS : le
// typage est la responsabilité du schéma (`databaseMetaData.type`) et de
// processor-v2, pas du moteur — deviner ici produirait des types incohérents d'un
// export à l'autre selon le contenu de la première page.
func parseCSV(body []byte, opts *CSVOptions) ([]map[string]any, error) {
	if opts == nil {
		opts = &CSVOptions{}
	}

	reader := csv.NewReader(strings.NewReader(string(body)))
	reader.FieldsPerRecord = -1 // lignes de longueur variable tolérées
	reader.LazyQuotes = true    // exports réels mal échappés : on préfère lire que rejeter
	if opts.Delimiter != "" {
		reader.Comma = rune(opts.Delimiter[0])
	}

	records, err := reader.ReadAll()
	if err != nil {
		return nil, newErr(KindInvalidData, 0, err, "response is not valid CSV")
	}
	if len(records) == 0 {
		return nil, nil
	}

	columns := opts.Columns
	start := 0
	if opts.HasHeader == nil || *opts.HasHeader {
		if len(columns) == 0 {
			columns = records[0]
		}
		start = 1
	}

	rows := make([]map[string]any, 0, len(records)-start)
	for _, record := range records[start:] {
		if len(record) == 1 && strings.TrimSpace(record[0]) == "" {
			continue // ligne vide en fin de fichier
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			if i < len(record) {
				row[col] = record[i]
			} else {
				// Colonne absente en fin de ligne : nil et pas "" — nil devient NULL en
				// base, "" deviendrait une chaîne vide, ce qui n'est pas la même chose.
				row[col] = nil
			}
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// #endregion

package httpsource

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// paginator porte l'état d'avancement dans les pages d'une requête. Une instance par
// appel à Fetch : jamais partagée, donc pas de fuite d'état d'une date sur l'autre.
type paginator struct {
	spec *Pagination

	page   int
	offset int
	cursor string

	// nextURL court-circuite la construction d'URL quand l'API fournit elle-même le
	// lien suivant (types next_url et link_header).
	nextURL string

	done bool
}

// #region newPaginator
func newPaginator(spec *Pagination) *paginator {
	return &paginator{
		spec:   spec,
		page:   spec.StartAt,
		offset: 0,
	}
}

// #endregion

// #region applyTo
// applyTo pose les paramètres de pagination sur la query de la requête courante.
// Ne touche à rien pour la première page en mode cursor/next_url : le curseur n'existe
// pas encore, et envoyer `cursor=` vide fait renvoyer une erreur 400 à certaines API.
func (p *paginator) applyTo(query map[string]string) {
	switch p.spec.Type {
	case PagePage:
		query[p.spec.Param] = strconv.Itoa(p.page)
		if p.spec.SizeParam != "" && p.spec.Size > 0 {
			query[p.spec.SizeParam] = strconv.Itoa(p.spec.Size)
		}

	case PageOffset:
		query[p.spec.Param] = strconv.Itoa(p.offset)
		if p.spec.SizeParam != "" && p.spec.Size > 0 {
			query[p.spec.SizeParam] = strconv.Itoa(p.spec.Size)
		}

	case PageCursor:
		if p.cursor != "" {
			query[p.spec.Param] = p.cursor
		}
	}
}

// #endregion

// #region advance
// advance calcule l'état de la page suivante depuis la réponse reçue et indique s'il
// faut continuer.
//
// `parsed` est le corps JSON déjà décodé (nil en CSV), `rowCount` le nombre de lignes
// extraites de cette page, `header` les en-têtes de la réponse.
func (p *paginator) advance(parsed any, rowCount int, header http.Header) bool {
	switch p.spec.Type {
	case PageNone:
		return false

	case PagePage:
		// Page vide ⇒ terminé. C'est le signal universel, même quand l'API annonce un
		// nombre total de pages (elle se trompe parfois).
		if rowCount == 0 {
			return false
		}
		if p.spec.StopWhen == "totalPages" {
			if total, ok := navigateOptional(parsed, p.spec.TotalPagesPath); ok {
				if totalPages := toInt(total); totalPages > 0 && p.page >= totalPages {
					return false
				}
			}
		}
		// Page incomplète ⇒ c'était la dernière. Économise un aller-retour par
		// requête, ce qui compte quand on collecte 365 jours d'historique.
		if p.spec.Size > 0 && rowCount < p.spec.Size {
			return false
		}
		p.page++
		return true

	case PageOffset:
		if rowCount == 0 {
			return false
		}
		if rowCount < p.spec.Size {
			return false
		}
		p.offset += p.spec.Size
		return true

	case PageCursor:
		// hasMorePath est prioritaire s'il est déclaré : une API peut renvoyer un
		// curseur non vide sur la dernière page, et s'y fier ferait boucler jusqu'à
		// maxPages.
		if p.spec.HasMorePath != "" {
			hasMore, ok := navigateOptional(parsed, p.spec.HasMorePath)
			if !ok || !toBool(hasMore) {
				return false
			}
		}
		cursor, ok := navigateOptional(parsed, p.spec.CursorPath)
		if !ok {
			return false
		}
		next := stringify(cursor)
		// Un curseur identique au précédent = l'API tourne en rond. Sans ce garde-fou
		// on redemande la même page jusqu'à maxPages, en dupliquant les lignes.
		if next == "" || next == p.cursor {
			return false
		}
		p.cursor = next
		return true

	case PageNextURL:
		next, ok := navigateOptional(parsed, p.spec.NextURLPath)
		if !ok {
			return false
		}
		url := stringify(next)
		if url == "" || url == p.nextURL {
			return false
		}
		p.nextURL = url
		return true

	case PageLinkHeader:
		next := parseLinkHeaderNext(header.Get("Link"))
		if next == "" || next == p.nextURL {
			return false
		}
		p.nextURL = next
		return true

	default:
		return false
	}
}

// #endregion

// #region overrideURL
// overrideURL retourne l'URL absolue à utiliser pour la page courante, ou "" s'il faut
// reconstruire l'URL depuis la spec.
func (p *paginator) overrideURL() string {
	if p.spec.Type == PageNextURL || p.spec.Type == PageLinkHeader {
		return p.nextURL
	}
	return ""
}

// #endregion

// linkHeaderRel extrait l'URL et le rel d'un maillon d'en-tête Link (RFC 5988),
// format `<https://api/x?page=2>; rel="next"`.
var linkHeaderRel = regexp.MustCompile(`<([^>]+)>\s*;\s*rel\s*=\s*"?([^"\s;]+)"?`)

// #region parseLinkHeaderNext
func parseLinkHeaderNext(header string) string {
	if header == "" {
		return ""
	}
	for _, match := range linkHeaderRel.FindAllStringSubmatch(header, -1) {
		if strings.EqualFold(match[2], "next") {
			return match[1]
		}
	}
	return ""
}

// #endregion

// #region toInt
func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// #endregion

// #region toBool
// toBool accepte les formes rencontrées en vrai : booléen JSON, mais aussi "true" et
// 1 (des API renvoient has_more en string ou en entier).
func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		return err == nil && b
	case float64:
		return t != 0
	default:
		return false
	}
}

// #endregion

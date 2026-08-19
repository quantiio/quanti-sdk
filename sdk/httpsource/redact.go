package httpsource

import (
	"sort"
	"strings"
)

// Redactor remplace les valeurs sensibles par un masque dans tout texte sortant du
// moteur (logs ET messages d'erreur).
//
// POURQUOI c'est un composant à part et pas un `strings.Replace` local : les erreurs
// du moteur remontent jusqu'à `qm_process.last_execution_message`, visible côté client
// et côté admin, et les logs partent dans Loki. Une clé d'API passée en query param
// (cas très courant) se retrouverait donc archivée en clair à deux endroits. Le
// masquage doit être appliqué en UN SEUL point de sortie, sinon on l'oublie.
type Redactor struct {
	// values est trié par longueur décroissante : masquer d'abord les valeurs longues
	// évite qu'un secret court (ex: un id de tenant "42") ne découpe un secret long
	// qui le contient, laissant des fragments en clair.
	values []string
}

const redactionMask = "***"

// #region NewRedactor
// NewRedactor construit un rédacteur à partir des valeurs de credentials RÉSOLUES.
//
// On ignore les valeurs très courtes (< 4 caractères) : masquer "1" ou "eu"
// remplacerait ces caractères partout dans les messages et les rendrait illisibles,
// pour une valeur qui n'est de toute façon pas un secret exploitable.
func NewRedactor(secrets map[string]any) *Redactor {
	r := &Redactor{}
	for _, v := range secrets {
		r.addValue(v)
	}
	r.sortValues()
	return r
}

// #endregion

// #region addValue
func (r *Redactor) addValue(v any) {
	switch t := v.(type) {
	case string:
		if len(t) >= 4 {
			r.values = append(r.values, t)
		}
	case map[string]any:
		// Une credential peut être un objet (headers, certificat en morceaux…) : on
		// descend, sinon les feuilles sensibles échapperaient au masquage.
		for _, nested := range t {
			r.addValue(nested)
		}
	case []any:
		for _, nested := range t {
			r.addValue(nested)
		}
	}
}

// #endregion

// #region sortValues
func (r *Redactor) sortValues() {
	sort.Slice(r.values, func(i, j int) bool {
		return len(r.values[i]) > len(r.values[j])
	})
}

// #endregion

// #region Add
// Add enregistre une valeur sensible supplémentaire, découverte en cours de route —
// typiquement un access_token obtenu par un échange OAuth2, qui n'existait pas dans
// les credentials du compte et qui, sans ça, apparaîtrait en clair dans un message
// d'erreur de la requête suivante.
func (r *Redactor) Add(value string) {
	if r == nil || len(value) < 4 {
		return
	}
	r.values = append(r.values, value)
	r.sortValues()
}

// #endregion

// #region String
// String masque toutes les valeurs connues dans s.
func (r *Redactor) String(s string) string {
	if r == nil || s == "" {
		return s
	}
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, redactionMask)
	}
	return s
}

// #endregion

// #region Err
// Err masque le message d'une erreur du moteur. Retourne une NOUVELLE erreur : muter
// celle d'origine masquerait aussi la copie conservée par l'appelant.
//
// Cause est volontairement écrasée par sa version masquée sous forme de texte : une
// erreur imbriquée (url.Error notamment) réexpose l'URL complète — donc le token —
// via son propre Error(), et on ne peut pas la masquer sans la remplacer.
func (r *Redactor) Err(err error) error {
	if err == nil {
		return nil
	}
	if r == nil {
		return err
	}

	e, ok := err.(*Error)
	if !ok {
		return &Error{Kind: KindOf(err), Message: r.String(err.Error())}
	}

	out := &Error{
		Kind:    e.Kind,
		Message: r.String(e.Message),
		Status:  e.Status,
	}
	if e.Cause != nil {
		out.Cause = &redactedCause{msg: r.String(e.Cause.Error())}
	}
	return out
}

// #endregion

// redactedCause porte une cause déjà masquée. Type dédié plutôt que errors.New pour
// que l'intention reste lisible dans une stack ou un debug.
type redactedCause struct{ msg string }

// #region redactedCause.Error
func (c *redactedCause) Error() string { return c.msg }

// #endregion

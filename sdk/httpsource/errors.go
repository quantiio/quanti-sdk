package httpsource

import "fmt"

// Kind classe une erreur du moteur par NATURE, sans jamais nommer de code d'erreur
// Quanti : le module ne doit pas importer le package `sdk` (le proc, lui, importe les
// deux, et fait la traduction Kind → QError). Ça garde `httpsource` testable seul et
// réutilisable hors contexte processor.
//
// Table de correspondance attendue côté proc :
//
//	KindInvalidSpec    → ERR_DEF_INVALID_REQUEST     (notre conf.yml est faux)
//	KindAuth           → ERR_DEF_AUTH_NOT_VALID      (401/403 : credentials à refaire)
//	KindRateLimit      → ERR_TMP_RATE_LIMIT_EXCEEDED (429 après épuisement des retries)
//	KindUnavailable    → ERR_DEF_API_UNAVAILABLE     (5xx, réseau, timeout)
//	KindInvalidData    → ERR_DEF_INVALID_DATA        (réponse illisible / path absent)
//	KindStopped        → pas une erreur : arrêt demandé par l'appelant
type Kind int

const (
	// KindInvalidSpec : la spec du conf.yml est invalide ou intemplatable. C'est
	// NOTRE erreur, pas celle du client ni de l'API tierce — elle doit être fatale et
	// visible, jamais retentée.
	KindInvalidSpec Kind = iota
	KindAuth
	KindRateLimit
	KindUnavailable
	KindInvalidData
	KindStopped
)

// #region String
func (k Kind) String() string {
	switch k {
	case KindInvalidSpec:
		return "invalid_spec"
	case KindAuth:
		return "auth"
	case KindRateLimit:
		return "rate_limit"
	case KindUnavailable:
		return "unavailable"
	case KindInvalidData:
		return "invalid_data"
	case KindStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// #endregion

// Error est l'erreur du moteur. Status porte le code HTTP quand il y en a un (0
// sinon), utile au proc pour enrichir le message client.
type Error struct {
	Kind    Kind
	Message string
	Status  int
	Cause   error
}

// #region Error
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	switch {
	case e.Status != 0 && e.Cause != nil:
		return fmt.Sprintf("%s (HTTP %d): %s: %v", e.Kind, e.Status, e.Message, e.Cause)
	case e.Status != 0:
		return fmt.Sprintf("%s (HTTP %d): %s", e.Kind, e.Status, e.Message)
	case e.Cause != nil:
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	default:
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
}

// #endregion

// #region Unwrap
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// #endregion

// #region newErr
func newErr(kind Kind, status int, cause error, format string, args ...any) *Error {
	return &Error{
		Kind:    kind,
		Message: fmt.Sprintf(format, args...),
		Status:  status,
		Cause:   cause,
	}
}

// #endregion

// #region ErrStop
// ErrStop est la sentinelle qu'un callback `emit` renvoie pour interrompre la
// collecte sans que ce soit une erreur. C'est ce qui permet à `test-query` de ne
// prendre que les N premières lignes puis de s'arrêter, au lieu de dérouler toute la
// pagination d'une API pour rien.
var ErrStop = &Error{Kind: KindStopped, Message: "iteration stopped by caller"}

// #endregion

// #region KindOf
// KindOf extrait le Kind d'une erreur du moteur. Retourne KindUnavailable pour une
// erreur étrangère : c'est le classement le plus sûr (retentable, non fatal) pour
// quelque chose qu'on n'a pas produit et qu'on ne sait pas qualifier.
func KindOf(err error) Kind {
	if err == nil {
		return KindStopped
	}
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return KindUnavailable
}

// #endregion

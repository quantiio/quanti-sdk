package httpsource

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// attemptOutcome décrit ce qu'il faut faire après une tentative HTTP.
type attemptOutcome int

const (
	outcomeSuccess attemptOutcome = iota
	outcomeRetry
	outcomeFatal
)

// #region classify
// classify décide du sort d'une réponse HTTP.
//
// Découpage volontairement explicite plutôt qu'un "tout ce qui n'est pas 2xx est
// retentable" : réessayer un 401 ou un 400 ne sert à rien (on épuise les tentatives et
// on retarde le message d'erreur utile de plusieurs minutes), tandis que ne PAS
// réessayer un 429 casse la collecte sur une API à quota.
func classify(status int) (attemptOutcome, Kind) {
	switch {
	case status >= 200 && status < 300:
		return outcomeSuccess, KindStopped

	case status == http.StatusTooManyRequests:
		return outcomeRetry, KindRateLimit

	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// Non retentable en tant que tel. Le cas particulier du token OAuth expiré est
		// traité en amont dans engine.go (un seul renouvellement + une seule reprise),
		// pas ici : la boucle de retry ne doit pas marteler l'endpoint token.
		return outcomeFatal, KindAuth

	case status == http.StatusRequestTimeout || status == 425 || status == 449:
		return outcomeRetry, KindUnavailable

	case status >= 500:
		return outcomeRetry, KindUnavailable

	default:
		// 4xx restants (400, 404, 422…) : la requête est mauvaise, la réessayer
		// donnera exactement la même réponse.
		return outcomeFatal, KindInvalidRequestStatus(status)
	}
}

// #endregion

// #region KindInvalidRequestStatus
// KindInvalidRequestStatus classe un 4xx. 404 est traité comme "API indisponible"
// plutôt que comme une spec invalide : sur les API sur-mesure des clients, un 404
// signifie bien plus souvent "l'endpoint a bougé / est en maintenance" qu'une faute
// dans notre conf.yml, et un KindInvalidSpec pousserait l'admin à chercher au mauvais
// endroit.
func KindInvalidRequestStatus(status int) Kind {
	if status == http.StatusNotFound {
		return KindUnavailable
	}
	return KindInvalidSpec
}

// #endregion

// #region waitFor429
// waitFor429 calcule l'attente avant de réessayer un 429.
//
// Ordre de priorité : `retryAfterPath` dans le corps → en-tête `Retry-After` →
// backoff configuré. Puis on AJOUTE extraWaitSeconds, et on plafonne à
// maxWaitSeconds.
//
// ⚠️ extraWaitSeconds n'est pas une marge de confort : certaines API sous-estiment
// leur propre Retry-After, et le respecter à la lettre fait immédiatement reprendre un
// 429 (cf le +600 s des Échos). Le plafond, lui, évite qu'une API renvoyant
// `Retry-After: 86400` ne bloque un worker toute la journée.
func waitFor429(cfg *On429, header http.Header, body []byte, attempt int) time.Duration {
	seconds := 0

	if cfg.RespectRetryAfter {
		if cfg.RetryAfterPath != "" {
			if v := retryAfterFromBody(body, cfg.RetryAfterPath); v > 0 {
				seconds = v
			}
		}
		if seconds == 0 {
			seconds = retryAfterFromHeader(header)
		}
	}

	if seconds == 0 {
		seconds = cfg.BackoffSeconds
		if cfg.JitterMs == 0 {
			// Sans indication de l'API, un backoff exponentiel évite que N requêtes
			// bloquées repartent toutes au même instant.
			seconds = backoffSeconds(cfg.BackoffSeconds, attempt, true)
		}
	}

	seconds += cfg.ExtraWaitSeconds

	if cfg.MaxWaitSeconds > 0 && seconds > cfg.MaxWaitSeconds {
		seconds = cfg.MaxWaitSeconds
	}
	if seconds < 0 {
		seconds = 0
	}

	return time.Duration(seconds)*time.Second + jitter(cfg.JitterMs, attempt)
}

// #endregion

// #region retryAfterFromBody
func retryAfterFromBody(body []byte, path string) int {
	if len(body) == 0 {
		return 0
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0
	}
	value, ok := navigateOptional(parsed, path)
	if !ok {
		return 0
	}
	return toInt(value)
}

// #endregion

// #region retryAfterFromHeader
// retryAfterFromHeader gère les deux formes légales de Retry-After : un nombre de
// secondes, ou une date HTTP.
func retryAfterFromHeader(header http.Header) int {
	raw := header.Get("Retry-After")
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return seconds
	}
	if t, err := http.ParseTime(raw); err == nil {
		if d := int(time.Until(t).Seconds()); d > 0 {
			return d
		}
	}
	return 0
}

// #endregion

// #region waitForBackoff
func waitForBackoff(cfg *Backoff, attempt int) time.Duration {
	if cfg == nil {
		return 0
	}
	seconds := backoffSeconds(cfg.BackoffSeconds, attempt, cfg.Exponential)
	return time.Duration(seconds)*time.Second + jitter(cfg.JitterMs, attempt)
}

// #endregion

// #region backoffSeconds
// backoffSeconds double l'attente à chaque tentative quand exponential est actif, avec
// un plafond dur à 300 s : au-delà, on préfère abandonner la date et laisser le cron
// suivant la rattraper plutôt que d'immobiliser un worker.
func backoffSeconds(base, attempt int, exponential bool) int {
	if base <= 0 {
		base = 1
	}
	if !exponential {
		return base
	}
	seconds := base
	for i := 0; i < attempt; i++ {
		seconds *= 2
		if seconds >= 300 {
			return 300
		}
	}
	return seconds
}

// #endregion

// #region jitter
// jitter désynchronise les reprises. Déterministe (dérivé du numéro de tentative) et
// non aléatoire : les scripts du SDK interdisent math/rand pour garder les runs
// reproductibles, et le seul objectif ici est d'étaler les reprises, pas d'être
// imprévisible.
func jitter(maxMs, attempt int) time.Duration {
	if maxMs <= 0 {
		return 0
	}
	// Suite pseudo-aléatoire simple bornée à maxMs.
	offset := (attempt * 2654435761) % maxMs
	if offset < 0 {
		offset = -offset
	}
	return time.Duration(offset) * time.Millisecond
}

// #endregion

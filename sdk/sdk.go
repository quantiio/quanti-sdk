package sdk

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	DebugMode bool

	logger = logrus.New()
)

const (
	MsgTypeCredentials = "credentials"
	MsgTypeProcessed   = "processed"
	MsgTypeLog         = "log"
	MsgTypeCheckpoint  = "checkpoint"
)

// #region Debug
func Debug() {
	logger.SetFormatter(&logrus.TextFormatter{ForceColors: true, FullTimestamp: true})
}

// #region Process
func Process(processFunc func(ConfigFile, map[string]string, map[string]interface{})) error {

	time.Local = time.UTC

	configPath := flag.String("config", "config.json", "Chemin du fichier de configuration")
	statePath := flag.String("state", "state.json", "Chemin du fichier de state")
	credentialsPath := flag.String("credentials", "credentials.json", "Chemin du fichier des identifiants")
	flag.BoolVar(&DebugMode, "debug", false, "Mode debug")
	flag.Parse()

	config, err := loadConfigFromFile(*configPath)
	if err != nil {
		return fmt.Errorf("erreur lors du chargement de %s : %v", *configPath, err)
	}

	state, err := loadMapFromFile(*statePath)
	if err != nil {
		return fmt.Errorf("erreur lors du chargement de %s : %v", *statePath, err)
	}

	// Charger les credentials depuis le fichier spécifié, optionnel, peut être absent
	credentials, _ := loadCredentialsFromFile(*credentialsPath)

	if DebugMode {
		Debug()
		logger.Debugf("Configuration: %v", config)
		logger.Debugf("State: %v", state)
		logger.Debugf("Credentials: %v", credentials)
	}

	processFunc(*config, state, credentials)

	return nil
}

// #region Upsert
func Upsert(data map[string]interface{}, state map[string]string) error {

	// Sérialiser le paramètre data en JSON
	payload, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("Erreur serialization JSON: %v", err)
		return fmt.Errorf("erreur serialization JSON: %w", err)
	}

	var b64 string
	if DebugMode {
		// Encoder le JSON en base64
		b64 = string(payload)

	} else {
		// Encoder le JSON en base64
		b64 = base64.StdEncoding.EncodeToString(payload)
	}

	// Construire le message à envoyer
	msg := UpsertMsg{
		Type:    MsgTypeProcessed,
		Message: b64,
	}
	if val, ok := data["requestId"]; ok {
		msg.RequestId = val.(string)
	} else {
		return fmt.Errorf("requestId manquant dans les données")
	}

	if val, ok := data["adAccount"]; ok {
		msg.AdAccount = val.(string)
	}

	if val, ok := data["accountId"]; ok {
		msg.AdAccount = val.(string)
	}

	if val, ok := state["date"]; ok {

		if val == "" {
			val = "dimension"
		}

		if val != "dimension" {
			//Verifier que la date est au format attendu
			if _, err := time.Parse("2006-01-02", val); err != nil {
				return fmt.Errorf("date invalide dans l'état: %s", val)
			}
		}

		msg.Date = val
	}

	if DebugMode {
		logger.Infof("Processed row (DEBUG MODE) %s", msg)
	} else {
		out, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("erreur serialization message upsert: %w", err)
		}
		fmt.Println(string(out))
	}

	return nil
}

// #region Logging
func Log(level, msg string, fields map[string]interface{}) {
	if DebugMode {
		switch level {
		case "error":
			logger.WithFields(fields).Error(msg)
		case "warn":
			logger.WithFields(fields).Warn(msg)
		case "info":
			logger.WithFields(fields).Info(msg)
		case "debug":
			logger.WithFields(fields).Debug(msg)
		default:
			logger.WithFields(fields).Print(msg)
		}
	} else {
		// Imprime un JSON structuré pour que le parent relogue
		entry := LogMsg{
			Type:      MsgTypeLog,
			Level:     level,
			Msg:       msg,
			Fields:    fields,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		out, err := json.Marshal(entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Erreur serialization log: %v\n", err)
			return
		}
		fmt.Println(string(out))
	}
}

func Info(msg string) {
	Log("info", msg, nil)
}
func Warn(msg string) {
	Log("warn", msg, nil)
}
func Error(err QError) {
	Log("error", err.Error(), map[string]interface{}{
		"code":    err.Code,
		"message": err.Message,
		"err":     err.Err,
	})
}
func Fatal(err QError) {
	Log("fatal", err.Error(), map[string]interface{}{
		"code":    err.Code,
		"message": err.Message,
		"err":     err.Err,
	})
}
func DebugLog(msg string) {
	Log("debug", msg, nil)
}

func Infof(format string, args ...interface{}) {
	Info(fmt.Sprintf(format, args...))
}
func Warnf(format string, args ...interface{}) {
	Warn(fmt.Sprintf(format, args...))
}
func Errorf(format string, args ...interface{}) {
	Log("error", fmt.Sprintf(format, args...), nil)
}
func Debugf(format string, args ...interface{}) {
	DebugLog(fmt.Sprintf(format, args...))
}

// #region UpdateConfigFile
func UpdateCredentials(credentials map[string]interface{}) error {
	if DebugMode {

		// Sérialiser en JSON
		data, e := json.MarshalIndent(credentials, "", "  ")
		if e != nil {
			return fmt.Errorf("erreur de sérialisation JSON: %w", e)
		}

		// Écrire dans un fichier local credentials.json
		e = os.WriteFile("credentials.json", data, 0644)
		if e != nil {
			return fmt.Errorf("erreur d'écriture du fichier: %w", e)
		}
	} else {
		entry := CredentialsMsg{
			Type:        MsgTypeCredentials,
			Credentials: credentials,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}
		out, err := json.Marshal(entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Erreur serialization credentials: %v\n", err)
			return nil
		}
		fmt.Println(string(out))
	}

	return nil
}

// #region Checkpoint
func Checkpoint(state map[string]string, err *QError) {

	if DebugMode {

		if err == nil {

			logger.WithFields(logrus.Fields{
				"state": state,
			}).Info("Checkpoint OK")

		} else {
			logger.WithFields(logrus.Fields{
				"state": state,
				"code":  err.Code,
				"err":   err.Err,
			}).Error(err.Message)
		}

	} else {

		entry := CheckpointMsg{
			Type:      MsgTypeCheckpoint,
			State:     state,
			Error:     err,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		out, err := json.Marshal(entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Erreur serialization checkpointOk: %v\n", err)
			return
		}
		fmt.Println(string(out))

	}
}

func resolvePath(filename string) string {
	if DebugMode || strings.HasPrefix(filename, "/") || strings.Contains(filename, string(os.PathSeparator)) {
		return filename
	}
	base := os.Getenv("DATA_PATH")
	if base == "" {
		return filename
	} // fallback propre
	return filepath.Join(base, filename)
}

// #region loadConfigFromFile
func loadConfigFromFile(filename string) (*ConfigFile, error) {

	path := resolvePath(filename)

	if DebugMode {
		path = filename
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var m ConfigFile
	if err := json.Unmarshal(bytes, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// #region loadConfigFromFile
func loadCredentialsFromFile(filename string) (map[string]interface{}, error) {
	path := resolvePath(filename)
	if DebugMode {
		path = filename
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(bytes, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// #region loadMapFromFile
func loadMapFromFile(filename string) (map[string]string, error) {
	path := resolvePath(filename)
	if DebugMode {
		path = filename
	}

	file, err := os.Open(path)

	if err != nil {
		if os.IsNotExist(err) {
			// Retourner une map vide si le fichier n'existe pas
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	var m map[string]string
	if err := json.Unmarshal(bytes, &m); err != nil {
		return nil, err
	}

	return m, nil
}

// #region GetDateRange
func GetDateRange(config ConfigFile) ([]time.Time, error) {

	var startDate, endDate time.Time
	var err error

	if startDate, err = time.Parse("2006-01-02", config.RequestParams.StartDate); err != nil {
		return nil, fmt.Errorf("date de début invalide: %s", config.RequestParams.StartDate)
	}
	if endDate, err = time.Parse("2006-01-02", config.RequestParams.EndDate); err != nil {
		return nil, fmt.Errorf("date de fin invalide: %s", config.RequestParams.EndDate)
	}

	// Vérifier que la date de début est antérieure à la date de fin
	if startDate.After(endDate) {
		return nil, fmt.Errorf("la date de début doit être antérieure à la date de fin")
	}

	// Calculer les différences entre les dates
	var differences []time.Time
	currentDate := startDate
	for !currentDate.After(endDate) {
		differences = append(differences, currentDate)
		currentDate = currentDate.AddDate(0, 0, 1) // Ajouter un jour
	}

	Infof("Date range: %d dates from %s to %s", len(differences), startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))

	return differences, nil

}

// #region GetRequests

// #region GetRequests
func GetRequests(config ConfigFile) ([]Request, error) {
	// 1) Marshal le contenu de ConnectorConf en JSON brut (tolérant aux types)
	b, err := json.Marshal(config.ConnectorConf)
	if err != nil {
		return nil, fmt.Errorf("marshal ConnectorConf: %w", err)
	}

	// 2) Parse dans un map pour accéder à "requests" et/ou "request"
	var confMap map[string]interface{}
	if err := json.Unmarshal(b, &confMap); err != nil {
		return nil, fmt.Errorf("unmarshal confMap: %w", err)
	}

	var result []Request

	// parse un item qui peut être:
	//  - wrapper: {"connectorsaccountrequest": {...}, "request": {...?}}
	//  - legacy direct: {...} (considéré comme "connectorsaccountrequest" directement)
	processOne := func(item interface{}) error {
		reqMap, ok := item.(map[string]interface{})
		if !ok || reqMap == nil {
			// Format inattendu → ignore
			return nil
		}

		// --- ConnectorsAccountRequest (wrappé ou non)
		caraw, hasCAR := reqMap["connectorsaccountrequest"]
		if !hasCAR {
			// rétro-compat: l'item est directement le payload du ConnectorsAccountRequest
			caraw = reqMap
		}

		carBytes, err := json.Marshal(caraw)
		if err != nil {
			return fmt.Errorf("marshal connectorsaccountrequest: %w", err)
		}

		var caReq ConnectorsAccountRequest
		if err := json.Unmarshal(carBytes, &caReq); err != nil {
			return fmt.Errorf("unmarshal connectorsaccountrequest: %w", err)
		}

		// --- Filtrage statut
		if !(caReq.Status > REQUEST_STATUS_DISABLED && caReq.Status < REQUEST_STATUS_ERROR) {
			// Exclut la requête si en dehors de la plage souhaitée
			return nil
		}

		// --- Payload "request" brut optionnel dans le wrapper
		var subRequest interface{}
		if raw, ok := reqMap["request"]; ok {
			// on laisse tel quel (map, slice, etc.)
			subRequest = raw
		}

		result = append(result, Request{
			ConnectorsAccountRequest: caReq,
			Request:                  subRequest,
		})
		return nil
	}

	// 3) Cas "requests": tableau de wrappers (ou d'items legacy)
	if reqsRaw, ok := confMap["requests"]; ok && reqsRaw != nil {
		if reqsList, ok := reqsRaw.([]interface{}); ok {
			for _, it := range reqsList {
				if err := processOne(it); err != nil {
					return nil, err
				}
			}
		} else {
			return nil, fmt.Errorf("requests is not a list")
		}
	}

	// 4) Cas "request": wrapper singulier (nouvelle structure)
	if singleRaw, ok := confMap["request"]; ok && singleRaw != nil {
		if err := processOne(singleRaw); err != nil {
			return nil, err
		}
	}

	Infof("Nombre de requêtes récupérées: %d", len(result))

	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// #region GetAdAccounts
func GetAdAccounts(config ConfigFile) ([]AdAccount, error) {
	data, err := json.Marshal(config.ConnectorConf)
	if err != nil {
		return nil, fmt.Errorf("marshal ConnectorConf: %w", err)
	}
	var conf connectorConfForDecode
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("unmarshal ConnectorConf: %w", err)
	}
	return conf.AdAccounts, nil
}

// #region normalizeAdAccountID
func normalizeAdAccountID(a AdAccount) string {
	if a.AccountID != "" {
		return a.AccountID
	}
	if a.ID != "" {
		return a.ID
	}
	return ""
}

// #region extractAdAccountFromLooseMap
func extractAdAccountFromLooseMap(m map[string]interface{}) (string, bool) {
	candidates := []string{
		"adaccount", "adAccount", "ad_account",
		"adaccount_id", "adAccountId", "ad_account_id",
		"account_id", "accountId",
		// !! NE PAS inclure "id" ici !!
	}
	for _, k := range candidates {
		if v, ok := m[k]; ok && v != nil {
			if s, ok := v.(string); ok && s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func extractExplicitAdAccountID(req Request) (string, bool) {
	// 1) Inspecte le sous-objet Request libre
	if req.Request != nil {
		if mm, ok := req.Request.(map[string]interface{}); ok {
			if id, ok := extractAdAccountFromLooseMap(mm); ok {
				return id, true
			}
		}
	}
	// 2) Inspecte ConnectorsAccountRequest en mode map (via JSON) pour y lire une clé adaccount si elle existe
	if !isZeroConnectorsAccountRequest(req.ConnectorsAccountRequest) {
		b, _ := json.Marshal(req.ConnectorsAccountRequest)
		var mm map[string]interface{}
		if err := json.Unmarshal(b, &mm); err == nil {
			if id, ok := extractAdAccountFromLooseMap(mm); ok {
				return id, true
			}
		}
	}
	return "", false
}

// #region GetRequestsByDateAndAdAccounts
func GetRequestsByDateAndAdAccounts(config ConfigFile, state map[string]string) ([]RequestByDateAndAdAccount, error) {
	// a) date × requête (ta fonction existante)
	requestsByDate, err := GetRequestsByDate(config, state)
	if err != nil {
		return nil, err
	}

	// b) adaccounts
	adAccounts, err := GetAdAccounts(config)
	if err != nil {
		return nil, err
	}
	type acctCarry struct {
		ID  string
		Obj AdAccount
	}
	var carry []acctCarry
	for _, a := range adAccounts {
		if id := normalizeAdAccountID(a); id != "" {
			carry = append(carry, acctCarry{ID: id, Obj: a})
		}
	}

	out := make([]RequestByDateAndAdAccount, 0, len(requestsByDate))

	for _, rbd := range requestsByDate {
		if explicitID, ok := extractExplicitAdAccountID(rbd.Request); ok && explicitID != "" {
			// Essaie d’associer l’objet pour avoir name etc.
			var ptr *AdAccount
			for _, c := range carry {
				if c.ID == explicitID {
					acCopy := c.Obj
					ptr = &acCopy
					break
				}
			}
			out = append(out, RequestByDateAndAdAccount{
				Date:        rbd.Date,
				Request:     rbd.Request,
				AdAccountID: explicitID,
				AdAccount:   ptr, // peut rester nil si non trouvé en config
			})
			continue
		}

		if len(carry) > 0 {
			for _, c := range carry {
				acCopy := c.Obj
				out = append(out, RequestByDateAndAdAccount{
					Date:        rbd.Date,
					Request:     rbd.Request,
					AdAccountID: c.ID,
					AdAccount:   &acCopy,
				})
			}
		} else {
			// fallback rétro-compatible
			out = append(out, RequestByDateAndAdAccount{
				Date:        rbd.Date,
				Request:     rbd.Request,
				AdAccountID: "",
				AdAccount:   nil,
			})
		}
	}

	return out, nil
}

func isZeroConnectorsAccountRequest(ca ConnectorsAccountRequest) bool {
	b, _ := json.Marshal(ca)
	return string(b) == "{}"
}

// #region GetRequestsByDate
type RequestByDate struct {
	Date    *time.Time
	Request Request
}

func GetRequestsByDate(config ConfigFile, state map[string]string) ([]RequestByDate, error) {
	var out []RequestByDate

	requests, err := GetRequests(config)
	if err != nil {
		return nil, fmt.Errorf("get requests: %w", err)
	}
	dates, err := GetDateRange(config)
	if err != nil {
		return nil, fmt.Errorf("get date range: %w", err)
	}

	stateDate, hasDate := state["date"]
	stateRequestID, hasReq := state["requestId"]
	if stateDate == "" {
		hasDate = false
	}
	if stateRequestID == "" {
		hasReq = false
	}

	emit := func(req Request, t *time.Time) {
		out = append(out, RequestByDate{Date: t, Request: req})
	}

	// --- Cas 1: aucun filtre → respecter l'ordre des requêtes
	if !hasDate && !hasReq {
		for _, req := range requests {
			if req.ConnectorsAccountRequest.IsDimension {
				emit(req, nil)
				continue
			}
			for _, dt := range dates {
				d := dt // copie pour adresse sûre
				emit(req, &d)
			}
		}
		return out, nil
	}

	// --- Cas 2: filtre par date (et éventuellement requestId) → ordre par requête
	if hasDate {
		filterDate, err := parseDate(stateDate)
		if err != nil {
			return nil, fmt.Errorf("invalid date in state[\"date\"]: %w", err)
		}

		startedReq := !hasReq // si pas de filtre reqId, on a "déjà démarré"
		for _, req := range requests {
			// Par design d’origine: on exclut les dimensions en mode "date"
			if req.ConnectorsAccountRequest.IsDimension {
				continue
			}

			// Si on doit démarrer à une request précise, attendre de la croiser
			if hasReq && !startedReq {
				if req.ConnectorsAccountRequest.ID != stateRequestID {
					continue
				}
				startedReq = true
			}

			// Pour la première requête (si on démarre pile au milieu), on ne prend que les dates >= filterDate.
			// Pour les suivantes, on prend toutes les dates (ça respecte la logique "à partir de ...").
			for _, dt := range dates {
				if !startedReq {
					// si on n'a pas encore atteint la bonne reqId (théoriquement impossible ici),
					// on continue; sécurité défensive
					continue
				}
				if len(out) == 0 {
					// première date/req émise : appliquer la contrainte date >= filterDate
					if dt.Before(filterDate) {
						continue
					}
				} else {
					// après la toute première émission, plus de contrainte de "rattrapage"
					// (les dates sont toutes bonnes à prendre)
				}
				d := dt
				emit(req, &d)
			}
		}
		return out, nil
	}

	// --- Cas 3: pas de date, mais requestId → ordre par requête
	if hasReq {
		started := false
		for _, req := range requests {
			if !started {
				if req.ConnectorsAccountRequest.ID != stateRequestID {
					continue
				}
				started = true
			}

			if req.ConnectorsAccountRequest.IsDimension {
				// D’après ta logique: n’émettre les dimensions qu’APRÈS être entré dans la fenêtre
				emit(req, nil)
				continue
			}

			for _, dt := range dates {
				d := dt
				emit(req, &d)
			}
		}
		return out, nil
	}

	return out, nil
}

func parseDate(dateStr string) (time.Time, error) {
	return time.Parse("2006-01-02", dateStr)
}

func DumpToFile(path string, data any) error {
	if DebugMode {
		// Ajouter l'extension .json si elle n'est pas présente
		if !strings.HasSuffix(strings.ToLower(path), ".json") {
			path = path + ".json"
		}

		file, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("error creating file: %w", err)
		}
		defer file.Close()
		enc := json.NewEncoder(file)
		enc.SetIndent("", "  ")
		if err := enc.Encode(data); err != nil {
			return fmt.Errorf("error writing JSON to file: %w", err)
		}
	}
	return nil
}

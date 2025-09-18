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

	credentials, err := loadCredentialsFromFile(*credentialsPath)
	if err != nil {
		return fmt.Errorf("erreur lors du chargement de %s : %v", *credentialsPath, err)
	}

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
func GetRequests(config ConfigFile) ([]Request, error) {
	// Marshal le contenu de ConnectorConf en JSON brut
	b, err := json.Marshal(config.ConnectorConf)
	if err != nil {
		return nil, fmt.Errorf("marshal ConnectorConf: %w", err)
	}

	// On parse dans un map pour accéder à "requests"
	var confMap map[string]interface{}
	if err := json.Unmarshal(b, &confMap); err != nil {
		return nil, fmt.Errorf("unmarshal confMap: %w", err)
	}

	reqsRaw, ok := confMap["requests"]
	if !ok {
		return nil, nil // pas de requests
	}

	reqsList, ok := reqsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("requests is not a list")
	}

	var result []Request
	for _, req := range reqsList {
		reqMap, ok := req.(map[string]interface{})
		if !ok {
			continue
		}

		// On parse "connectorsaccountrequest" typé
		caraw, hasCAR := reqMap["connectorsaccountrequest"]
		if !hasCAR {
			continue // ignore si pas de connectorsaccountrequest
		}

		carBytes, err := json.Marshal(caraw)
		if err != nil {
			return nil, fmt.Errorf("marshal connectorsaccountrequest: %w", err)
		}
		var caReq ConnectorsAccountRequest
		if err := json.Unmarshal(carBytes, &caReq); err != nil {
			return nil, fmt.Errorf("unmarshal connectorsaccountrequest: %w", err)
		}

		if !(caReq.Status > REQUEST_STATUS_DISABLED &&
			caReq.Status < REQUEST_STATUS_ERROR) {
			continue // on exclut cette requête
		}

		// Récupérer "request" si présent
		var subRequest interface{}
		if reqField, ok := reqMap["request"]; ok {
			subRequest = reqField
		}

		result = append(result, Request{
			ConnectorsAccountRequest: caReq,
			Request:                  subRequest,
		})
	}

	Infof("Nombre de requêtes récupérées: %d", len(result))

	return result, nil
}

// #region GetRequestsByDate
type RequestByDate struct {
	Date    *time.Time
	Request Request
}

func GetRequestsByDate(config ConfigFile, state map[string]string) ([]RequestByDate, error) {
	var requestByDate []RequestByDate

	requests, err := GetRequests(config)
	if err != nil {
		return nil, fmt.Errorf("get requests: %w", err)
	}

	dates, err := GetDateRange(config)
	if err != nil {
		return nil, fmt.Errorf("get date range: %w", err)
	}

	stateDate, hasDate := state["date"]
	stateRequestId, hasRequestId := state["requestId"]

	if stateDate == "" {
		hasDate = false
	}

	if stateRequestId == "" {
		hasRequestId = false
	}

	// Cas 1: Aucun filtre, on prend tout
	if !hasDate && !hasRequestId {
		// On ajoute d'abord les dimensions sans date
		for _, request := range requests {
			if request.ConnectorsAccountRequest.IsDimension {
				requestByDate = append(requestByDate, RequestByDate{
					Date:    nil,
					Request: request,
				})
			}
		}
		// Puis les autres requêtes pour toutes les dates
		for _, date := range dates {
			for _, request := range requests {
				if request.ConnectorsAccountRequest.IsDimension {
					continue
				}
				requestByDate = append(requestByDate, RequestByDate{
					Date:    &date,
					Request: request,
				})
			}
		}
		return requestByDate, nil
	}

	// Cas 2: date présente
	if hasDate && stateDate != "" {
		filterDate, err := parseDate(stateDate)
		if err != nil {
			return nil, fmt.Errorf("invalid date in state[\"date\"]: %w", err)
		}
		foundDate := false
		foundReq := false

		for _, date := range dates {
			if !foundDate {
				// On saute tant qu'on n'a pas atteint la date cible
				if date.Before(filterDate) {
					continue
				}
				if date.Equal(filterDate) {
					foundDate = true
				} else {
					continue
				}
			}

			for _, request := range requests {
				if request.ConnectorsAccountRequest.IsDimension {
					continue // exclure dimensions
				}

				if foundDate && hasRequestId && stateRequestId != "" {
					// Pour la première date, ne commencer qu'à la bonne requestId
					if !foundReq && date.Equal(filterDate) {
						if request.ConnectorsAccountRequest.ID != stateRequestId {
							continue
						}
						foundReq = true
					}
				}
				// À partir d'ici, foundDate = true, foundReq = true (ou pas de filtre sur requestId)
				requestByDate = append(requestByDate, RequestByDate{
					Date:    &date,
					Request: request,
				})
			}
			// Après la date cible, plus de filtre sur requestId
			foundReq = true
		}
		return requestByDate, nil
	}

	// Cas 3: Pas de date, mais requestId présent (on prend tout à partir de cette requestId, dimensions exclues AVANT)
	if hasRequestId && stateRequestId != "" {
		found := false
		// On ajoute les dimensions jusqu'à la bonne requestId (exclues avant)
		for _, request := range requests {
			if request.ConnectorsAccountRequest.IsDimension {
				if found {
					requestByDate = append(requestByDate, RequestByDate{
						Date:    nil,
						Request: request,
					})
				}
				continue
			}
			// Pour chaque date, on applique le filtre
			for _, date := range dates {
				if !found {
					if request.ConnectorsAccountRequest.ID != stateRequestId {
						continue
					}
					found = true
				}
				requestByDate = append(requestByDate, RequestByDate{
					Date:    &date,
					Request: request,
				})
			}
		}
		return requestByDate, nil
	}

	return requestByDate, nil
}

func parseDate(dateStr string) (time.Time, error) {
	return time.Parse("2006-01-02", dateStr)
}

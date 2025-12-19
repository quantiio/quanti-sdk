package sdk

import "time"

type MsgType string

type UpsertMsg struct {
	Type      MsgType `json:"type"`
	ID        string  `json:"id"`
	AdAccount string  `json:"ad_account"`
	RequestId string  `json:"request_id"`
	ParentId  string  `json:"parent_id"`
	ChildId   string  `json:"child_id"`
	Message   string  `json:"message"`
	Date      string  `json:"date"`
}

type LogMsg struct {
	Type      string                 `json:"type"`
	Level     string                 `json:"level"`
	Msg       string                 `json:"msg"`
	Fields    map[string]interface{} `json:"fields"`
	Timestamp string                 `json:"timestamp"`
}

type CheckpointMsg struct {
	Type      MsgType           `json:"type"`
	State     map[string]string `json:"state"`
	Error     *QError           `json:"error"`
	Timestamp string            `json:"timestamp"`
}

type PlantMsg struct {
	Type MsgType `json:"type"`
	Plan []Plan  `json:"msg"`
}

type Plan struct {
	RequestId      string `json:"requestId"`
	Date           string `json:"date"`
	AccountId      string `json:"accountId"`
	AccountChildId string `json:"accountChildId,omitempty"` // ID enfant si différent de AccountId (ex: propertyId GA4)
}

type CredentialsMsg struct {
	Type        MsgType                `json:"type"`
	Credentials map[string]interface{} `json:"credentials"`
	Timestamp   string                 `json:"timestamp"`
}

type RequestParams struct {
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	ProcessType string  `json:"process_type"`
	Params      *string `json:"params,omitempty"`
}

type ConfigFile struct {
	PersonnalCredentials map[string]interface{} `json:"personnalCredentials"`
	ConnectorCredentials map[string]interface{} `json:"connectorCredentials"`
	ConnectorConf        interface{}            `json:"connectorConf"`
	AdAccounts           []AdAccount            `json:"adAccounts"`
	RequestParams        RequestParams          `json:"requestParams"`
	ProcessId            string                 `json:"processId"`
}

// #region Requests

type Request_status int

const (
	REQUEST_STATUS_DISABLED Request_status = 100
	REQUEST_STATUS_ENABLED  Request_status = 200
	REQUEST_STATUS_ERROR    Request_status = 300
)

type DatabaseMetaData struct {
	Description  string `json:"description"`
	Format       string `json:"format"`
	IsMetric     bool   `json:"isMetric"`
	IsQuantiDate bool   `json:"isQuantiDate"`
	Managed      bool   `json:"managed"`
	Name         string `json:"name"`
	QuantiField  bool   `json:"quantiField"`
	QuantiId     bool   `json:"quantiId"`
	Type         string `json:"type"`

	// Enrichissements IA (optionnels, rétro-compatibles)
	Purpose      string `json:"purpose,omitempty"`       // À quoi ça sert
	BusinessName string `json:"business_name,omitempty"` // Nom métier (ex: "Dépenses")
	SemanticType string `json:"semantic_type,omitempty"` // id, dimension, metric, date, currency_micro
	FormatHint   string `json:"format_hint,omitempty"`   // divide_1000000, percentage
	IsPII        bool   `json:"is_pii,omitempty"`        // Donnée personnelle identifiable
}

type OrderedField struct {
	DatabaseMetaData DatabaseMetaData `json:"databaseMetaData"`
	FieldPath        string           `json:"fieldPath"`
	FieldSrc         string           `json:"fieldSrc"`
}

type Schema struct {
	OrderedFields []OrderedField `json:"orderedFields"`
	TableName     string         `json:"tableName"`
}

type ConnectorsAccountRequest struct {
	Description string         `json:"description"`
	ID          string         `json:"id"`
	IsDimension bool           `json:"isDimension"`
	IsPrebuild  bool           `json:"isPrebuild"`
	Name        string         `json:"name"`
	Schema      Schema         `json:"schema"`
	Status      Request_status `json:"status"`

	// Enrichissements IA (optionnels, rétro-compatibles)
	Purpose         string   `json:"purpose,omitempty"`          // À quoi ça sert
	BusinessDomain  string   `json:"business_domain,omitempty"`  // performance, attribution, acquisition
	Grain           string   `json:"grain,omitempty"`            // daily, event, snapshot
	SampleQuestions []string `json:"sample_questions,omitempty"` // Questions types pour le RAG
}

type Request struct {
	ConnectorsAccountRequest ConnectorsAccountRequest `json:"connectorsaccountrequest"`
	Request                  interface{}              `json:"request,omitempty"`
}

// #region Enriched Prebuilds (pour Quanti AI)

// ConnectorInfo contient les métadonnées du connecteur pour l'enrichissement IA
type ConnectorInfo struct {
	SKU         string `json:"sku"`                   // Identifiant technique (google_ads, meta_ads)
	Name        string `json:"name"`                  // Nom affiché (Google Ads)
	Category    string `json:"category"`              // marketing, analytics, business, custom
	Description string `json:"description,omitempty"` // Qu'est-ce que c'est
	Purpose     string `json:"purpose,omitempty"`     // À quoi ça sert
}

// EnrichedPrebuildsFile représente le fichier prebuilds.json enrichi avec wrapper connecteur
// Structure : { "connector": {...}, "prebuilds": [...] }
// Rétro-compatible : si "connector" est absent, c'est un ancien fichier (array direct)
type EnrichedPrebuildsFile struct {
	Connector *ConnectorInfo `json:"connector,omitempty"`
	Prebuilds []Request      `json:"prebuilds,omitempty"`
}

// #endregion

type connectorConfForDecode struct {
	AdAccounts []AdAccount   `json:"adaccounts"`
	Requests   []interface{} `json:"requests"`
	Request    interface{}   `json:"request"`
}

type AdAccount struct {
	AccountID string `json:"account_id"`
	ID        string `json:"id"`
	Name      string `json:"name"`
}

type RequestByDateAndAdAccount struct {
	Date             *time.Time `json:"date,omitempty"`
	Request          Request    `json:"request"`
	AdAccountID      string     `json:"adAccountId,omitempty"`
	AdAccountChildID string     `json:"adAccountChildId,omitempty"` // ID enfant (ex: propertyId GA4) si différent de AdAccountID
	AdAccount        *AdAccount `json:"adAccount,omitempty"`
}

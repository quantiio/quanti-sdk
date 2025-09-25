package sdk

type MsgType string

type UpsertMsg struct {
	Type         MsgType `json:"type"`
	ID           string  `json:"id"`
	AdAccount    string  `json:"ad_account"`
	RequestId    string  `json:"request_id"`
	InsertedRows *int    `json:"inserted_rows"`
	Message      string  `json:"message"`
	Date         string  `json:"date"`
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

type CredentialsMsg struct {
	Type        MsgType                `json:"type"`
	Credentials map[string]interface{} `json:"credentials"`
	Timestamp   string                 `json:"timestamp"`
}

type RequestParams struct {
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	ProcessType string `json:"process_type"`
}

type ConfigFile struct {
	PersonnalCredentials map[string]interface{} `json:"personnalCredentials"`
	ConnectorCredentials map[string]interface{} `json:"connectorCredentials"`
	ConnectorConf        interface{}            `json:"connectorConf"`
	RequestParams        RequestParams          `json:"requestParams"`
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
}

type Request struct {
	ConnectorsAccountRequest ConnectorsAccountRequest `json:"connectorsaccountrequest"`
	Request                  interface{}              `json:"request,omitempty"`
}

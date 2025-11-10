package sdk

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type QErrorCode int

type QError struct {
	Code    QErrorCode `json:"code"`
	Message string     `json:"message,omitempty"`
	Details string     `json:"details,omitempty"`
	Err     string     `json:"error"`
}

const (
	ERR_DEF_AUTH_NOT_VALID               QErrorCode = 1000
	ERR_DEF_INVALID_REQUEST              QErrorCode = 1010
	ERR_DEF_INVALID_DATA                 QErrorCode = 1020
	ERR_DEF_NOT_FOUND                    QErrorCode = 1040
	ERR_DEF_PERMISSION_DENIED            QErrorCode = 1050
	ERR_DEF_INVALID_UPSERT               QErrorCode = 1060
	ERR_DEF_INVALID_DATE                 QErrorCode = 1070
	ERR_DEF_INVALID_REQUESTS             QErrorCode = 1080
	ERR_DEF_API_UNAVAILABLE              QErrorCode = 1090
	ERR_DEF_UNABLED_START_PROCESS        QErrorCode = 1100
	ERR_DEF_CANT_INSERT_IN_DATAWAREHOUSE QErrorCode = 1200
	ERR_DEF_PROCESSED_WITH_ERROR         QErrorCode = 1210
	//Tmp error codes
	ERR_TMP_RATE_LIMIT_EXCEEDED QErrorCode = 2000
	ERR_TMP_TIMEOUT             QErrorCode = 2010
	ERR_TMP_SERVICE_UNAVAILABLE QErrorCode = 2020
)

var errorCodeLabels = map[QErrorCode]string{
	ERR_DEF_AUTH_NOT_VALID:               "DEF",
	ERR_DEF_INVALID_REQUEST:              "DEF",
	ERR_DEF_INVALID_DATA:                 "DEF",
	ERR_DEF_NOT_FOUND:                    "DEF",
	ERR_DEF_PERMISSION_DENIED:            "DEF",
	ERR_DEF_INVALID_UPSERT:               "DEF",
	ERR_DEF_INVALID_DATE:                 "DEF",
	ERR_DEF_INVALID_REQUESTS:             "DEF",
	ERR_TMP_RATE_LIMIT_EXCEEDED:          "TMP",
	ERR_TMP_TIMEOUT:                      "TMP",
	ERR_TMP_SERVICE_UNAVAILABLE:          "TMP",
	ERR_DEF_API_UNAVAILABLE:              "DEF",
	ERR_DEF_UNABLED_START_PROCESS:        "DEF",
	ERR_DEF_CANT_INSERT_IN_DATAWAREHOUSE: "DEF",
	ERR_DEF_PROCESSED_WITH_ERROR:         "DEF",
}

var ErrorCodes = map[QErrorCode]string{
	ERR_DEF_AUTH_NOT_VALID:               "Auth not valid",
	ERR_DEF_INVALID_REQUEST:              "Invalid Request",
	ERR_DEF_INVALID_DATA:                 "Invalid Data",
	ERR_DEF_NOT_FOUND:                    "Not Found",
	ERR_DEF_PERMISSION_DENIED:            "Permission Denied",
	ERR_TMP_RATE_LIMIT_EXCEEDED:          "Rate Limit Exceeded",
	ERR_TMP_TIMEOUT:                      "Timeout",
	ERR_TMP_SERVICE_UNAVAILABLE:          "Service Unavailable",
	ERR_DEF_INVALID_UPSERT:               "Invalid Upsert",
	ERR_DEF_INVALID_DATE:                 "Invalid Date",
	ERR_DEF_INVALID_REQUESTS:             "Invalid Requests",
	ERR_DEF_API_UNAVAILABLE:              "API Unavailable",
	ERR_DEF_UNABLED_START_PROCESS:        "Process start is disabled",
	ERR_DEF_CANT_INSERT_IN_DATAWAREHOUSE: "Can't insert in Datawarehouse",
	ERR_DEF_PROCESSED_WITH_ERROR:         "Processed with error",
}

func ParseQErrorCode(val interface{}) (QErrorCode, bool) {
	switch v := val.(type) {
	case float64:
		return QErrorCode(int(v)), true
	case int:
		return QErrorCode(v), true
	case int64:
		return QErrorCode(v), true
	case string:
		i, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return QErrorCode(i), true
	default:
		return 0, false
	}
}

func GetErrorCodeType(code QErrorCode) string {
	if label, ok := errorCodeLabels[code]; ok {
		return label
	}
	return ""
}

// Implémentation de la méthode Error() pour satisfaire l'interface error
func (e *QError) Error() string {
	if e == nil {
		return "nil QError"
	}
	if e.Err != "" {

		return fmt.Sprintf("code: %d, message: %s, cause: %v", e.Code, e.ErrorMessage(), e.Err)
	}

	return fmt.Sprintf("code: %d, message: %s", e.Code, e.ErrorMessage())
}

func (e *QError) Unwrap() error {
	if e == nil {
		return nil
	}
	return fmt.Errorf("%s", e.Err)
}

// Méthode pour obtenir le code d'erreur
func (e *QError) ErrorCode() QErrorCode {
	if e == nil {
		return 0
	}
	return e.Code
}

// Méthode pour obtenir le message d'erreur
func (e *QError) ErrorMessage() string {

	if e == nil {
		return "nil QError"
	}
	if e.Code == 0 {
		return ""
	}

	message, ok := ErrorCodes[QErrorCode(e.Code)]
	if ok {
		return message
	}

	return "unknown error"
}

func (e *QError) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte(`null`), nil
	}

	type Alias QError // évite l'appel récursif

	return json.Marshal(&struct {
		*Alias
		Message string `json:"message"`
		Details string `json:"details,omitempty"`
	}{
		Alias:   (*Alias)(e),
		Message: e.ErrorMessage(), // => libellé du code
		Details: e.Message,
	})
}

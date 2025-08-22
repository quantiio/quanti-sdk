# Quanti Sdk

## How to use

```go

const BASE_URL = "https://myapi.com?date="

func main() {
	quanti.Process(process)
}


//Config contient credentials, conf du connecteur, ..., et state contient date, requestId et potentiellement offset (ou autre element choisie) de la dernière boucle qui n'a pu aboutir. Le service sera redémarré à partir de de ce state pour continuer le traitement à partir de ce point
func process(config quanti.ConfigFile, state map[string]string) {


    //Le tableau requests contient toutes les combinaisons Date + Request (date = nil dans le cas des dimenssions, et son placées en premier)
    //Si un state est présent, cela signifie qu'il s'agit d'une reprise après une erreur
	requests, err := quantiSdk.GetRequestsByDate(config, state)

	if err != nil {
		quantiSdk.Checkpoint(state, &quantiSdk.QError{
			Code: quantiSdk.ERR_DEF_INVALID_DATE,
			Err:  fmt.Errorf("can’t create date range: %v", err),
		})

		return
	}

    //Ici récupération des credentials (apiKey, OAuth, etc. )
    apiKeyConf := config.PersonnalCredentials["apiKey"]

    //Attention sur l'exemple il n'y a pas d'adAccount, mais il y en avait un il faudrait boucler sur les adAccounts
	for _, request := range requests {

		state["date"] = request.Date.Format("2006-01-02")

		state["requestId"] = request.Request.ConnectorsAccountRequest.ID

        //for _, adAccount := range config.conf.AdAccounts {
        //  state["adAccount"] = votre adAccount
        //  ...

		var apiKey string

		if s, ok := apiKeyConf.(string); ok {
			apiKey = s
		} else {
			quantiSdk.Errorf("Clé API invalide dans la configuration: %v", apiKeyConf)
			return
		}

		url := BASE_URL + request.Date.Format("2006-01-02")

		var body []byte
		var carts []map[string]interface{}

		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {

            //Les codes d'erreur sont classés entre erreur définitive qui ne pourra pas être traitée
            //Ou des codes d'erreurs qui temporaire (perte credentials, over quota, etc) qui stopera le service mais le redémarera à partir de ce point
			quantiSdk.Checkpoint(state, &quantiSdk.QError{
				Code: quantiSdk.ERR_DEF_INVALID_REQUEST,
				Err:  fmt.Errorf("can’t create client http  %s: %v", request.Date.Format("2006-01-02"), err),
			})
			return
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 200 {
			var parsed struct {
				Carts []map[string]interface{} `json:"carts"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				quantiSdk.Checkpoint(state, &quantiSdk.QError{
					Code: quantiSdk.ERR_DEF_INVALID_DATA,
					Err:  fmt.Errorf("can’t create client http  %s: %v", request.Date.Format("2006-01-02"), err),
				})
				return
			}
			carts = parsed.Carts

		} else {

			quantiSdk.Checkpoint(state, &quantiSdk.QError{
				Code: quantiSdk.ERR_TMP_SERVICE_UNAVAILABLE,
				Err:  fmt.Errorf("erreur lors de l'upsert pour %s: %v", request.Date.Format("2006-01-02"), err),
			})

			return
		}

		for _, cart := range carts {
			payload := map[string]interface{}{
				"adAccount": "",
				"requestId": "request01",
				"date":      request.Date.Format("2006-01-02"),
				"data":      cart,
			}
            //Chaque ligne est envoyées de manière BRUTE en précisant, l'adAccount, requestId, date, et data
			err := quantiSdk.Upsert(payload, state)

			if err != nil {
				quantiSdk.Checkpoint(state, &quantiSdk.QError{
					Code: quantiSdk.ERR_DEF_INVALID_UPSERT,
					Err:  fmt.Errorf("erreur lors de l'upsert pour %s: %v", request.Date.Format("2006-01-02"), err),
				})

				return
			}

		}

		quantiSdk.Checkpoint(state, nil)

	}

}

```

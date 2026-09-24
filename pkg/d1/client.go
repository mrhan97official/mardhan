// Package d1 talks to a Cloudflare D1 database over Cloudflare's HTTP API,
// so the Go handlers can run as plain Vercel serverless functions without a
// native SQLite driver or a persistent connection.
package d1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type queryRequest struct {
	SQL    string        `json:"sql"`
	Params []interface{} `json:"params,omitempty"`
}

type queryResponse struct {
	Success bool `json:"success"`
	Result  []struct {
		Results []map[string]interface{} `json:"results"`
		Success *bool `json:"success"`
		Error string `json:"error"`
	} `json:"result"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Query runs a single SQL statement against the configured D1 database and
// returns the resulting rows as generic maps.
func Query(sql string, params ...interface{}) ([]map[string]interface{}, error) {
	accountID := os.Getenv("CF_ACCOUNT_ID")
	databaseID := os.Getenv("CF_D1_DATABASE_ID")
	token := os.Getenv("CF_API_TOKEN")

	if accountID == "" || databaseID == "" || token == "" {
		return nil, fmt.Errorf("cloudflare D1 credentials are not configured (CF_ACCOUNT_ID / CF_D1_DATABASE_ID / CF_API_TOKEN)")
	}

	endpoint := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/d1/database/%s/query",
		accountID, databaseID,
	)

	payload, err := json.Marshal(queryRequest{SQL: sql, Params: params})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decoding D1 response: %w", err)
	}

	if resp.StatusCode >= 300 || !parsed.Success {
		msg := "unknown D1 error"
		if len(parsed.Errors) > 0 {
			msg = parsed.Errors[0].Message
		}
		return nil, fmt.Errorf("d1 query failed: %s", msg)
	}

	if len(parsed.Result) == 0 {
		return nil, fmt.Errorf("d1 query failed: respons tidak berisi hasil")
	}
	for _, result := range parsed.Result {
		if result.Success != nil && !*result.Success {
			if result.Error != "" { return nil, fmt.Errorf("d1 query failed: %s", result.Error) }
			return nil, fmt.Errorf("d1 query failed: satu pernyataan SQL gagal")
		}
	}
	return parsed.Result[0].Results, nil
}

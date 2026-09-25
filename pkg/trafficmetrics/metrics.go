// Package trafficmetrics records traffic served by this application's Go API.
// It does not claim to measure static assets or Vercel's CDN traffic.
package trafficmetrics

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"devcontrol/pkg/d1"
)

var modeCache struct {
	sync.Mutex
	mode string
	until time.Time
}

var cleanup struct {
	sync.Mutex
	day string
}

// CountingWriter counts response body bytes, including streamed responses.
type CountingWriter struct {
	http.ResponseWriter
	BytesWritten int64
}

func (w *CountingWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	w.BytesWritten += int64(n)
	return n, err
}

func (w *CountingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *CountingWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok { flusher.Flush() }
}

func Mode() (string, error) {
	modeCache.Lock()
	if time.Now().Before(modeCache.until) {
		mode := modeCache.mode
		modeCache.Unlock()
		return mode, nil
	}
	modeCache.Unlock()
	rows, err := d1.Query(`SELECT mode FROM traffic_monitoring WHERE id = 1 LIMIT 1`)
	if err != nil { return "", err }
	mode := ""
	if len(rows) > 0 { mode, _ = rows[0]["mode"].(string) }
	modeCache.Lock()
	modeCache.mode, modeCache.until = mode, time.Now().Add(10*time.Second)
	modeCache.Unlock()
	return mode, nil
}

func Enable(mode string) error {
	if mode != "api" && mode != "cloudflare" { return fmt.Errorf("mode monitoring tidak valid") }
	_, err := d1.Query(`INSERT INTO traffic_monitoring (id, mode, updated_at) VALUES (1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, updated_at = CURRENT_TIMESTAMP`, mode)
	if err == nil {
		modeCache.Lock()
		modeCache.mode, modeCache.until = mode, time.Now().Add(10*time.Second)
		modeCache.Unlock()
	}
	return err
}

// Record is best-effort and is called after the authenticated API handler
// returns. A telemetry failure must not alter a deployment or other action.
func Record(resource string, bytes int64) {
	if resource == "health" || resource == "zone-approval" || resource == "" { return }
	mode, err := Mode()
	if err != nil || mode != "api" { return }
	if bytes < 0 { bytes = 0 }
	hour := time.Now().UTC().Format("2006-01-02 15:00:00")
	_, err = d1.Query(`INSERT INTO api_traffic_hourly (bucket, requests, response_bytes) VALUES (?, 1, ?)
		ON CONFLICT(bucket) DO UPDATE SET requests = requests + 1,
		response_bytes = response_bytes + excluded.response_bytes`, hour, bytes)
	if err != nil { return }
	day := time.Now().UTC().Format("2006-01-02")
	cleanup.Lock()
	defer cleanup.Unlock()
	if cleanup.day != day {
		if _, err := d1.Query(`DELETE FROM api_traffic_hourly WHERE bucket < datetime('now', '-30 days')`); err == nil { cleanup.day = day }
	}
}

// Series returns the current hour and 23 preceding hourly buckets.
func Series() ([]float64, []float64, error) {
	rows, err := d1.Query(`SELECT bucket, requests, response_bytes FROM api_traffic_hourly
		WHERE bucket >= datetime('now', '-24 hours') ORDER BY bucket`)
	if err != nil { return nil, nil, err }
	bytes, requests := make([]float64, 24), make([]float64, 24)
	start := time.Now().UTC().Truncate(time.Hour).Add(-23*time.Hour)
	for _, row := range rows {
		bucket, _ := row["bucket"].(string)
		hour, parseErr := time.ParseInLocation("2006-01-02 15:04:05", bucket, time.UTC)
		if parseErr != nil { continue }
		index := int(hour.Sub(start) / time.Hour)
		if index < 0 || index >= 24 { continue }
		bytes[index], _ = row["response_bytes"].(float64)
		requests[index], _ = row["requests"].(float64)
	}
	return bytes, requests, nil
}

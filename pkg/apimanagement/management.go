// Package apimanagement owns read-only client keys and endpoint checks.
package apimanagement

import (
  "context"
  "crypto/rand"
  "crypto/sha256"
  "encoding/base64"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "io"
  "net"
  "net/http"
  "net/url"
  "sort"
  "strings"
  "time"

  "devcontrol/pkg/auth"
  "devcontrol/pkg/d1"
  "devcontrol/pkg/util"
)

func randomID(size int) (string, error) {
  value := make([]byte, size)
  if _, err := rand.Read(value); err != nil { return "", err }
  return hex.EncodeToString(value), nil
}

func audit(action, target string) {
  _, _ = d1.Query(`INSERT INTO admin_audit_log (action, target) VALUES (?, ?)`, action, target)
}

func validPath(path string) bool {
  if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || len(path) > 200 || strings.Contains(path, "..") { return false }
  for _, char := range path {
    if char != '/' && char != '-' && char != '_' && char != '.' &&
      !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') { return false }
  }
  return true
}

func asString(v interface{}) string { s, _ := v.(string); return s }
func asNumber(v interface{}) float64 { n, _ := v.(float64); return n }

// Test only connects to public HTTPS hosts. DNS resolution happens inside the
// dialer and each resolved address is checked before opening a connection.
func publicIP(ip net.IP) bool {
  if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() { return false }
  for _, raw := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "2001:db8::/32"} {
    _, network, _ := net.ParseCIDR(raw)
    if network.Contains(ip) { return false }
  }
  return true
}

func checkURL(ctx context.Context, rawURL, method string) (int, int, error) {
  parsed, err := url.Parse(rawURL)
  if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" ||
    (parsed.Port() != "" && parsed.Port() != "443") || parsed.Fragment != "" {
    return 0, 0, fmt.Errorf("hanya URL HTTPS publik yang diperbolehkan")
  }
  transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 4 * time.Second,
    DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
      hostname, port, splitErr := net.SplitHostPort(addr)
      if splitErr != nil || port != "443" { return nil, fmt.Errorf("alamat pemeriksaan tidak valid") }
      ips, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, hostname)
      if lookupErr != nil || len(ips) == 0 { return nil, fmt.Errorf("DNS endpoint gagal") }
      for _, ip := range ips { if !publicIP(ip.IP) { return nil, fmt.Errorf("alamat endpoint bukan IP publik") } }
      return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
    },
  }
  defer transport.CloseIdleConnections()
  client := &http.Client{Transport: transport, Timeout: 8 * time.Second,
    CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
  req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
  if err != nil { return 0, 0, err }
  start := time.Now()
  resp, err := client.Do(req)
  elapsed := int(time.Since(start).Milliseconds())
  if err != nil { return 0, elapsed, fmt.Errorf("endpoint tidak merespons: %w", err) }
  defer resp.Body.Close()
  _, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
  return resp.StatusCode, elapsed, nil
}

func selectedRange(r *http.Request) (string, string) {
  switch r.URL.Query().Get("range") {
  case "7d": return "7d", "-7 days"
  case "30d": return "30d", "-30 days"
  default: return "24h", "-1 day"
  }
}

type Performance struct {
  ResponseTimeMS int `json:"response_time_ms"`
  ResponseTimeChange float64 `json:"response_time_change"`
  RequestVolume int `json:"request_volume"`
  RequestVolumeChange float64 `json:"request_volume_change"`
  ErrorRate float64 `json:"error_rate"`
  ErrorRateChange float64 `json:"error_rate_change"`
  SeriesLatency []float64 `json:"series_latency"`
  SeriesVolume []float64 `json:"series_volume"`
  SeriesErrors []float64 `json:"series_errors"`
  Range string `json:"range"`
  HasComparison bool `json:"has_comparison"`
}

func summary(window string) (map[string]interface{}, error) {
  rows, err := d1.Query(`SELECT COUNT(*) AS total, COALESCE(AVG(latency_ms), 0) AS latency,
    COALESCE(SUM(CASE WHEN status_code = 0 OR status_code >= 300 THEN 1 ELSE 0 END), 0) AS errors
    FROM api_check_metrics WHERE checked_at >= datetime('now', ?)`, window)
  if err != nil || len(rows) == 0 { return nil, err }
  return rows[0], nil
}

func percent(now, previous float64) float64 {
  if previous <= 0 { return 0 }
  return float64(int((now-previous)/previous*10000)) / 100
}

func GetPerformance(r *http.Request) (Performance, error) {
  label, window := selectedRange(r)
  result := Performance{Range: label, SeriesLatency: []float64{}, SeriesVolume: []float64{}, SeriesErrors: []float64{}}
  current, err := summary(window)
  if err != nil { return result, err }
  previousRows, err := d1.Query(`SELECT COUNT(*) AS total, COALESCE(AVG(latency_ms), 0) AS latency,
    COALESCE(SUM(CASE WHEN status_code = 0 OR status_code >= 300 THEN 1 ELSE 0 END), 0) AS errors
    FROM api_check_metrics WHERE checked_at >= datetime('now', ? || ' days')
    AND checked_at < datetime('now', ?)`, map[string]string{"24h": "-2", "7d": "-14", "30d": "-60"}[label], window)
  if err != nil { return result, err }
  total, previousTotal := asNumber(current["total"]), asNumber(previousRows[0]["total"])
  result.HasComparison = previousTotal > 0
  errors, previousErrors := asNumber(current["errors"]), asNumber(previousRows[0]["errors"])
  result.ResponseTimeMS = int(asNumber(current["latency"]) + 0.5)
  result.RequestVolume = int(total)
  if total > 0 { result.ErrorRate = float64(int(errors/total*10000)) / 100 }
  prevRate := float64(0)
  if previousTotal > 0 { prevRate = previousErrors/previousTotal*100 }
  result.ResponseTimeChange = percent(asNumber(current["latency"]), asNumber(previousRows[0]["latency"]))
  result.RequestVolumeChange = percent(total, previousTotal)
  result.ErrorRateChange = float64(int((result.ErrorRate-prevRate)*100)) / 100
  bucket := "%Y-%m-%d %H"
  if label != "24h" { bucket = "%Y-%m-%d" }
  series, err := d1.Query(`SELECT strftime(?, checked_at) AS bucket, COUNT(*) AS total,
    COALESCE(AVG(latency_ms), 0) AS latency,
    COALESCE(SUM(CASE WHEN status_code = 0 OR status_code >= 300 THEN 1 ELSE 0 END), 0) AS errors
    FROM api_check_metrics WHERE checked_at >= datetime('now', ?)
    GROUP BY bucket ORDER BY bucket`, bucket, window)
  if err != nil { return result, err }
  for _, row := range series {
    result.SeriesVolume = append(result.SeriesVolume, asNumber(row["total"]))
    result.SeriesLatency = append(result.SeriesLatency, asNumber(row["latency"]))
    result.SeriesErrors = append(result.SeriesErrors, asNumber(row["errors"]))
  }
  return result, nil
}

func Handle(w http.ResponseWriter, r *http.Request) {
  if r.Method == http.MethodGet { list(w, r); return }
  if r.Method != http.MethodPost { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST")); return }
  var body struct {
    Action string `json:"action"`
    ID string `json:"id"`
    Name string `json:"name"`
    Project string `json:"project"`
    Path string `json:"path"`
    Method string `json:"method"`
    Environment string `json:"environment"`
    Enabled bool `json:"enabled"`
    Scopes []string `json:"scopes"`
  }
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
    util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan tidak valid")); return
  }
  var response interface{}
  var err error
  switch body.Action {
  case "create_api": response, err = createAPI(body.Name, body.Project, body.Path, body.Method, body.Environment)
  case "toggle_api": response, err = toggleAPI(body.ID, body.Enabled)
  case "test_api": response, err = testAPI(r.Context(), body.ID)
  case "create_key": response, err = createKey(body.Name, body.Scopes)
  case "rotate_key": response, err = rotateKey(body.ID)
  case "revoke_key": response, err = revokeKey(body.ID)
  default: err = fmt.Errorf("aksi tidak dikenal")
  }
  if err != nil { util.Error(w, http.StatusBadRequest, err); return }
  util.JSON(w, http.StatusOK, response)
}

func list(w http.ResponseWriter, r *http.Request) {
  apis, err := d1.Query(`SELECT id, name, project, path, method, environment, enabled, created_at FROM managed_apis ORDER BY created_at DESC`)
  if err != nil { util.Error(w, http.StatusBadGateway, err); return }
  keys, err := d1.Query(`SELECT id, name, key_prefix, scopes, created_at, revoked_at FROM api_keys ORDER BY created_at DESC LIMIT 100`)
  if err != nil { util.Error(w, http.StatusBadGateway, err); return }
  projects, err := d1.Query(`SELECT name, app_url FROM services WHERE app_url IS NOT NULL AND app_url <> '' ORDER BY name`)
  if err != nil { util.Error(w, http.StatusBadGateway, err); return }
  latest, err := d1.Query(`SELECT api_id, status_code, latency_ms, checked_at FROM api_check_metrics ORDER BY checked_at DESC LIMIT 100`)
  if err != nil { util.Error(w, http.StatusBadGateway, err); return }
  history, err := d1.Query(`SELECT action, target, created_at FROM admin_audit_log ORDER BY id DESC LIMIT 20`)
  if err != nil { util.Error(w, http.StatusBadGateway, err); return }
  seen := map[string]bool{}
  checks := make([]map[string]interface{}, 0)
  for _, row := range latest {
    id := asString(row["api_id"])
    if !seen[id] { checks = append(checks, row); seen[id] = true }
  }
  scopes := auth.ReadScopes()
  sort.Strings(scopes)
  metrics, err := GetPerformance(r)
  if err != nil { util.Error(w, http.StatusBadGateway, err); return }
  util.JSON(w, http.StatusOK, map[string]interface{}{
    "apis": apis, "keys": keys, "projects": projects, "checks": checks, "scopes": scopes, "performance": metrics, "audit": history,
  })
}

func createAPI(name, project, path, method, environment string) (interface{}, error) {
  name, project, environment = strings.TrimSpace(name), strings.TrimSpace(project), strings.TrimSpace(environment)
  if len(name) < 2 || len(name) > 80 || len(project) == 0 || len(project) > 100 ||
    !validPath(path) || (method != "GET" && method != "HEAD") || len(environment) < 2 || len(environment) > 40 {
    return nil, fmt.Errorf("nama, proyek, path, metode GET/HEAD, atau lingkungan tidak valid")
  }
  rows, err := d1.Query(`SELECT app_url FROM services WHERE name = ? AND app_url IS NOT NULL AND app_url <> '' LIMIT 1`, project)
  if err != nil || len(rows) != 1 { return nil, fmt.Errorf("proyek belum memiliki URL aplikasi di D1") }
  id, err := randomID(16)
  if err != nil { return nil, err }
  if _, err := d1.Query(`INSERT INTO managed_apis (id, name, project, path, method, environment) VALUES (?, ?, ?, ?, ?, ?)`,
    id, name, project, path, method, environment); err != nil { return nil, err }
  audit("create_api", id)
  return map[string]string{"id": id}, nil
}

func toggleAPI(id string, enabled bool) (interface{}, error) {
  if len(id) != 32 { return nil, fmt.Errorf("ID API tidak valid") }
  state := 0
  if enabled { state = 1 }
  rows, err := d1.Query(`UPDATE managed_apis SET enabled = ? WHERE id = ? RETURNING id`, state, id)
  if err != nil || len(rows) == 0 { return nil, fmt.Errorf("API tidak ditemukan") }
  audit("toggle_monitoring", id)
  return map[string]interface{}{"id": id, "enabled": enabled}, nil
}

func testAPI(ctx context.Context, id string) (interface{}, error) {
  if len(id) != 32 { return nil, fmt.Errorf("ID API tidak valid") }
  rows, err := d1.Query(`SELECT a.path, a.method, a.enabled, s.app_url FROM managed_apis a
    JOIN services s ON s.name = a.project WHERE a.id = ? LIMIT 1`, id)
  if err != nil || len(rows) != 1 { return nil, fmt.Errorf("API atau proyek tidak ditemukan") }
  row := rows[0]
  if asNumber(row["enabled"]) != 1 { return nil, fmt.Errorf("pemeriksaan API ini sedang dijeda") }
  base, err := url.Parse(asString(row["app_url"]))
  if err != nil || base.Scheme != "https" || base.User != nil || base.Hostname() == "" ||
    base.RawQuery != "" || base.Fragment != "" { return nil, fmt.Errorf("URL proyek tidak valid") }
  base.Path = asString(row["path"])
  code, latency, checkErr := checkURL(ctx, base.String(), asString(row["method"]))
  metricID, err := randomID(16)
  if err != nil { return nil, err }
  if _, err := d1.Query(`INSERT INTO api_check_metrics (id, api_id, status_code, latency_ms) VALUES (?, ?, ?, ?)`,
    metricID, id, code, latency); err != nil { return nil, err }
  audit("check_api", id)
  message := ""
  if checkErr != nil { message = checkErr.Error() }
  return map[string]interface{}{"status_code": code, "latency_ms": latency, "error": message}, nil
}

func createKey(name string, scopes []string) (interface{}, error) {
  name = strings.TrimSpace(name)
  allowed := map[string]bool{}
  for _, scope := range auth.ReadScopes() { allowed[scope] = true }
  if len(name) < 2 || len(name) > 80 || len(scopes) < 1 || len(scopes) > len(allowed) { return nil, fmt.Errorf("nama atau hak akses API key tidak valid") }
  seen := map[string]bool{}
  for _, scope := range scopes { if !allowed[scope] || seen[scope] { return nil, fmt.Errorf("hak akses API key tidak dikenal atau berulang") }; seen[scope] = true }
  sort.Strings(scopes)
  raw := make([]byte, 32)
  if _, err := rand.Read(raw); err != nil { return nil, err }
  secret := "dc_"+base64.RawURLEncoding.EncodeToString(raw)
  digest := sha256.Sum256([]byte(secret))
  id, err := randomID(16)
  if err != nil { return nil, err }
  if _, err := d1.Query(`INSERT INTO api_keys (id, name, key_prefix, key_hash, scopes) VALUES (?, ?, ?, ?, ?)`,
    id, name, secret[:10], hex.EncodeToString(digest[:]), strings.Join(scopes, ",")); err != nil { return nil, err }
  audit("create_key", id)
  return map[string]string{"id": id, "key": secret}, nil
}

func revokeKey(id string) (interface{}, error) {
  if len(id) != 32 { return nil, fmt.Errorf("ID API key tidak valid") }
  rows, err := d1.Query(`UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND revoked_at IS NULL RETURNING id`, id)
  if err != nil || len(rows) == 0 { return nil, fmt.Errorf("API key aktif tidak ditemukan") }
  audit("revoke_key", id)
  return map[string]string{"id": id}, nil
}

func rotateKey(id string) (interface{}, error) {
  if len(id) != 32 { return nil, fmt.Errorf("ID API key tidak valid") }
  rows, err := d1.Query(`SELECT name, scopes FROM api_keys WHERE id = ? AND revoked_at IS NULL LIMIT 1`, id)
  if err != nil || len(rows) == 0 { return nil, fmt.Errorf("API key aktif tidak ditemukan") }
  name := asString(rows[0]["name"])
  if len(name) > 68 { name = name[:68] }
  replacement, err := createKey(name+" (rotasi)", strings.Split(asString(rows[0]["scopes"]), ","))
  if err != nil { return nil, err }
  result := replacement.(map[string]string)
  if _, err := revokeKey(id); err != nil {
    // The new secret must still be returned once so the admin can save it and
    // manually revoke the old key. Never discard a successfully created key.
    result["warning"] = "Kunci baru dibuat, tetapi kunci lama belum dapat dicabut. Cabut kunci lama secara manual."
    return result, nil
  }
  audit("rotate_key", id)
  return result, nil
}

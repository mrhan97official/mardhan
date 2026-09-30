// Package auth protects server actions with an HttpOnly admin session or a
// separately scoped, revocable read-only API key.
package auth

import (
  "crypto/hmac"
  "crypto/sha256"
  "encoding/base64"
  "encoding/hex"
  "fmt"
  "net"
  "net/http"
  "net/url"
  "os"
  "strings"
  "time"

  "devcontrol/pkg/d1"
  "devcontrol/pkg/util"
)

const sessionLifetime = 12 * time.Hour

func Configured() bool {
  return len(os.Getenv("DEVCONTROL_ADMIN_PASSWORD")) >= 16 && len(os.Getenv("DEVCONTROL_SESSION_SECRET")) >= 32
}

func secureCookie(r *http.Request) bool {
  hostname, _, err := net.SplitHostPort(r.Host)
  if err != nil { hostname = r.Host }
  return hostname != "localhost" && hostname != "127.0.0.1" && hostname != "::1"
}

func SameOrigin(r *http.Request) bool {
  if r.Method == http.MethodGet || r.Method == http.MethodHead { return true }
  origin := r.Header.Get("Origin")
  parsed, err := url.Parse(origin)
  if err != nil || parsed.Host == "" || parsed.Host != r.Host || (parsed.Scheme != "https" && (secureCookie(r) || parsed.Scheme != "http")) { return false }
  if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" { return false }
  return true
}

func signature(payload string) string {
  mac := hmac.New(sha256.New, []byte(os.Getenv("DEVCONTROL_SESSION_SECRET")))
  _, _ = mac.Write([]byte("v2|" + payload))
  return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// IsAdmin is true for the owner (admin password) and for members with the
// "admin" role. Existing admin-only handlers keep using it unchanged.
func IsAdmin(r *http.Request) bool {
  p := Current(r)
  return p != nil && (p.Role == RoleOwner || p.Role == RoleAdmin)
}

// IsOwner is true only for the admin-password session.
func IsOwner(r *http.Request) bool {
  p := Current(r)
  return p != nil && p.Role == RoleOwner
}

var readScopes = map[string]string{
  "overview": "read:overview", "deployments": "read:deployments",
  "environments": "read:environments", "health": "read:health",
  "performance": "read:performance", "services": "read:services",
  "activity": "read:activity", "logs": "read:logs",
}

func ReadScopes() []string {
  scopes := make([]string, 0, len(readScopes))
  for _, scope := range readScopes { scopes = append(scopes, scope) }
  // Caller sorts for deterministic UI output.
  return scopes
}

func Allowed(r *http.Request, resource string) bool {
  lockdown := LockdownActive()
  if p := Current(r); p != nil {
    if lockdown && !lockdownAllows(p.Role, resource, r.Method) { return false }
    return Can(p.Role, resource, r.Method)
  }
  if lockdown { return false }
  scope, ok := readScopes[resource]
  if !ok || r.Method != http.MethodGet { return false }
  bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
  if !strings.HasPrefix(bearer, "dc_") || len(bearer) != 46 { return false }
  digest := sha256.Sum256([]byte(bearer))
  hash := hex.EncodeToString(digest[:])
  rows, err := d1.Query(`SELECT scopes FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL
    AND (expires_at IS NULL OR expires_at > datetime('now')) LIMIT 1`, hash)
  if noColumn(err) { rows, err = d1.Query(`SELECT scopes FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL LIMIT 1`, hash) }
  if err != nil || len(rows) != 1 { return false }
  _, _ = d1.Query(`UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE key_hash = ? AND (last_used_at IS NULL OR last_used_at < datetime('now', '-1 hour'))`, hash)
  raw, _ := rows[0]["scopes"].(string)
  for _, granted := range strings.Split(raw, ",") { if granted == scope { return true } }
  return false
}

func HandleSession(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Cache-Control", "no-store")
  if !Configured() {
    util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("konfigurasi admin belum lengkap: isi DEVCONTROL_ADMIN_PASSWORD (minimal 16 karakter) di Vercel lalu Redeploy; DEVCONTROL_SESSION_SECRET kini dibuat otomatis"))
    return
  }
  switch r.Method {
  case http.MethodGet:
    p := Current(r)
    if p == nil { util.JSON(w, http.StatusOK, map[string]interface{}{"authenticated": false}); return }
    util.JSON(w, http.StatusOK, map[string]interface{}{"authenticated": true, "role": p.Role, "name": p.Name, "subject": p.Subject, "apps": p.Apps})
  case http.MethodPost:
    login(w, r)
  case http.MethodDelete:
    // Logging out also revokes this cookie on the server, so a copy taken
    // earlier stops working immediately instead of at its 12-hour expiry.
    if c, ok := readClaims(r); ok { revokeClaims(c) }
    clearSession(w, r)
    util.JSON(w, http.StatusOK, map[string]bool{"authenticated": false})
  default:
    util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("metode tidak didukung"))
  }
}

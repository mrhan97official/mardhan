// Package auth protects server actions with an HttpOnly admin session or a
// separately scoped, revocable read-only API key.
package auth

import (
  "crypto/hmac"
  "crypto/rand"
  "crypto/sha256"
  "crypto/subtle"
  "encoding/base64"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "net"
  "net/http"
  "net/url"
  "os"
  "strconv"
  "strings"
  "time"

  "devcontrol/pkg/d1"
  "devcontrol/pkg/util"
)

const cookieName = "devcontrol_session"
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
  _, _ = mac.Write([]byte(payload))
  return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func IsAdmin(r *http.Request) bool {
  if !Configured() { return false }
  cookie, err := r.Cookie(cookieName)
  if err != nil { return false }
  parts := strings.Split(cookie.Value, ".")
  if len(parts) != 3 || len(parts[1]) < 20 { return false }
  expiry, err := strconv.ParseInt(parts[0], 10, 64)
  if err != nil || expiry <= time.Now().Unix() || expiry > time.Now().Add(sessionLifetime).Unix() { return false }
  expected := signature(parts[0]+"."+parts[1])
  return subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expected)) == 1
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
  if IsAdmin(r) { return true }
  scope, ok := readScopes[resource]
  if !ok || r.Method != http.MethodGet { return false }
  bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
  if !strings.HasPrefix(bearer, "dc_") || len(bearer) != 46 { return false }
  digest := sha256.Sum256([]byte(bearer))
  rows, err := d1.Query(`SELECT scopes FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL LIMIT 1`, hex.EncodeToString(digest[:]))
  if err != nil || len(rows) != 1 { return false }
  raw, _ := rows[0]["scopes"].(string)
  for _, granted := range strings.Split(raw, ",") { if granted == scope { return true } }
  return false
}

func HandleSession(w http.ResponseWriter, r *http.Request) {
  if !Configured() {
    util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("konfigurasi admin belum lengkap: isi DEVCONTROL_ADMIN_PASSWORD (minimal 16 karakter) dan DEVCONTROL_SESSION_SECRET (minimal 32 karakter) di Vercel"))
    return
  }
  switch r.Method {
  case http.MethodGet:
    util.JSON(w, http.StatusOK, map[string]interface{}{"authenticated": IsAdmin(r), "role": "admin"})
  case http.MethodPost:
    var body struct { Password string `json:"password"` }
    if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
      util.Error(w, http.StatusBadRequest, fmt.Errorf("form login tidak valid")); return
    }
    got, want := sha256.Sum256([]byte(body.Password)), sha256.Sum256([]byte(os.Getenv("DEVCONTROL_ADMIN_PASSWORD")))
    if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
      util.Error(w, http.StatusUnauthorized, fmt.Errorf("kata sandi admin salah")); return
    }
    nonce := make([]byte, 24)
    if _, err := rand.Read(nonce); err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    payload := fmt.Sprintf("%d.%s", time.Now().Add(sessionLifetime).Unix(), base64.RawURLEncoding.EncodeToString(nonce))
    http.SetCookie(w, &http.Cookie{Name: cookieName, Value: payload+"."+signature(payload), Path: "/",
      HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteStrictMode,
      MaxAge: int(sessionLifetime.Seconds())})
    util.JSON(w, http.StatusOK, map[string]interface{}{"authenticated": true, "role": "admin"})
  case http.MethodDelete:
    http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true,
      Secure: secureCookie(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
    util.JSON(w, http.StatusOK, map[string]bool{"authenticated": false})
  default:
    util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("metode tidak didukung"))
  }
}

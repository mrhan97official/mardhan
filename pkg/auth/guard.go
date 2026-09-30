package auth

// Pusat Keamanan hooks inside the gate: signed-in devices, login origins,
// Mode Darurat, a per-instance rate limit, and sealing of the 2FA secret.

import (
  "crypto/aes"
  "crypto/cipher"
  "crypto/rand"
  "crypto/sha256"
  "encoding/base64"
  "fmt"
  "net/http"
  "os"
  "strings"
  "sync"
  "time"

  "devcontrol/pkg/d1"
)

// SecurityEvent is set by the API entrypoint (Pusat Keamanan): it stores
// the alert, shows it as a pop-up and sends push/Telegram. Levels:
// "waspada", "siaga", "darurat". The key de-duplicates repeats.
var SecurityEvent func(level, kind, key, title, detail string)

func raise(level, kind, key, title, detail string) {
  if SecurityEvent != nil { SecurityEvent(level, kind, key, title, detail) }
}

func noColumn(err error) bool {
  return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such column")
}

func shortAgent(agent string) string {
  agent = strings.TrimSpace(agent)
  lower := strings.ToLower(agent)
  device := "perangkat tak dikenal"
  for _, pair := range [][2]string{{"iphone", "iPhone"}, {"ipad", "iPad"}, {"android", "Android"}, {"windows", "Windows"}, {"mac os", "Mac"}, {"linux", "Linux"}} {
    if strings.Contains(lower, pair[0]) { device = pair[1]; break }
  }
  browser := ""
  for _, pair := range [][2]string{{"edg/", "Edge"}, {"chrome/", "Chrome"}, {"firefox/", "Firefox"}, {"safari/", "Safari"}} {
    if strings.Contains(lower, pair[0]) { browser = pair[1]; break }
  }
  if browser == "" { return device }
  return browser + " di " + device
}

// ---------- signed-in devices ----------

func recordSession(r *http.Request, c claims) {
  name := "Owner"
  if c.Sub != RoleOwner {
    if found, ok := member(c.Sub); ok { name = found.Name } else { name = c.Sub }
  }
  agent := r.UserAgent()
  if len(agent) > 300 { agent = agent[:300] }
  _, _ = d1.Query(`INSERT INTO user_sessions (nonce, subject, name, role, ip, user_agent, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(nonce) DO NOTHING`, c.Nonce, c.Sub, name, c.Role, ClientIP(r), agent, time.Unix(c.Exp, 0).UTC().Format("2006-01-02 15:04:05"))
  _, _ = d1.Query(`DELETE FROM user_sessions WHERE expires_at < datetime('now')`)
}

// Sessions lists signed-in devices; current marks the caller's own.
func Sessions(r *http.Request) ([]map[string]interface{}, error) {
  rows, err := d1.Query(`SELECT nonce, subject, name, role, ip, user_agent, created_at, expires_at FROM user_sessions
    WHERE expires_at > datetime('now') ORDER BY created_at DESC LIMIT 100`)
  if err != nil { return nil, err }
  mine := ""
  if c, ok := readClaims(r); ok { mine = c.Nonce }
  list := make([]map[string]interface{}, 0, len(rows))
  for _, row := range rows {
    nonce, _ := row["nonce"].(string)
    agent, _ := row["user_agent"].(string)
    list = append(list, map[string]interface{}{
      "id": sessionID(nonce), "name": row["name"], "role": row["role"], "ip": row["ip"], "device": shortAgent(agent),
      "created_at": row["created_at"], "expires_at": row["expires_at"], "current": nonce == mine,
    })
  }
  return list, nil
}

// sessionID exposes a stable handle without revealing the nonce itself.
func sessionID(nonce string) string {
  sum := sha256.Sum256([]byte("devcontrol/session-id|" + nonce))
  return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// RevokeSession signs one listed device out (owner only).
func RevokeSession(id string) (bool, error) {
  rows, err := d1.Query(`SELECT nonce, expires_at FROM user_sessions WHERE expires_at > datetime('now') LIMIT 200`)
  if err != nil { return false, err }
  for _, row := range rows {
    nonce, _ := row["nonce"].(string)
    if sessionID(nonce) != id { continue }
    expires, _ := row["expires_at"].(string)
    _, _ = d1.Query(`INSERT INTO revoked_sessions (nonce, expires_at) VALUES (?, ?) ON CONFLICT(nonce) DO NOTHING`, nonce, expires)
    _, _ = d1.Query(`DELETE FROM user_sessions WHERE nonce = ?`, nonce)
    revocations.Lock()
    if revocations.seen == nil { revocations.seen = map[string]revocationState{} }
    revocations.seen[nonce] = revocationState{revoked: true, at: time.Now()}
    revocations.Unlock()
    audit("session_revoke", id)
    return true, nil
  }
  return false, nil
}

// ---------- login origins ----------

func knownOrigin(subject, ip string) bool {
  rows, err := d1.Query(`SELECT 1 AS found FROM login_origins WHERE subject = ? AND ip = ? LIMIT 1`, subject, ip)
  return err == nil && len(rows) > 0
}

// noteOrigin records where a successful sign-in came from and raises an
// alert the first time an account signs in from a new address.
func noteOrigin(r *http.Request, subject, label string) {
  ip := ClientIP(r)
  rows, err := d1.Query(`SELECT COUNT(*) AS n, SUM(CASE WHEN ip = ? THEN 1 ELSE 0 END) AS same FROM login_origins WHERE subject = ?`, ip, subject)
  if err != nil || len(rows) == 0 { return }
  _, _ = d1.Query(`INSERT INTO login_origins (subject, ip) VALUES (?, ?) ON CONFLICT(subject, ip) DO UPDATE SET last_seen = CURRENT_TIMESTAMP`, subject, ip)
  if toInt(rows[0]["n"]) == 0 || toInt(rows[0]["same"]) > 0 { return } // first ever sign-in sets the baseline
  raise("waspada", "login_new_ip", "login-ip:"+subject+":"+ip, "Login dari alamat baru: "+label,
    fmt.Sprintf("%s masuk dari IP %s memakai %s. Kalau ini bukan Anda atau anggota tim Anda, pilih \"Bukan saya\".", label, ip, shortAgent(r.UserAgent())))
}

// ---------- Mode Darurat ----------

var lockdownCache struct {
  sync.Mutex
  active bool
  at     time.Time
}

// LockdownActive: during Mode Darurat only the owner may use DevControl,
// read-only except for the security pages; API keys are refused.
func LockdownActive() bool {
  lockdownCache.Lock()
  if time.Since(lockdownCache.at) < 10*time.Second { active := lockdownCache.active; lockdownCache.Unlock(); return active }
  lockdownCache.Unlock()
  value, err := authSetting("lockdown_since")
  active := err == nil && value != ""
  lockdownCache.Lock()
  lockdownCache.active, lockdownCache.at = active, time.Now()
  lockdownCache.Unlock()
  return active
}

func setLockdownCache(active bool) {
  lockdownCache.Lock()
  lockdownCache.active, lockdownCache.at = active, time.Now()
  lockdownCache.Unlock()
}

func LockdownSince() string { value, _ := authSetting("lockdown_since"); return value }

func lockdownAllows(role, resource, method string) bool {
  if role != RoleOwner { return false }
  if method == http.MethodGet || method == http.MethodHead { return true }
  return resource == "security" || resource == "two-factor" || resource == "push"
}

// StartLockdown signs everyone else out (all owner devices except this one,
// every member) and freezes changes until EndLockdown.
func StartLockdown(w http.ResponseWriter, r *http.Request) error {
  c, ok := readClaims(r)
  if !ok || c.Role != RoleOwner { return fmt.Errorf("Mode Darurat hanya dapat diaktifkan owner") }
  if err := putAuthSetting("lockdown_since", time.Now().UTC().Format("2006-01-02 15:04:05")); err != nil { return err }
  if _, err := d1.Query(`INSERT INTO auth_settings (key, value) VALUES ('owner_epoch', '1')
    ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)`); err != nil { return err }
  if _, err := d1.Query(`UPDATE members SET epoch = epoch + 1`); err != nil { return err }
  _, _ = d1.Query(`DELETE FROM user_sessions`)
  forget("")
  revokeClaims(c)
  epoch := ownerEpoch()
  if epoch < 0 { return fmt.Errorf("database sesi tidak dapat dihubungi") }
  if err := issue(w, r, claims{Sub: RoleOwner, Role: RoleOwner, Epoch: epoch, Auth: c.Auth}); err != nil { return err }
  setLockdownCache(true)
  audit("lockdown_start", ClientIP(r))
  return nil
}

func EndLockdown() error {
  if err := deleteAuthSetting("lockdown_since"); err != nil { return err }
  setLockdownCache(false)
  audit("lockdown_end", "owner")
  return nil
}

// ---------- rate limit (per server instance) ----------

var limiter struct {
  sync.Mutex
  window time.Time
  counts map[string]int
}

// RateLimited allows 300 API requests per minute per address on each
// server instance: enough for the app's polling, a brake on a leaked token.
func RateLimited(key string) bool {
  limiter.Lock()
  defer limiter.Unlock()
  now := time.Now()
  if now.Sub(limiter.window) > time.Minute || limiter.counts == nil { limiter.window, limiter.counts = now, map[string]int{} }
  limiter.counts[key]++
  return limiter.counts[key] > 300
}

// ---------- 2FA secret sealing ----------

func require2FA() bool {
  return strings.TrimSpace(os.Getenv("DEVCONTROL_REQUIRE_2FA")) == "1" && !totpResetActive()
}

func totpCipher() (cipher.AEAD, error) {
  sum := sha256.Sum256([]byte("devcontrol/totp/v1|" + os.Getenv("DEVCONTROL_SESSION_SECRET")))
  block, err := aes.NewCipher(sum[:])
  if err != nil { return nil, err }
  return cipher.NewGCM(block)
}

// sealTOTP encrypts the 2FA secret before it is written to D1, so someone
// who can only read or edit the database cannot generate codes.
func sealTOTP(secret string) (string, error) {
  aead, err := totpCipher()
  if err != nil { return "", err }
  nonce := make([]byte, aead.NonceSize())
  if _, err := rand.Read(nonce); err != nil { return "", err }
  return "v1:" + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(secret), nil)), nil
}

func openTOTP(stored string) (string, error) {
  if stored == "" || !strings.HasPrefix(stored, "v1:") { return stored, nil } // older plain value
  raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(stored, "v1:"))
  if err != nil { return "", fmt.Errorf("rahasia 2FA rusak") }
  aead, err := totpCipher()
  if err != nil { return "", err }
  if len(raw) < aead.NonceSize() { return "", fmt.Errorf("rahasia 2FA rusak") }
  plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
  if err != nil {
    return "", fmt.Errorf("rahasia 2FA tidak dapat dibuka karena DEVCONTROL_SESSION_SECRET berubah; isi DEVCONTROL_TOTP_RESET=1 di Vercel, redeploy, lalu aktifkan ulang 2FA")
  }
  return string(plain), nil
}

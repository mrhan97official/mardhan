package auth

// Multi-user access. The owner signs in with DEVCONTROL_ADMIN_PASSWORD;
// members sign in with a long random access token (dcm_…, 256-bit) that is
// stored only as a SHA-256 hash and shown once. Roles are read from D1 on
// every request (cached briefly), so a role change or revocation applies
// without waiting for the cookie to expire. Optional per-member IP allowlists
// are enforced at login and on every request.

import (
  "crypto/rand"
  "crypto/sha256"
  "crypto/subtle"
  "encoding/base64"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "net"
  "net/http"
  "os"
  "strconv"
  "strings"
  "sync"
  "time"

  "devcontrol/pkg/d1"
  "devcontrol/pkg/util"
)

const (
  RoleOwner    = "owner"
  RoleAdmin    = "admin"
  RoleOperator = "operator"
  RoleViewer   = "viewer"
  tokenPrefix  = "dcm_"
  cacheTTL     = 15 * time.Second
)

type Principal struct {
  Subject string // "owner" or member id
  Role    string
  Name    string
}

type claims struct {
  Sub   string `json:"s"`
  Role  string `json:"r"`
  Exp   int64  `json:"e"`
  Epoch int64  `json:"p"`
  Nonce string `json:"n"`
  // Auth is when the credential (password/token, plus 2FA) was last typed.
  // Dangerous actions require it to be recent (see RecentlyConfirmed).
  Auth  int64  `json:"a,omitempty"`
}

type memberRow struct {
  ID      string
  Name    string
  Role    string
  Epoch   int64
  IPs     []string
  Revoked bool
  at      time.Time
}

var cache struct {
  sync.Mutex
  ownerEpoch   int64
  ownerEpochAt time.Time
  members      map[string]memberRow
}

// ---------- permissions ----------

var viewerRead = map[string]bool{
  "overview": true, "deployments": true, "environments": true, "health": true, "performance": true,
  "services": true, "activity": true, "logs": true, "github-repos": true, "github-branches": true, "vercel-projects": true,
  "project-thumbnails": true, "app-promo": true, "branding": true, "deploy-history": true,
}

// Can is the single permission matrix for every API resource.
func Can(role, resource, method string) bool {
  read := method == http.MethodGet || method == http.MethodHead
  // Every signed-in role manages push notifications for its own devices;
  // the handler limits admin-only events (confirmations) itself.
  if resource == "push" { return role == RoleOwner || role == RoleAdmin || role == RoleOperator || role == RoleViewer }
  switch role {
  case RoleOwner:
    return true
  case RoleAdmin:
    // Owner-only: member management, 2FA, and Update Diri (it replaces
    // DevControl's own code, so a leaked admin token must not reach it).
    return resource != "members" && resource != "two-factor" && resource != "self-update"
  case RoleOperator:
    if read { return viewerRead[resource] }
    switch resource {
    case "trigger-deployment": return method == http.MethodPost // Aplikasi Baru / Update Aplikasi
    case "deployments": return method == http.MethodDelete       // close a failed run
    case "diagnose": return method == http.MethodPost
    }
    return false
  case RoleViewer:
    if read { return viewerRead[resource] }
    return resource == "diagnose" && method == http.MethodPost
  }
  return false
}

func validRole(role string) bool { return role == RoleAdmin || role == RoleOperator || role == RoleViewer }

// ---------- client IP ----------

// ClientIP uses the headers Vercel's edge sets (and overwrites), so a client
// cannot spoof them on a Vercel deployment.
// ClientIP trusts only headers the platform sets itself: on Vercel
// X-Vercel-Forwarded-For/X-Real-Ip; elsewhere the connection address, unless
// DEVCONTROL_TRUST_PROXY=1 says a proxy in front rewrites X-Forwarded-For.
func ClientIP(r *http.Request) string {
  headers := []string{}
  if strings.TrimSpace(os.Getenv("VERCEL")) != "" {
    headers = []string{"X-Vercel-Forwarded-For", "X-Real-Ip"}
  } else if strings.TrimSpace(os.Getenv("DEVCONTROL_TRUST_PROXY")) == "1" {
    headers = []string{"X-Real-Ip", "X-Forwarded-For"}
  }
  for _, header := range headers {
    if value := strings.TrimSpace(strings.Split(r.Header.Get(header), ",")[0]); value != "" {
      if ip := net.ParseIP(value); ip != nil { return ip.String() }
    }
  }
  host, _, err := net.SplitHostPort(r.RemoteAddr)
  if err != nil { host = r.RemoteAddr }
  return host
}

func ipAllowed(list []string, raw string) bool {
  if len(list) == 0 { return true }
  ip := net.ParseIP(raw)
  if ip == nil { return false }
  for _, item := range list {
    if strings.Contains(item, "/") {
      if _, network, err := net.ParseCIDR(item); err == nil && network.Contains(ip) { return true }
    } else if allowed := net.ParseIP(item); allowed != nil && allowed.Equal(ip) {
      return true
    }
  }
  return false
}

func cleanIPList(input []string) ([]string, error) {
  out := []string{}
  for _, raw := range input {
    for _, item := range strings.FieldsFunc(raw, func(c rune) bool { return c == ',' || c == '\n' || c == ' ' || c == ';' }) {
      item = strings.TrimSpace(item)
      if item == "" { continue }
      if strings.Contains(item, "/") {
        if _, _, err := net.ParseCIDR(item); err != nil { return nil, fmt.Errorf("CIDR tidak valid: %s", item) }
      } else if net.ParseIP(item) == nil {
        return nil, fmt.Errorf("IP tidak valid: %s", item)
      }
      out = append(out, item)
    }
  }
  if len(out) > 20 { return nil, fmt.Errorf("maksimal 20 IP/CIDR per member") }
  return out, nil
}

// ---------- D1 state (with short cache) ----------

// Tables that do not exist yet (fresh install) mean nothing was revoked.
func missingTable(err error) bool {
  message := strings.ToLower(err.Error())
  return strings.Contains(message, "no such table") || strings.Contains(message, "credentials are not configured")
}

// ownerEpoch returns -1 when D1 cannot confirm the value: the owner session
// is then refused rather than risk accepting a revoked cookie.
func ownerEpoch() int64 {
  cache.Lock()
  if time.Since(cache.ownerEpochAt) < cacheTTL { value := cache.ownerEpoch; cache.Unlock(); return value }
  cache.Unlock()
  rows, err := d1.Query(`SELECT value FROM auth_settings WHERE key = 'owner_epoch' LIMIT 1`)
  value := int64(0)
  if err != nil {
    if !missingTable(err) { return -1 }
  } else if len(rows) == 1 {
    text, _ := rows[0]["value"].(string)
    value, _ = strconv.ParseInt(text, 10, 64)
  }
  cache.Lock()
  cache.ownerEpoch, cache.ownerEpochAt = value, time.Now()
  cache.Unlock()
  return value
}

func toInt(value interface{}) int64 {
  switch number := value.(type) {
  case float64: return int64(number)
  case string: parsed, _ := strconv.ParseInt(number, 10, 64); return parsed
  }
  return 0
}

func rowToMember(row map[string]interface{}) memberRow {
  text := func(key string) string { value, _ := row[key].(string); return value }
  var ips []string
  _ = json.Unmarshal([]byte(text("ip_allowlist")), &ips)
  return memberRow{ID: text("id"), Name: text("name"), Role: text("role"), Epoch: toInt(row["epoch"]), IPs: ips, Revoked: text("revoked_at") != ""}
}

func member(id string) (memberRow, bool) {
  cache.Lock()
  if cached, ok := cache.members[id]; ok && time.Since(cached.at) < cacheTTL { cache.Unlock(); return cached, true }
  cache.Unlock()
  rows, err := d1.Query(`SELECT id, name, role, epoch, ip_allowlist,
    CASE WHEN expires_at IS NOT NULL AND expires_at < datetime('now') THEN 'expired' ELSE COALESCE(revoked_at, '') END AS revoked_at
    FROM members WHERE id = ? LIMIT 1`, id)
  if noColumn(err) { rows, err = d1.Query(`SELECT id, name, role, epoch, ip_allowlist, COALESCE(revoked_at, '') AS revoked_at FROM members WHERE id = ? LIMIT 1`, id) }
  if err != nil || len(rows) == 0 { return memberRow{}, false }
  found := rowToMember(rows[0])
  found.at = time.Now()
  cache.Lock()
  if cache.members == nil { cache.members = map[string]memberRow{} }
  cache.members[id] = found
  cache.Unlock()
  return found, true
}

func forget(id string) {
  cache.Lock()
  delete(cache.members, id)
  if id == "" { cache.members = nil; cache.ownerEpochAt = time.Time{} }
  cache.Unlock()
}

// ---------- cookies ----------

// __Host- cookies must be Secure, Path=/ and host-only: they cannot be set
// by a sibling subdomain or over plain HTTP.
func cookieNameFor(r *http.Request) string {
  if secureCookie(r) { return "__Host-devcontrol_session" }
  return "devcontrol_session"
}

func issue(w http.ResponseWriter, r *http.Request, c claims) error {
  nonce := make([]byte, 18)
  if _, err := rand.Read(nonce); err != nil { return err }
  c.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
  c.Exp = time.Now().Add(sessionLifetime).Unix()
  raw, err := json.Marshal(c)
  if err != nil { return err }
  payload := base64.RawURLEncoding.EncodeToString(raw)
  http.SetCookie(w, &http.Cookie{Name: cookieNameFor(r), Value: payload + "." + signature(payload), Path: "/",
    HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds())})
  recordSession(r, c)
  return nil
}

func clearSession(w http.ResponseWriter, r *http.Request) {
  for _, name := range []string{"__Host-devcontrol_session", "devcontrol_session"} {
    http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: secureCookie(r) || strings.HasPrefix(name, "__Host-"),
      SameSite: http.SameSiteStrictMode, MaxAge: -1})
  }
}

func readClaims(r *http.Request) (claims, bool) {
  cookie, err := r.Cookie(cookieNameFor(r))
  if err != nil { return claims{}, false }
  parts := strings.Split(cookie.Value, ".")
  if len(parts) != 2 || len(parts[0]) > 1024 { return claims{}, false }
  expected := signature(parts[0])
  if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) != 1 { return claims{}, false }
  raw, err := base64.RawURLEncoding.DecodeString(parts[0])
  if err != nil { return claims{}, false }
  var c claims
  if json.Unmarshal(raw, &c) != nil { return claims{}, false }
  now := time.Now().Unix()
  if c.Exp <= now || c.Exp > time.Now().Add(sessionLifetime).Unix() { return claims{}, false }
  return c, true
}

// Current returns the signed-in principal, or nil.
func Current(r *http.Request) *Principal {
  if !Configured() { return nil }
  c, ok := readClaims(r)
  if !ok || isRevoked(c.Nonce) { return nil }
  if c.Sub == RoleOwner {
    if epoch := ownerEpoch(); epoch < 0 || c.Epoch != epoch { return nil }
    return &Principal{Subject: RoleOwner, Role: RoleOwner, Name: "Owner"}
  }
  found, exists := member(c.Sub)
  if !exists || found.Revoked || found.Epoch != c.Epoch || !validRole(found.Role) { return nil }
  if !ipAllowed(found.IPs, ClientIP(r)) { return nil }
  return &Principal{Subject: found.ID, Role: found.Role, Name: found.Name}
}

// ---------- login with lockout ----------

func audit(action, target string) {
  _, _ = d1.Query(`INSERT INTO admin_audit_log (action, target) VALUES (?, ?)`, action, target)
}

// lockedFor returns remaining lockout minutes for a key (an IP, or a
// prefixed key such as "global:owner"); 0 = free. A database error is
// returned so callers refuse the attempt instead of allowing unlimited tries.
func lockedFor(key string) (int64, error) {
  rows, err := d1.Query(`SELECT CAST((julianday(locked_until) - julianday('now')) * 1440 AS INTEGER) + 1 AS minutes
    FROM auth_attempts WHERE ip = ? AND locked_until > datetime('now') LIMIT 1`, key)
  if err != nil {
    if missingTable(err) { return 0, nil }
    return 0, err
  }
  if len(rows) == 0 { return 0, nil }
  return toInt(rows[0]["minutes"]), nil
}

// failureCount adds one failure for key (reset after a quiet day).
func failureCount(key string) int64 {
  rows, err := d1.Query(`INSERT INTO auth_attempts (ip, failures, updated_at) VALUES (?, 1, CURRENT_TIMESTAMP)
    ON CONFLICT(ip) DO UPDATE SET failures = CASE WHEN updated_at < datetime('now', '-1 day') THEN 1 ELSE failures + 1 END,
      updated_at = CURRENT_TIMESTAMP
    RETURNING failures`, key)
  if err != nil || len(rows) == 0 { return 0 }
  return toInt(rows[0]["failures"])
}

func lockKey(key string, minutes int64) {
  _, _ = d1.Query(`UPDATE auth_attempts SET locked_until = datetime('now', ?) WHERE ip = ?`, fmt.Sprintf("+%d minutes", minutes), key)
}

// recordFailure: after 5 failures the key is locked 1, 2, 4 … up to 60 minutes.
func recordFailure(key string) {
  failures := failureCount(key)
  if failures < 5 { return }
  minutes := int64(1) << uint(minInt(failures-5, 6))
  if minutes > 60 { minutes = 60 }
  lockKey(key, minutes)
  if failures == 5 && !strings.Contains(key, ":") {
    raise("waspada", "login_failed", "login-lock:"+key+":"+time.Now().UTC().Format("2006-01-02T15"), "Login gagal berulang",
      "5 percobaan masuk gagal dari IP "+key+". IP itu dikunci sementara. Abaikan bila itu Anda sendiri.")
  }
}

// globalOwnerKey counts wrong owner passwords from every IP together, so a
// guess spread over many addresses is still slowed down.
const globalOwnerKey = "global:owner"

func recordGlobalOwnerFailure() {
  failures := failureCount(globalOwnerKey)
  if failures == 10 || failures == 20 {
    raise("siaga", "login_failed", "login-global:"+strconv.FormatInt(failures, 10)+":"+time.Now().UTC().Format("2006-01-02"), "Banyak percobaan login owner",
      strconv.FormatInt(failures, 10)+" kata sandi owner salah hari ini dari berbagai alamat. Aktifkan 2FA dan pertimbangkan mengganti kata sandi.")
  }
  if failures >= 20 { lockKey(globalOwnerKey, 15) }
}

// Throttled / RecordFailure let other endpoints with a secret (for example
// the ZIP archive key) reuse the same lockout table under their own prefix.
func Throttled(key string) (int64, error) { return lockedFor(key) }
func RecordFailure(key string)            { recordFailure(key) }
func ClearFailures(key string)            { _, _ = d1.Query(`DELETE FROM auth_attempts WHERE ip = ?`, key) }



func minInt(a, b int64) int64 { if a < b { return a }; return b }

func hashToken(token string) string {
  digest := sha256.Sum256([]byte(token))
  return hex.EncodeToString(digest[:])
}

type loginBody struct {
  Credential string `json:"credential"`
  Password   string `json:"password"`
  Code       string `json:"code"`
  Action     string `json:"action"`
}

func loginFailed(w http.ResponseWriter, ip, target string) {
  recordFailure(ip)
  audit("login_failed", target+" @ "+ip)
  time.Sleep(600 * time.Millisecond) // slows automated guessing
  util.Error(w, http.StatusUnauthorized, fmt.Errorf("kata sandi, token akses, atau kode 2FA salah"))
}

func totpRequired(w http.ResponseWriter) {
  util.JSON(w, http.StatusUnauthorized, map[string]interface{}{
    "error": "Masukkan kode 6 digit dari aplikasi authenticator.", "totp_required": true})
}

// checkLockout answers 429/503 itself and returns false when the attempt
// must not be evaluated.
func checkLockout(w http.ResponseWriter, key, message string) bool {
  minutes, err := lockedFor(key)
  if err != nil {
    util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("status keamanan login tidak dapat diperiksa; coba lagi sebentar"))
    return false
  }
  if minutes > 0 {
    w.Header().Set("Retry-After", strconv.FormatInt(minutes*60, 10))
    util.Error(w, http.StatusTooManyRequests, fmt.Errorf(message, minutes))
    return false
  }
  return true
}

// ownerProof checks the owner password and, when enabled, the 2FA code.
// It writes the failure response itself and returns false on any failure.
func ownerProof(w http.ResponseWriter, ip, credential, code string) bool {
  secret, err := ownerTOTPSecret()
  if err != nil { util.Error(w, http.StatusServiceUnavailable, err); return false }
  // Addresses the owner signed in from before are not held by the global
  // lock, so strangers cannot keep the owner locked out.
  if secret == "" && !knownOrigin(RoleOwner, ip) && !checkLockout(w, globalOwnerKey, "login owner dikunci sementara karena banyak percobaan gagal; coba lagi dalam %d menit") { return false }
  got, want := sha256.Sum256([]byte(credential)), sha256.Sum256([]byte(os.Getenv("DEVCONTROL_ADMIN_PASSWORD")))
  if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
    recordGlobalOwnerFailure()
    loginFailed(w, ip, "owner")
    return false
  }
  if secret != "" {
    if strings.TrimSpace(code) == "" { totpRequired(w); return false }
    if !checkLockout(w, "global:owner-2fa", "kode 2FA owner terlalu sering salah; coba lagi dalam %d menit") { return false }
    ok, verifyErr := verifyTOTP(secret, code, true)
    if verifyErr != nil { util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("kode 2FA tidak dapat diperiksa; coba lagi")); return false }
    if !ok {
      // A right password with a wrong code means the password is known to
      // someone: count it across all addresses and alert the owner.
      failures := failureCount("global:owner-2fa")
      if failures >= 10 { lockKey("global:owner-2fa", 30) }
      raise("siaga", "totp_failed", "totp-failed:"+time.Now().UTC().Format("2006-01-02T15"), "Kata sandi owner benar, tetapi kode 2FA salah",
        "Seseorang memasukkan kata sandi owner yang benar dari IP "+ip+", lalu gagal di kode 2FA. Kalau bukan Anda, ganti DEVCONTROL_ADMIN_PASSWORD sekarang.")
      loginFailed(w, ip, "owner-2fa")
      return false
    }
    ClearFailures("global:owner-2fa")
  }
  return true
}

func login(w http.ResponseWriter, r *http.Request) {
  var body loginBody
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
    util.Error(w, http.StatusBadRequest, fmt.Errorf("form login tidak valid")); return
  }
  credential := strings.TrimSpace(body.Credential)
  if credential == "" { credential = body.Password }
  ip := ClientIP(r)
  if !checkLockout(w, ip, "terlalu banyak percobaan gagal; coba lagi dalam %d menit") { return }
  if body.Action == "confirm" { confirmSession(w, r, ip, credential, body.Code); return }
  now := time.Now().Unix()

  if strings.HasPrefix(credential, tokenPrefix) {
    rows, err := d1.Query(`SELECT id, name, role, epoch, ip_allowlist,
      CASE WHEN expires_at IS NOT NULL AND expires_at < datetime('now') THEN 'expired' ELSE COALESCE(revoked_at, '') END AS revoked_at
      FROM members WHERE token_hash = ? LIMIT 1`, hashToken(credential))
    if noColumn(err) { rows, err = d1.Query(`SELECT id, name, role, epoch, ip_allowlist, COALESCE(revoked_at, '') AS revoked_at FROM members WHERE token_hash = ? LIMIT 1`, hashToken(credential)) }
    if err != nil || len(rows) == 0 { loginFailed(w, ip, "token"); return }
    found := rowToMember(rows[0])
    if found.Revoked || !validRole(found.Role) { loginFailed(w, ip, "member:"+found.Name+" (dicabut)"); return }
    if !ipAllowed(found.IPs, ip) { loginFailed(w, ip, "member:"+found.Name+" (IP tidak diizinkan)"); return }
    if err := issue(w, r, claims{Sub: found.ID, Role: found.Role, Epoch: found.Epoch, Auth: now}); err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    _, _ = d1.Query(`UPDATE members SET last_login_at = CURRENT_TIMESTAMP, last_ip = ? WHERE id = ?`, ip, found.ID)
    ClearFailures(ip)
    noteOrigin(r, found.ID, "Member "+found.Name)
    audit("login_member", found.Name+" @ "+ip)
    util.JSON(w, http.StatusOK, map[string]interface{}{"authenticated": true, "role": found.Role, "name": found.Name})
    return
  }

  if !ownerProof(w, ip, credential, body.Code) { return }
  epoch := ownerEpoch()
  if epoch < 0 { util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("database sesi tidak dapat dihubungi; coba lagi")); return }
  if err := issue(w, r, claims{Sub: RoleOwner, Role: RoleOwner, Epoch: epoch, Auth: now}); err != nil { util.Error(w, http.StatusInternalServerError, err); return }
  ClearFailures(ip)
  ClearFailures(globalOwnerKey)
  noteOrigin(r, RoleOwner, "Owner")
  audit("login_owner", ip)
  util.JSON(w, http.StatusOK, map[string]interface{}{"authenticated": true, "role": RoleOwner, "name": "Owner"})
}

// confirmSession re-checks the signed-in person's own credential (step-up)
// and re-issues the cookie with a fresh Auth time. The old cookie is revoked.
func confirmSession(w http.ResponseWriter, r *http.Request, ip, credential, code string) {
  p := Current(r)
  c, ok := readClaims(r)
  if p == nil || !ok { util.Error(w, http.StatusUnauthorized, fmt.Errorf("sesi berakhir; silakan masuk lagi")); return }
  if p.Role == RoleOwner {
    if !ownerProof(w, ip, credential, code) { return }
  } else {
    if !strings.HasPrefix(credential, tokenPrefix) { loginFailed(w, ip, "confirm:"+p.Name); return }
    rows, err := d1.Query(`SELECT id FROM members WHERE token_hash = ? LIMIT 1`, hashToken(credential))
    if err != nil || len(rows) == 0 { loginFailed(w, ip, "confirm:"+p.Name); return }
    if id, _ := rows[0]["id"].(string); id != p.Subject { loginFailed(w, ip, "confirm:"+p.Name); return }
  }
  now := time.Now().Unix()
  revokeClaims(c)
  if err := issue(w, r, claims{Sub: c.Sub, Role: c.Role, Epoch: c.Epoch, Auth: now}); err != nil { util.Error(w, http.StatusInternalServerError, err); return }
  ClearFailures(ip)
  audit("session_confirm", p.Name+" @ "+ip)
  util.JSON(w, http.StatusOK, map[string]interface{}{"confirmed": true, "until": now + int64(confirmWindow.Seconds())})
}

// ---------- member management (owner only) ----------

func newToken() (string, error) {
  raw := make([]byte, 32)
  if _, err := rand.Read(raw); err != nil { return "", err }
  return tokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func newID() (string, error) {
  raw := make([]byte, 8)
  if _, err := rand.Read(raw); err != nil { return "", err }
  return hex.EncodeToString(raw), nil
}

func validMemberID(id string) bool { return len(id) == 16 && strings.Trim(id, "0123456789abcdef") == "" }

func cleanName(name string) (string, error) {
  name = strings.TrimSpace(name)
  if name == "" || len(name) > 60 { return "", fmt.Errorf("nama wajib diisi (maksimal 60 karakter)") }
  if strings.ContainsAny(name, "<>\"'`\\\x00\r\n") { return "", fmt.Errorf("nama mengandung karakter yang tidak diizinkan") }
  return name, nil
}

// HandleMembers serves /api/members. Only the owner may view or change members.
func HandleMembers(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Cache-Control", "no-store")
  if !IsOwner(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("hanya owner yang dapat mengelola member")); return }
  switch r.Method {
  case http.MethodGet:
    listMembers(w)
  case http.MethodPost, http.MethodPatch:
    changeMember(w, r)
  case http.MethodDelete:
    id := r.URL.Query().Get("id")
    if !validMemberID(id) { util.Error(w, http.StatusBadRequest, fmt.Errorf("ID member tidak valid")); return }
    rows, _ := d1.Query(`SELECT name FROM members WHERE id = ? LIMIT 1`, id)
    if _, err := d1.Query(`DELETE FROM members WHERE id = ?`, id); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    forget(id)
    name := id
    if len(rows) == 1 { name, _ = rows[0]["name"].(string) }
    audit("member_delete", name)
    util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
  default:
    util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("metode tidak didukung"))
  }
}

func listMembers(w http.ResponseWriter) {
  rows, err := d1.Query(`SELECT id, name, role, token_prefix, ip_allowlist, created_at, COALESCE(last_login_at, '') AS last_login_at,
    COALESCE(last_ip, '') AS last_ip, COALESCE(revoked_at, '') AS revoked_at FROM members ORDER BY created_at DESC LIMIT 200`)
  if err != nil { util.Error(w, http.StatusBadGateway, err); return }
  members := make([]map[string]interface{}, 0, len(rows))
  for _, row := range rows {
    text := func(key string) string { value, _ := row[key].(string); return value }
    ips := []string{}
    _ = json.Unmarshal([]byte(text("ip_allowlist")), &ips)
    members = append(members, map[string]interface{}{
      "id": text("id"), "name": text("name"), "role": text("role"), "token_prefix": text("token_prefix"), "ip_allowlist": ips,
      "created_at": text("created_at"), "last_login_at": text("last_login_at"), "last_ip": text("last_ip"), "revoked": text("revoked_at") != "",
    })
  }
  events, err := d1.Query(`SELECT action, target, created_at FROM admin_audit_log
    WHERE action LIKE 'login_%' OR action LIKE 'member_%' OR action = 'sessions_revoke_all'
    ORDER BY id DESC LIMIT 40`)
  if err != nil { events = []map[string]interface{}{} }
  locked, err := d1.Query(`SELECT ip, failures, locked_until FROM auth_attempts WHERE locked_until > datetime('now') ORDER BY locked_until DESC LIMIT 20`)
  if err != nil { locked = []map[string]interface{}{} }
  util.JSON(w, http.StatusOK, map[string]interface{}{"members": members, "events": events, "locked_ips": locked})
}

func changeMember(w http.ResponseWriter, r *http.Request) {
  var input struct {
    Action string   `json:"action"`
    ID     string   `json:"id"`
    Name   string   `json:"name"`
    Role   string   `json:"role"`
    IPs    []string `json:"ip_allowlist"`
    SetIPs bool     `json:"set_ip_allowlist"`
    IP     string   `json:"ip"`
  }
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); err != nil {
    util.Error(w, http.StatusBadRequest, fmt.Errorf("data member tidak valid")); return
  }

  if input.Action == "revoke_all_sessions" {
    // Signs out everyone, the owner included, on every device.
    if _, err := d1.Query(`INSERT INTO auth_settings (key, value) VALUES ('owner_epoch', '1')
      ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)`); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    if _, err := d1.Query(`UPDATE members SET epoch = epoch + 1`); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    forget("")
    audit("sessions_revoke_all", "owner")
    clearSession(w, r)
    util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
    return
  }

  if input.Action == "unlock_ip" {
    ip := net.ParseIP(strings.TrimSpace(input.IP))
    if ip == nil { util.Error(w, http.StatusBadRequest, fmt.Errorf("IP tidak valid")); return }
    _, _ = d1.Query(`DELETE FROM auth_attempts WHERE ip = ?`, ip.String())
    audit("member_unlock_ip", ip.String())
    util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
    return
  }

  if r.Method == http.MethodPost && input.Action == "" {
    name, err := cleanName(input.Name)
    if err != nil { util.Error(w, http.StatusBadRequest, err); return }
    if !validRole(input.Role) { util.Error(w, http.StatusBadRequest, fmt.Errorf("role tidak dikenal")); return }
    ips, err := cleanIPList(input.IPs)
    if err != nil { util.Error(w, http.StatusBadRequest, err); return }
    id, err := newID()
    if err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    token, err := newToken()
    if err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    encodedIPs, _ := json.Marshal(ips)
    if _, err := d1.Query(`INSERT INTO members (id, name, role, token_hash, token_prefix, ip_allowlist, expires_at) VALUES (?, ?, ?, ?, ?, ?, datetime('now', '+90 days'))`,
      id, name, input.Role, hashToken(token), token[:10], string(encodedIPs)); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    audit("member_create", name+" ("+input.Role+")")
    raise("waspada", "member_change", "member-create:"+id, "Member baru: "+name+" ("+input.Role+")", "Owner menambahkan member baru. Kalau bukan Anda yang menambahkannya, pilih \"Bukan saya\".")
    util.JSON(w, http.StatusOK, map[string]interface{}{"id": id, "token": token})
    return
  }

  if !validMemberID(input.ID) { util.Error(w, http.StatusBadRequest, fmt.Errorf("ID member tidak valid")); return }
  existing, ok := member(input.ID)
  if !ok { util.Error(w, http.StatusNotFound, fmt.Errorf("member tidak ditemukan")); return }
  defer forget(input.ID)

  switch input.Action {
  case "rotate":
    token, err := newToken()
    if err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    if _, err := d1.Query(`UPDATE members SET token_hash = ?, token_prefix = ?, epoch = epoch + 1, expires_at = datetime('now', '+90 days') WHERE id = ?`, hashToken(token), token[:10], input.ID); err != nil {
      util.Error(w, http.StatusBadGateway, err); return
    }
    audit("member_rotate", existing.Name)
    util.JSON(w, http.StatusOK, map[string]interface{}{"id": input.ID, "token": token})
    return
  case "revoke":
    if _, err := d1.Query(`UPDATE members SET revoked_at = CURRENT_TIMESTAMP, epoch = epoch + 1 WHERE id = ?`, input.ID); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    audit("member_revoke", existing.Name)
  case "restore":
    if _, err := d1.Query(`UPDATE members SET revoked_at = NULL WHERE id = ?`, input.ID); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    audit("member_restore", existing.Name)
  case "":
    if input.Role != "" {
      if !validRole(input.Role) { util.Error(w, http.StatusBadRequest, fmt.Errorf("role tidak dikenal")); return }
      if _, err := d1.Query(`UPDATE members SET role = ? WHERE id = ?`, input.Role, input.ID); err != nil { util.Error(w, http.StatusBadGateway, err); return }
      audit("member_role", existing.Name+" → "+input.Role)
    }
    if input.Name != "" {
      name, err := cleanName(input.Name)
      if err != nil { util.Error(w, http.StatusBadRequest, err); return }
      if _, err := d1.Query(`UPDATE members SET name = ? WHERE id = ?`, name, input.ID); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    }
    if input.SetIPs {
      ips, err := cleanIPList(input.IPs)
      if err != nil { util.Error(w, http.StatusBadRequest, err); return }
      encoded, _ := json.Marshal(ips)
      if _, err := d1.Query(`UPDATE members SET ip_allowlist = ? WHERE id = ?`, string(encoded), input.ID); err != nil { util.Error(w, http.StatusBadGateway, err); return }
      audit("member_ip", existing.Name)
    }
  default:
    util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi tidak dikenal")); return
  }
  util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}


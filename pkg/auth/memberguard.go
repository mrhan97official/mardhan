package auth

// Member rows are signed with the server secret, so a member written or
// changed straight in the database (for example with a leaked Cloudflare
// token) is refused at sign-in. Operators can be limited to chosen apps.

import (
  "crypto/hmac"
  "crypto/sha256"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "os"
  "regexp"
  "strings"
  "sync"

  "devcontrol/pkg/d1"
)

const memberMarkerKey = "members_signed"

func memberSignature(id, role, tokenHash, ipList, appScope, revokedAt, expiresAt string) string {
  mac := hmac.New(sha256.New, []byte("devcontrol/member-row/v1|"+os.Getenv("DEVCONTROL_SESSION_SECRET")))
  _, _ = mac.Write([]byte(strings.Join([]string{id, role, tokenHash, ipList, appScope, revokedAt, expiresAt}, "\x1f")))
  return hex.EncodeToString(mac.Sum(nil))
}

func memberMarker() string {
  mac := hmac.New(sha256.New, []byte("devcontrol/member-row/v1|"+os.Getenv("DEVCONTROL_SESSION_SECRET")))
  _, _ = mac.Write([]byte("members-signed"))
  return hex.EncodeToString(mac.Sum(nil))
}

func rowSignature(row map[string]interface{}) string {
  text := func(key string) string { value, _ := row[key].(string); return value }
  return memberSignature(text("id"), text("role"), text("token_hash"), text("ip_allowlist"), text("app_scope"), text("revoked_raw"), text("expires_raw"))
}

// The columns every signed read needs.
const memberColumns = `id, name, role, epoch, token_hash, ip_allowlist, app_scope, sig,
  COALESCE(revoked_at, '') AS revoked_raw, COALESCE(expires_at, '') AS expires_raw,
  CASE WHEN expires_at IS NOT NULL AND expires_at < datetime('now') THEN 'expired' ELSE COALESCE(revoked_at, '') END AS revoked_at`

var signing struct {
  sync.Mutex
  state string // "" unknown, "legacy" (not yet signed), "strict"
}

// signingMode signs every row once the first time (when no row carries a
// signature yet), then stays strict: unsigned or wrongly signed rows fail.
func signingMode() string {
  signing.Lock()
  if signing.state == "strict" { signing.Unlock(); return "strict" }
  signing.Unlock()
  marker, err := authSetting(memberMarkerKey)
  if err != nil { return "legacy" }
  if marker != "" {
    if marker != memberMarker() {
      raise("siaga", "member_signature", "member-marker-mismatch:"+marker[:shorter(12, len(marker))], "Tanda tangan member tidak cocok",
        "Penanda tanda tangan member tidak cocok dengan rahasia sesi saat ini. Bila Anda baru mengganti DEVCONTROL_SESSION_SECRET, buka Member & Akses lalu tekan Tandatangani ulang. Bila tidak, database mungkin diubah dari luar.")
    }
    setSigningState("strict")
    return "strict"
  }
  rows, err := d1.Query(`SELECT COUNT(*) AS total, SUM(CASE WHEN sig != '' THEN 1 ELSE 0 END) AS signed FROM members`)
  if err != nil || len(rows) == 0 { return "legacy" }
  if toInt(rows[0]["signed"]) > 0 {
    raise("darurat", "member_signature", "member-marker-missing", "Penanda tanda tangan member hilang",
      "Beberapa member sudah bertanda tangan tetapi penandanya hilang dari database. Kemungkinan database diubah dari luar DevControl.")
    setSigningState("strict")
    return "strict"
  }
  if err := SignAllMembers(); err != nil { return "legacy" }
  return "strict"
}

func setSigningState(value string) { signing.Lock(); signing.state = value; signing.Unlock() }

func shorter(a, b int) int { if a < b { return a }; return b }

// SignAllMembers (re)signs every row with the current secret and stores the
// marker. The owner runs it after changing DEVCONTROL_SESSION_SECRET.
func SignAllMembers() error {
  rows, err := d1.Query(`SELECT ` + memberColumns + ` FROM members LIMIT 500`)
  if err != nil { return err }
  for _, row := range rows {
    id, _ := row["id"].(string)
    if _, err := d1.Query(`UPDATE members SET sig = ? WHERE id = ?`, rowSignature(row), id); err != nil { return err }
  }
  if err := putAuthSetting(memberMarkerKey, memberMarker()); err != nil { return err }
  setSigningState("strict")
  forget("")
  return nil
}

// resignMember is called after every change DevControl makes to a member.
func resignMember(id string) {
  rows, err := d1.Query(`SELECT `+memberColumns+` FROM members WHERE id = ? LIMIT 1`, id)
  if err != nil || len(rows) == 0 { return }
  _, _ = d1.Query(`UPDATE members SET sig = ? WHERE id = ?`, rowSignature(rows[0]), id)
}

// signatureOK checks one row; a failure in strict mode raises an alert.
func signatureOK(row map[string]interface{}) bool {
  if _, has := row["sig"]; !has { return true } // columns not migrated yet
  stored, _ := row["sig"].(string)
  if hmac.Equal([]byte(stored), []byte(rowSignature(row))) { return true }
  if signingMode() != "strict" { return true }
  id, _ := row["id"].(string)
  name, _ := row["name"].(string)
  raise("darurat", "member_signature", "member-sig:"+id+":"+stored[:shorter(12, len(stored))], "Baris member tidak sah: "+name,
    "Data member ini (role, token, IP, atau cakupan aplikasi) tidak cocok dengan tanda tangannya, jadi login-nya ditolak. Kemungkinan database diubah dari luar DevControl.")
  return false
}

// ---------- operator app scope ----------

var appNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

func parseScope(raw string) []string {
  raw = strings.TrimSpace(raw)
  if raw == "" || raw == "*" { return nil }
  var apps []string
  if json.Unmarshal([]byte(raw), &apps) != nil { return []string{} }
  return apps
}

func cleanScope(all bool, apps []string) (string, error) {
  if all { return "*", nil }
  if len(apps) > 100 { return "", fmt.Errorf("maksimal 100 aplikasi") }
  seen := map[string]bool{}
  list := []string{}
  for _, app := range apps {
    app = strings.TrimSpace(app)
    if app == "" { continue }
    if !appNamePattern.MatchString(app) { return "", fmt.Errorf("nama aplikasi %q tidak valid", app) }
    if !seen[strings.ToLower(app)] { seen[strings.ToLower(app)] = true; list = append(list, app) }
  }
  encoded, _ := json.Marshal(list)
  return string(encoded), nil
}

// CanUseApp: owner/admin reach every app; an operator limited to chosen apps
// reaches only those (nil list = every app).
func CanUseApp(p *Principal, app string) bool {
  if p == nil { return false }
  if p.Role == RoleOwner || p.Role == RoleAdmin || p.Apps == nil { return true }
  for _, allowed := range p.Apps { if strings.EqualFold(allowed, app) { return true } }
  return false
}

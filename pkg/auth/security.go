package auth

// Step-up confirmation, server-side logout and owner two-factor sign-in.

import (
  "crypto/hmac"
  "crypto/rand"
  "crypto/sha1"
  "crypto/subtle"
  "encoding/base32"
  "encoding/binary"
  "encoding/json"
  "fmt"
  "net/http"
  "net/url"
  "os"
  "strconv"
  "strings"
  "sync"
  "time"

  "devcontrol/pkg/d1"
  "devcontrol/pkg/util"
)

// confirmWindow: how long a typed password/token (and 2FA code) keeps
// dangerous actions (Update Diri, secrets, members, ZIP, delete) unlocked.
const confirmWindow = 15 * time.Minute

// RecentlyConfirmed reports whether the signed-in person typed their
// credential within confirmWindow.
func RecentlyConfirmed(r *http.Request) bool {
  if Current(r) == nil { return false }
  c, ok := readClaims(r)
  return ok && c.Auth > 0 && time.Now().Unix()-c.Auth <= int64(confirmWindow.Seconds())
}

// ReauthRequired tells the app to ask for the credential again. The
// browser shows a confirmation dialog when it sees "reauth": true.
func ReauthRequired(w http.ResponseWriter) {
  util.JSON(w, http.StatusForbidden, map[string]interface{}{
    "error":  "Konfirmasi ulang diperlukan untuk aksi ini. Masukkan kata sandi/token Anda, lalu ulangi aksinya.",
    "reauth": true,
  })
}

// ---------- server-side logout ----------

var revocations struct {
  sync.Mutex
  seen map[string]revocationState
}

type revocationState struct {
  revoked bool
  at      time.Time
}

// isRevoked is checked for every signed-in request (cached briefly). A D1
// error other than a missing table refuses the cookie.
func isRevoked(nonce string) bool {
  if nonce == "" { return false }
  revocations.Lock()
  if state, ok := revocations.seen[nonce]; ok && (state.revoked || time.Since(state.at) < cacheTTL) { revocations.Unlock(); return state.revoked }
  revocations.Unlock()
  rows, err := d1.Query(`SELECT 1 AS found FROM revoked_sessions WHERE nonce = ? LIMIT 1`, nonce)
  revoked := false
  if err != nil {
    if !missingTable(err) { return true }
  } else {
    revoked = len(rows) > 0
  }
  revocations.Lock()
  if revocations.seen == nil || len(revocations.seen) > 5000 { revocations.seen = map[string]revocationState{} }
  revocations.seen[nonce] = revocationState{revoked: revoked, at: time.Now()}
  revocations.Unlock()
  return revoked
}

func revokeClaims(c claims) {
  if c.Nonce == "" { return }
  expires := time.Unix(c.Exp, 0).UTC().Format("2006-01-02 15:04:05")
  _, _ = d1.Query(`DELETE FROM user_sessions WHERE nonce = ?`, c.Nonce)
  _, _ = d1.Query(`INSERT INTO revoked_sessions (nonce, expires_at) VALUES (?, ?) ON CONFLICT(nonce) DO NOTHING`, c.Nonce, expires)
  _, _ = d1.Query(`DELETE FROM revoked_sessions WHERE expires_at < datetime('now')`)
  revocations.Lock()
  if revocations.seen == nil { revocations.seen = map[string]revocationState{} }
  revocations.seen[c.Nonce] = revocationState{revoked: true, at: time.Now()}
  revocations.Unlock()
}

// ---------- owner 2FA (RFC 6238 TOTP: HMAC-SHA1, 30 s, 6 digits) ----------

const (
  totpKey        = "owner_totp_secret"
  totpPendingKey = "owner_totp_pending"
  totpLastKey    = "owner_totp_last"
)

var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func authSetting(key string) (string, error) {
  rows, err := d1.Query(`SELECT value FROM auth_settings WHERE key = ? LIMIT 1`, key)
  if err != nil {
    if missingTable(err) { return "", nil }
    return "", err
  }
  if len(rows) == 0 { return "", nil }
  value, _ := rows[0]["value"].(string)
  return value, nil
}

func putAuthSetting(key, value string) error {
  _, err := d1.Query(`INSERT INTO auth_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
  return err
}

func deleteAuthSetting(key string) error {
  _, err := d1.Query(`DELETE FROM auth_settings WHERE key = ?`, key)
  return err
}

// totpResetActive: emergency switch for a lost phone. Setting
// DEVCONTROL_TOTP_RESET=1 in Vercel (and redeploying) turns 2FA off until the
// variable is removed; only someone with access to Vercel can do this.
func totpResetActive() bool { return strings.TrimSpace(os.Getenv("DEVCONTROL_TOTP_RESET")) == "1" }

// ownerTOTPSecret returns the opened 2FA secret ("" = 2FA off). With
// DEVCONTROL_REQUIRE_2FA=1 a missing secret is an error, so deleting the row
// straight from the database cannot switch 2FA off silently.
func ownerTOTPSecret() (string, error) {
  if totpResetActive() { return "", nil }
  stored, err := authSetting(totpKey)
  if err != nil { return "", err }
  if stored == "" {
    if require2FA() {
      raise("darurat", "totp_missing", "totp-missing:"+time.Now().UTC().Format("2006-01-02"), "Rahasia 2FA owner hilang dari database",
        "DEVCONTROL_REQUIRE_2FA=1 aktif tetapi rahasia 2FA tidak ada. Kemungkinan database diubah dari luar DevControl.")
      return "", fmt.Errorf("2FA diwajibkan (DEVCONTROL_REQUIRE_2FA=1) tetapi rahasia 2FA tidak ditemukan di database; login owner ditahan. Periksa database, atau isi DEVCONTROL_TOTP_RESET=1 di Vercel untuk memulihkan")
    }
    return "", nil
  }
  return openTOTP(stored)
}

// OwnerTwoFactorEnabled is used by the app audit.
func OwnerTwoFactorEnabled() (bool, error) {
  secret, err := ownerTOTPSecret()
  return secret != "", err
}

func totpCode(secret []byte, counter int64) string {
  var message [8]byte
  binary.BigEndian.PutUint64(message[:], uint64(counter))
  mac := hmac.New(sha1.New, secret)
  _, _ = mac.Write(message[:])
  sum := mac.Sum(nil)
  offset := sum[len(sum)-1] & 0x0f
  value := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
  return fmt.Sprintf("%06d", value%1000000)
}

// verifyTOTP accepts the current 30-second step ±1. With remember=true a
// step that was already used is refused, so a code cannot be replayed.
func verifyTOTP(secretText, code string, remember bool) (bool, error) {
  code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
  if len(code) != 6 || strings.Trim(code, "0123456789") != "" { return false, nil }
  secret, err := totpBase32.DecodeString(strings.ToUpper(strings.TrimSpace(secretText)))
  if err != nil || len(secret) == 0 { return false, fmt.Errorf("rahasia 2FA rusak") }
  last := int64(-1)
  if remember {
    text, readErr := authSetting(totpLastKey)
    if readErr != nil { return false, readErr }
    if text != "" { if parsed, parseErr := strconv.ParseInt(text, 10, 64); parseErr == nil { last = parsed } }
  }
  now := time.Now().Unix() / 30
  for _, step := range []int64{now - 1, now, now + 1} {
    if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) != 1 { continue }
    if remember {
      if step <= last { return false, nil }
      if err := putAuthSetting(totpLastKey, strconv.FormatInt(step, 10)); err != nil { return false, err }
    }
    return true, nil
  }
  return false, nil
}

// HandleTwoFactor serves /api/two-factor for the owner:
// GET status; POST {action: begin|enable|disable, code}.
func HandleTwoFactor(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Cache-Control", "no-store")
  if !IsOwner(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("2FA hanya dapat diatur oleh owner")); return }
  switch r.Method {
  case http.MethodGet:
    stored, err := authSetting(totpKey)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    util.JSON(w, http.StatusOK, map[string]interface{}{"enabled": stored != "" && !totpResetActive(), "reset_active": totpResetActive() && stored != ""})
    return
  case http.MethodPost:
  default:
    util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST")); return
  }
  var input struct {
    Action string `json:"action"`
    Code   string `json:"code"`
  }
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
    util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan 2FA tidak valid")); return
  }
  if !RecentlyConfirmed(r) { ReauthRequired(w); return }
  switch input.Action {
  case "begin":
    raw := make([]byte, 20)
    if _, err := rand.Read(raw); err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    secret := totpBase32.EncodeToString(raw)
    sealed, err := sealTOTP(secret)
    if err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    if err := putAuthSetting(totpPendingKey, sealed); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    params := url.Values{"secret": {secret}, "issuer": {"DevControl"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
    util.JSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": "otpauth://totp/" + url.PathEscape("DevControl:owner") + "?" + params.Encode()})
  case "enable":
    stored, err := authSetting(totpPendingKey)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    pending, err := openTOTP(stored)
    if err != nil || pending == "" { util.Error(w, http.StatusBadRequest, fmt.Errorf("mulai ulang pengaturan 2FA")); return }
    ok, verifyErr := verifyTOTP(pending, input.Code, false)
    if verifyErr != nil { util.Error(w, http.StatusBadGateway, verifyErr); return }
    if !ok { util.Error(w, http.StatusBadRequest, fmt.Errorf("kode tidak cocok; pastikan jam ponsel tepat lalu coba kode terbaru")); return }
    sealed, err := sealTOTP(pending)
    if err != nil { util.Error(w, http.StatusInternalServerError, err); return }
    if err := putAuthSetting(totpKey, sealed); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    _ = deleteAuthSetting(totpPendingKey)
    _ = deleteAuthSetting(totpLastKey)
    audit("totp_enabled", "owner")
    raise("waspada", "totp_change", "totp-enabled:"+time.Now().UTC().Format("2006-01-02T15:04"), "2FA owner diaktifkan", "2FA owner baru saja diaktifkan. Kalau bukan Anda, segera periksa Riwayat Keamanan.")
    util.JSON(w, http.StatusOK, map[string]bool{"enabled": true})
  case "disable":
    stored, err := authSetting(totpKey)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    secret, openErr := openTOTP(stored)
    if openErr != nil { util.Error(w, http.StatusBadGateway, openErr); return }
    if secret != "" && !totpResetActive() {
      ok, verifyErr := verifyTOTP(secret, input.Code, true)
      if verifyErr != nil { util.Error(w, http.StatusBadGateway, verifyErr); return }
      if !ok { util.Error(w, http.StatusBadRequest, fmt.Errorf("kode 2FA salah")); return }
    }
    _ = deleteAuthSetting(totpKey)
    _ = deleteAuthSetting(totpLastKey)
    _ = deleteAuthSetting(totpPendingKey)
    audit("totp_disabled", "owner")
    raise("siaga", "totp_change", "totp-disabled:"+time.Now().UTC().Format("2006-01-02T15:04"), "2FA owner dimatikan", "2FA owner baru saja dimatikan. Kalau bukan Anda, anggap akun owner sudah diambil alih.")
    util.JSON(w, http.StatusOK, map[string]bool{"enabled": false})
  default:
    util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi 2FA tidak dikenal"))
  }
}

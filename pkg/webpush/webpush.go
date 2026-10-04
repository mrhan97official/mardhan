// Package webpush delivers DevControl notifications to devices even when the
// app is closed, using the browser Web Push standard (RFC 8030/8291/8292).
// No third-party service or extra Vercel variable is needed: the VAPID key
// pair is generated once and kept in D1, unless VAPID_PUBLIC_KEY and
// VAPID_PRIVATE_KEY are set in Vercel.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/deploymentrunner"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
)

// Event kinds a device can switch on or off.
const (
	EventDeploy     = "deploy"      // Aplikasi Baru / Update Aplikasi
	EventSelfUpdate = "self_update" // Update Diri
	EventConfirm    = "confirm"     // confirmations waiting for an admin
	EventApp404     = "app404"      // an application link shows Vercel's 404
	EventSecurity   = "security"    // login abuse and new high-risk audit findings
)

var Events = []string{EventDeploy, EventSelfUpdate, EventConfirm, EventApp404, EventSecurity}

// Confirmations can only be answered by owner/admin.
var adminOnly = map[string]bool{EventConfirm: true, EventSecurity: true}

// Message is one notification. Key de-duplicates: a message whose key was
// already sent is skipped, so retries never notify twice.
type Message struct {
	Event string
	Key   string
	Title string
	Body  string
	URL   string
	Tag   string
}

var b64 = base64.RawURLEncoding

func decodeB64(value string) ([]byte, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "=")
	value = strings.NewReplacer("+", "-", "/", "_").Replace(value)
	return b64.DecodeString(value)
}

func validEvent(event string) bool {
	for _, known := range Events { if event == known { return true } }
	return false
}

// ---------- VAPID keys ----------

type vapid struct {
	publicText string
	private    *ecdsa.PrivateKey
	subject    string
}

func parseKeys(publicText, privateText string) (*vapid, error) {
	publicBytes, err := decodeB64(publicText)
	if err != nil || len(publicBytes) != 65 || publicBytes[0] != 4 { return nil, fmt.Errorf("kunci publik VAPID tidak valid") }
	privateBytes, err := decodeB64(privateText)
	if err != nil || len(privateBytes) != 32 { return nil, fmt.Errorf("kunci privat VAPID tidak valid") }
	check, err := ecdh.P256().NewPrivateKey(privateBytes)
	if err != nil || !bytes.Equal(check.PublicKey().Bytes(), publicBytes) {
		return nil, fmt.Errorf("pasangan kunci VAPID tidak cocok")
	}
	key := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(publicBytes[1:33]), Y: new(big.Int).SetBytes(publicBytes[33:])},
		D:         new(big.Int).SetBytes(privateBytes),
	}
	return &vapid{publicText: b64.EncodeToString(publicBytes), private: key}, nil
}

func subjectFor(stored string) string {
	if host := strings.TrimSpace(os.Getenv("VERCEL_PROJECT_PRODUCTION_URL")); host != "" && !strings.ContainsAny(host, "/:@?# ") {
		return "https://" + host
	}
	if strings.HasPrefix(stored, "https://") { return stored }
	return "mailto:devcontrol@users.noreply.github.com"
}

// loadKeys returns the key pair, creating and storing one on first use.
func loadKeys() (*vapid, error) {
	rows, err := d1.Query(`SELECT public_key, private_key, subject FROM push_config WHERE id = 1 LIMIT 1`)
	if err != nil { return nil, err }
	stored := ""
	if len(rows) == 1 { stored, _ = rows[0]["subject"].(string) }
	if public, private := strings.TrimSpace(os.Getenv("VAPID_PUBLIC_KEY")), strings.TrimSpace(os.Getenv("VAPID_PRIVATE_KEY")); public != "" && private != "" {
		keys, err := parseKeys(public, private)
		if err != nil { return nil, fmt.Errorf("VAPID_PUBLIC_KEY/VAPID_PRIVATE_KEY di Vercel: %w", err) }
		if len(rows) == 0 {
			_, _ = d1.Query(`INSERT OR IGNORE INTO push_config (id, public_key, private_key) VALUES (1, '', '')`)
		}
		keys.subject = subjectFor(stored)
		return keys, nil
	}
	if len(rows) == 0 || rows[0]["public_key"] == "" {
		generated, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil { return nil, err }
		public, private := b64.EncodeToString(generated.PublicKey().Bytes()), b64.EncodeToString(generated.Bytes())
		// Two first requests may race; the stored pair always wins.
		if _, err := d1.Query(`INSERT INTO push_config (id, public_key, private_key) VALUES (1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET public_key = excluded.public_key, private_key = excluded.private_key
			WHERE push_config.public_key = ''`, public, private); err != nil { return nil, err }
		rows, err = d1.Query(`SELECT public_key, private_key, subject FROM push_config WHERE id = 1 LIMIT 1`)
		if err != nil || len(rows) == 0 { return nil, fmt.Errorf("kunci notifikasi belum tersimpan: %v", err) }
	}
	public, _ := rows[0]["public_key"].(string)
	private, _ := rows[0]["private_key"].(string)
	keys, err := parseKeys(public, private)
	if err != nil { return nil, err }
	keys.subject = subjectFor(stored)
	return keys, nil
}

func (k *vapid) token(audience string) (string, error) {
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]interface{}{"aud": audience, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": k.subject})
	if err != nil { return "", err }
	unsigned := header + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	if err != nil { return "", err }
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + b64.EncodeToString(signature), nil
}

// ---------- RFC 8291 payload encryption (aes128gcm) ----------

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func encrypt(userPublic, authSecret, payload []byte) ([]byte, error) {
	curve := ecdh.P256()
	receiver, err := curve.NewPublicKey(userPublic)
	if err != nil { return nil, fmt.Errorf("kunci perangkat tidak valid") }
	sender, err := curve.GenerateKey(rand.Reader)
	if err != nil { return nil, err }
	senderPublic := sender.PublicKey().Bytes()
	shared, err := sender.ECDH(receiver)
	if err != nil { return nil, err }

	keyInfo := make([]byte, 0, 14+65+65+1)
	keyInfo = append(keyInfo, []byte("WebPush: info\x00")...)
	keyInfo = append(keyInfo, userPublic...)
	keyInfo = append(keyInfo, senderPublic...)
	keyInfo = append(keyInfo, 1)
	ikm := hmacSHA256(hmacSHA256(authSecret, shared), keyInfo)

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil { return nil, err }
	prk := hmacSHA256(salt, ikm)
	cek := hmacSHA256(prk, []byte("Content-Encoding: aes128gcm\x00\x01"))[:16]
	nonce := hmacSHA256(prk, []byte("Content-Encoding: nonce\x00\x01"))[:12]

	block, err := aes.NewCipher(cek)
	if err != nil { return nil, err }
	gcm, err := cipher.NewGCM(block)
	if err != nil { return nil, err }
	plaintext := append(append(make([]byte, 0, len(payload)+1), payload...), 2) // last-record delimiter
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	body := make([]byte, 0, 16+4+1+len(senderPublic)+len(ciphertext))
	body = append(body, salt...)
	recordSize := make([]byte, 4)
	binary.BigEndian.PutUint32(recordSize, 4096)
	body = append(body, recordSize...)
	body = append(body, byte(len(senderPublic)))
	body = append(body, senderPublic...)
	return append(body, ciphertext...), nil
}

// ---------- subscriptions ----------

// Push endpoints are issued by browser vendors. Anything else is refused so
// the server never posts to arbitrary hosts on a member's behalf.
var pushHostSuffixes = []string{".googleapis.com", ".mozilla.com", ".push.apple.com", ".notify.windows.com"}

func validEndpoint(raw string) bool {
	if len(raw) > 1024 { return false }
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" { return false }
	host := strings.ToLower(parsed.Hostname())
	for _, suffix := range pushHostSuffixes { if strings.HasSuffix(host, suffix) { return true } }
	return false
}

type subscription struct {
	endpoint, p256dh, auth, subject, role string
	events map[string]bool
}

func splitEvents(value string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.Split(value, ",") { if validEvent(item) { out[item] = true } }
	return out
}

func joinEvents(events []string, admin bool) string {
	chosen := make([]string, 0, len(Events))
	for _, event := range Events {
		if adminOnly[event] && !admin { continue }
		for _, item := range events { if item == event { chosen = append(chosen, event); break } }
	}
	return strings.Join(chosen, ",")
}

func loadSubscriptions(where string, params ...interface{}) ([]subscription, error) {
	rows, err := d1.Query(`SELECT s.endpoint, s.p256dh, s.auth, s.subject, s.events,
		CASE WHEN s.subject = 'owner' THEN 'owner'
			WHEN m.id IS NOT NULL AND m.revoked_at IS NULL THEN m.role ELSE '' END AS role
		FROM push_subscriptions s LEFT JOIN members m ON m.id = s.subject `+where, params...)
	if err != nil { return nil, err }
	out := make([]subscription, 0, len(rows))
	for _, row := range rows {
		text := func(key string) string { value, _ := row[key].(string); return value }
		item := subscription{endpoint: text("endpoint"), p256dh: text("p256dh"), auth: text("auth"),
			subject: text("subject"), role: text("role"), events: splitEvents(text("events"))}
		// A removed or revoked member no longer receives anything.
		if item.role == "" { _, _ = d1.Query(`DELETE FROM push_subscriptions WHERE endpoint = ?`, item.endpoint); continue }
		out = append(out, item)
	}
	return out, nil
}

func isAdmin(role string) bool { return role == auth.RoleOwner || role == auth.RoleAdmin }

// ---------- delivery ----------

type payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag,omitempty"`
}

func trim(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= limit { return value }
	return string([]rune(value)[:limit-1]) + "…"
}

// transientError marks a delivery failure that is worth one more try: a
// network problem or a push service that answered 5xx/429.
type transientError struct{ error }

func (e transientError) Unwrap() error { return e.error }

func deliver(ctx context.Context, keys *vapid, target subscription, message Message) error {
	parsed, err := url.Parse(target.endpoint)
	if err != nil || !validEndpoint(target.endpoint) { return fmt.Errorf("alamat push tidak valid") }
	userPublic, err := decodeB64(target.p256dh)
	if err != nil { return err }
	authSecret, err := decodeB64(target.auth)
	if err != nil { return err }
	destination := message.URL
	if !strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "//") { destination = "/" }
	content, err := json.Marshal(payload{Title: trim(message.Title, 90), Body: trim(message.Body, 280), URL: destination, Tag: trim(message.Tag, 60)})
	if err != nil { return err }
	body, err := encrypt(userPublic, authSecret, content)
	if err != nil { return err }
	token, err := keys.token(parsed.Scheme + "://" + parsed.Host)
	if err != nil { return err }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.endpoint, bytes.NewReader(body))
	if err != nil { return err }
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "vapid t="+token+", k="+keys.publicText)
	resp, err := httpClient.Do(req)
	if err != nil { return transientError{err} }
	defer resp.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// The browser dropped this subscription; forget it.
		_, _ = d1.Query(`DELETE FROM push_subscriptions WHERE endpoint = ?`, target.endpoint)
		return fmt.Errorf("langganan perangkat sudah tidak berlaku (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 300:
		failure := fmt.Errorf("layanan push menolak (HTTP %d) %s", resp.StatusCode, strings.TrimSpace(string(detail)))
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests { return transientError{failure} }
		return failure
	}
	return nil
}

var httpClient = &http.Client{Timeout: 8 * time.Second}

// sendAll delivers to every target and returns how many accepted the message.
// A transient failure (network error, push service 5xx/429) is retried once.
func sendAll(targets []subscription, message Message) int {
	if len(targets) == 0 { return 0 }
	keys, err := loadKeys()
	if err != nil { return 0 }
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	var delivered int32
	var wait sync.WaitGroup
	for _, target := range targets {
		wait.Add(1)
		go func(target subscription) {
			defer wait.Done()
			err := deliver(ctx, keys, target, message)
			var transient transientError
			if err != nil && errors.As(err, &transient) {
				select {
				case <-time.After(1500 * time.Millisecond):
					err = deliver(ctx, keys, target, message)
				case <-ctx.Done():
				}
			}
			errText := ""
			if err != nil { errText = trim(err.Error(), 300) } else { atomic.AddInt32(&delivered, 1) }
			_, _ = d1.Query(`UPDATE push_subscriptions SET last_error = ? WHERE endpoint = ?`, errText, target.endpoint)
		}(target)
	}
	wait.Wait()
	return int(atomic.LoadInt32(&delivered))
}

// Notify sends a message to every device that enabled its event. It never
// fails the caller: notifications are best effort.
//
// The de-duplication key is kept once a device accepted the message, or when
// no device wants it. If devices exist but none accepted it (push service
// down, VAPID keys unreadable, D1 hiccup) the key is released, so the retry
// in /api/push-watch can deliver it later instead of losing it for good.
func Notify(message Message) {
	if !validEvent(message.Event) { return }
	key := trim(message.Key, 400)
	if message.Key != "" {
		rows, err := d1.Query(`INSERT INTO push_log (event_key) VALUES (?) ON CONFLICT(event_key) DO NOTHING RETURNING event_key`, key)
		if err != nil || len(rows) == 0 { return }
	}
	release := func() { if message.Key != "" { Forget(key) } }
	subs, err := loadSubscriptions(`WHERE (',' || s.events || ',') LIKE ?`, "%,"+message.Event+",%")
	if err != nil { release(); return }
	targets := make([]subscription, 0, len(subs))
	for _, item := range subs {
		if !item.events[message.Event] || (adminOnly[message.Event] && !isAdmin(item.role)) { continue }
		targets = append(targets, item)
	}
	if len(targets) == 0 { return }
	if sendAll(targets, message) == 0 { release() }
}

// Forget clears a de-duplication key, e.g. once a 404 application is healthy
// again so a later outage notifies again.
func Forget(key string) { _, _ = d1.Query(`DELETE FROM push_log WHERE event_key = ?`, key) }

// Wants reports whether any device enabled an event (skips useless checks).
func Wants(event string) bool {
	rows, err := d1.Query(`SELECT 1 AS found FROM push_subscriptions WHERE (',' || events || ',') LIKE ? LIMIT 1`, "%,"+event+",%")
	return err == nil && len(rows) > 0
}

// Prune keeps the de-duplication log small.
func Prune() { _, _ = d1.Query(`DELETE FROM push_log WHERE created_at < datetime('now', '-30 days')`) }

// watchKey is a push_log row whose created_at records when the scheduled
// Worker last called /api/push-watch (no schema change needed).
const watchKey = "watch:last"

// MarkWatch records that the scheduled Worker just ran its periodic upkeep.
func MarkWatch() {
	_, _ = d1.Query(`INSERT INTO push_log (event_key, created_at) VALUES (?, CURRENT_TIMESTAMP)
		ON CONFLICT(event_key) DO UPDATE SET created_at = CURRENT_TIMESTAMP`, watchKey)
}

// WatchAge returns the seconds since MarkWatch, or -1 when it never ran.
func WatchAge() int64 {
	rows, err := d1.Query(`SELECT CAST(strftime('%s', 'now') AS INTEGER) - CAST(strftime('%s', created_at) AS INTEGER) AS age
		FROM push_log WHERE event_key = ? LIMIT 1`, watchKey)
	if err != nil || len(rows) == 0 { return -1 }
	switch age := rows[0]["age"].(type) {
	case float64:
		if age < 0 { return 0 }
		return int64(age)
	case int64:
		return age
	}
	return -1
}

// RunnerVersion and SetRunnerVersion remember which scheduled Worker source
// is installed, so a new version is installed once after an update.
func RunnerVersion() string {
	rows, err := d1.Query(`SELECT runner_version FROM push_config WHERE id = 1 LIMIT 1`)
	if err != nil || len(rows) == 0 { return "" }
	value, _ := rows[0]["runner_version"].(string)
	return value
}

func SetRunnerVersion(version string) {
	_, _ = d1.Query(`UPDATE push_config SET runner_version = ? WHERE id = 1`, version)
}

// ---------- HTTP: /api/push ----------

// Handle serves every signed-in role. ensureRunner installs the current
// scheduled Worker (used for 404 and confirmation checks) when needed.
func Handle(w http.ResponseWriter, r *http.Request, ensureRunner func(*http.Request, bool) error) {
	w.Header().Set("Cache-Control", "no-store")
	principal := auth.Current(r)
	if principal == nil { util.Error(w, http.StatusUnauthorized, fmt.Errorf("login diperlukan")); return }
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	admin := isAdmin(principal.Role)
	available := Events
	if !admin { available = []string{EventDeploy, EventSelfUpdate, EventApp404} }

	if r.Method == http.MethodGet {
		keys, err := loadKeys()
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]interface{}{"public_key": keys.publicText, "events": available})
		return
	}
	if r.Method != http.MethodPost { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST")); return }
	var input struct {
		Action   string   `json:"action"`
		Endpoint string   `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan notifikasi tidak valid")); return
	}
	if !validEndpoint(input.Endpoint) {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("browser ini memberikan alamat push yang tidak dikenal")); return
	}
	own := func() ([]subscription, error) {
		return loadSubscriptions(`WHERE s.endpoint = ? AND s.subject = ?`, input.Endpoint, principal.Subject)
	}

	switch input.Action {
	case "subscribe":
		userPublic, err := decodeB64(input.Keys.P256dh)
		if err != nil || len(userPublic) != 65 || userPublic[0] != 4 { util.Error(w, http.StatusBadRequest, fmt.Errorf("kunci perangkat tidak valid")); return }
		if _, err := ecdh.P256().NewPublicKey(userPublic); err != nil { util.Error(w, http.StatusBadRequest, fmt.Errorf("kunci perangkat tidak valid")); return }
		secret, err := decodeB64(input.Keys.Auth)
		if err != nil || len(secret) != 16 { util.Error(w, http.StatusBadRequest, fmt.Errorf("kunci autentikasi perangkat tidak valid")); return }
		if _, err := loadKeys(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		events := joinEvents(input.Events, admin)
		if _, err := d1.Query(`INSERT INTO push_subscriptions (endpoint, p256dh, auth, subject, events) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, subject = excluded.subject,
			events = excluded.events, last_error = '', updated_at = CURRENT_TIMESTAMP`,
			input.Endpoint, b64.EncodeToString(userPublic), b64.EncodeToString(secret), principal.Subject, events); err != nil {
			util.Error(w, http.StatusBadGateway, err); return
		}
		if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "https://") && !strings.ContainsAny(strings.TrimPrefix(origin, "https://"), "/@?# ") {
			_, _ = d1.Query(`UPDATE push_config SET subject = ? WHERE id = 1 AND subject = ''`, origin)
		}
		if ensureRunner != nil { _ = ensureRunner(r, false) }
		chosen := []string{}
		if events != "" { chosen = strings.Split(events, ",") }
		util.JSON(w, http.StatusOK, map[string]interface{}{"subscribed": true, "events": chosen})
	case "status":
		items, err := own()
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(items) == 0 { util.JSON(w, http.StatusOK, map[string]interface{}{"subscribed": false}); return }
		events := make([]string, 0, len(Events))
		for _, event := range available { if items[0].events[event] { events = append(events, event) } }
		rows, _ := d1.Query(`SELECT last_error FROM push_subscriptions WHERE endpoint = ? LIMIT 1`, input.Endpoint)
		lastError := ""
		if len(rows) == 1 { lastError, _ = rows[0]["last_error"].(string) }
		reply := map[string]interface{}{"subscribed": true, "events": events, "last_error": lastError}
		if admin {
			// Scheduler health: is the installed Worker current, and did it call us recently?
			reply["runner"] = map[string]interface{}{"current": RunnerVersion() == deploymentrunner.Version(), "watch_age": WatchAge()}
		}
		util.JSON(w, http.StatusOK, reply)
	case "runner":
		if !admin { util.Error(w, http.StatusForbidden, fmt.Errorf("hanya owner/admin yang dapat memasang ulang penjadwal")); return }
		if ensureRunner == nil { util.Error(w, http.StatusInternalServerError, fmt.Errorf("pemasang penjadwal tidak tersedia")); return }
		if err := ensureRunner(r, true); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]bool{"installed": true})
	case "unsubscribe":
		if _, err := d1.Query(`DELETE FROM push_subscriptions WHERE endpoint = ?`, input.Endpoint); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]bool{"subscribed": false})
	case "test":
		items, err := own()
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(items) == 0 { util.Error(w, http.StatusNotFound, fmt.Errorf("perangkat ini belum mengaktifkan notifikasi")); return }
		keys, err := loadKeys()
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := deliver(ctx, keys, items[0], Message{Title: "DevControl", Body: "Notifikasi uji berhasil. Pemberitahuan tetap masuk walaupun aplikasi ditutup.", URL: "/settings", Tag: "devcontrol-test"}); err != nil {
			util.Error(w, http.StatusBadGateway, err); return
		}
		util.JSON(w, http.StatusOK, map[string]bool{"sent": true})
	default:
		util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi notifikasi tidak valid"))
	}
}

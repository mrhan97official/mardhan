// Package securitycenter is DevControl's "Pusat Keamanan": the alert list
// behind the Siaga pop-up, a patrol that compares the live state with the
// last approved baseline every 15 minutes, Mode Darurat with the "kunci dari
// luar" checklist, and a signed heartbeat for an independent watchdog.
package securitycenter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/util"
	"devcontrol/pkg/webpush"
)

var levelIcon = map[string]string{"waspada": "🟡", "siaga": "🟠", "darurat": "🔴"}

func text(row map[string]interface{}, key string) string { value, _ := row[key].(string); return value }

func number(value interface{}) int64 {
	switch n := value.(type) {
	case float64: return int64(n)
	case int64: return n
	case int: return int64(n)
	case string: parsed, _ := strconv.ParseInt(n, 10, 64); return parsed
	}
	return 0
}

// ---------- alerts ----------

// Raise stores an alert once per key and sends it to owner/admin devices
// (push) and, when configured, to Telegram — a channel outside DevControl.
func Raise(level, kind, key, title, detail string) {
	if levelIcon[level] == "" { level = "waspada" }
	if len(title) > 200 { title = title[:200] }
	if len(detail) > 1500 { detail = detail[:1500] }
	rows, err := d1.Query(`INSERT INTO security_alerts (alert_key, level, kind, title, detail) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(alert_key) DO NOTHING RETURNING id`, key, level, kind, title, detail)
	if err != nil || len(rows) == 0 { return }
	webpush.Notify(webpush.Message{Event: webpush.EventSecurity, Key: "sec:" + key, URL: "/security", Tag: "security-" + level,
		Title: levelIcon[level] + " " + strings.ToUpper(level) + ": " + title, Body: detail})
	telegram(levelIcon[level] + " DevControl · " + strings.ToUpper(level) + "\n" + title + "\n\n" + detail)
}

func telegramConfigured() bool {
	return strings.TrimSpace(os.Getenv("DEVCONTROL_TELEGRAM_BOT_TOKEN")) != "" && strings.TrimSpace(os.Getenv("DEVCONTROL_TELEGRAM_CHAT_ID")) != ""
}

// telegram copies an alert outside DevControl, so it survives even if the
// database or the app itself is later tampered with.
func telegram(message string) error {
	token, chat := strings.TrimSpace(os.Getenv("DEVCONTROL_TELEGRAM_BOT_TOKEN")), strings.TrimSpace(os.Getenv("DEVCONTROL_TELEGRAM_CHAT_ID"))
	if token == "" || chat == "" { return fmt.Errorf("DEVCONTROL_TELEGRAM_BOT_TOKEN dan DEVCONTROL_TELEGRAM_CHAT_ID belum diisi") }
	payload, _ := json.Marshal(map[string]interface{}{"chat_id": chat, "text": message, "disable_web_page_preview": true})
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", bytes.NewReader(payload))
	if err != nil { return err }
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil { return fmt.Errorf("Telegram tidak dapat dihubungi") }
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK { return fmt.Errorf("Telegram menjawab HTTP %d; periksa token bot dan chat ID", resp.StatusCode) }
	return nil
}

// ---------- baselines ----------

func state(key string) string {
	rows, err := d1.Query(`SELECT value FROM security_state WHERE key = ? LIMIT 1`, key)
	if err != nil || len(rows) == 0 { return "" }
	return text(rows[0], "value")
}

func setState(key, value string) {
	_, _ = d1.Query(`INSERT INTO security_state (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value)
}

func fingerprint(value string) string {
	if value == "" { return "" }
	sum := sha256.Sum256([]byte("devcontrol-fp|" + value))
	return hex.EncodeToString(sum[:6])
}

type secretItem struct {
	Env   string
	Label string
	Where string
	Link  string
}

// The things to replace from outside DevControl after a break-in.
var outsideChecklist = []secretItem{
	{"DEVCONTROL_ADMIN_PASSWORD", "Kata sandi admin", "Vercel → project DevControl → Settings → Environment Variables", "https://vercel.com/dashboard"},
	{"DEVCONTROL_SESSION_SECRET", "Rahasia sesi (64 karakter acak)", "Vercel → project DevControl → Settings → Environment Variables", "https://vercel.com/dashboard"},
	{"GITHUB_TOKEN", "Token GitHub", "GitHub → Settings → Developer settings → Personal access tokens (cabut yang lama)", "https://github.com/settings/personal-access-tokens"},
	{"VERCEL_TOKEN", "Token Vercel", "Vercel → Account Settings → Tokens (hapus yang lama)", "https://vercel.com/account/settings/tokens"},
	{"CF_API_TOKEN", "Token Cloudflare", "Cloudflare → My Profile → API Tokens (roll/cabut yang lama)", "https://dash.cloudflare.com/profile/api-tokens"},
}

func checklist() []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(outsideChecklist))
	for _, item := range outsideChecklist {
		before := state("fp:" + item.Env)
		current := fingerprint(os.Getenv(item.Env))
		items = append(items, map[string]interface{}{"env": item.Env, "label": item.Label, "where": item.Where, "link": item.Link,
			"changed": before != "" && current != "" && before != current, "missing": current == ""})
	}
	return items
}

// ---------- API ----------

func reauth(w http.ResponseWriter, r *http.Request) bool {
	if auth.RecentlyConfirmed(r) { return true }
	auth.ReauthRequired(w)
	return false
}

// Handle serves /api/security (owner/admin to read and answer alerts;
// owner for Mode Darurat, device sign-out and baseline approval).
func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal := auth.Current(r)
	if principal == nil || !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("Pusat Keamanan hanya untuk owner/admin")); return }
	owner := auth.IsOwner(r)
	switch r.Method {
	case http.MethodGet:
		alerts, err := d1.Query(`SELECT id, level, kind, title, detail, created_at, COALESCE(acknowledged_at, '') AS acknowledged_at, answer
			FROM security_alerts ORDER BY (acknowledged_at IS NULL) DESC, id DESC LIMIT 80`)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		events, err := d1.Query(`SELECT action, target, created_at FROM admin_audit_log ORDER BY id DESC LIMIT 150`)
		if err != nil { events = []map[string]interface{}{} }
		sessions, err := auth.Sessions(r)
		if err != nil { sessions = []map[string]interface{}{} }
		lockdown := auth.LockdownSince()
		result := map[string]interface{}{
			"alerts": alerts, "events": events, "sessions": sessions, "owner": owner,
			"lockdown": map[string]interface{}{"active": lockdown != "", "since": lockdown},
			"patrol": map[string]string{"last_run": state("last_patrol")},
			"watchdog": map[string]interface{}{"configured": len(os.Getenv("DEVCONTROL_HEARTBEAT_SECRET")) >= 32, "last_seen": state("heartbeat_seen")},
			"telegram": telegramConfigured(),
		}
		if lockdown != "" && owner { result["checklist"] = checklist() }
		util.JSON(w, http.StatusOK, result)
		return
	case http.MethodPost:
	default:
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST")); return
	}
	var input struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan tidak valid")); return
	}
	switch input.Action {
	case "ack":
		if input.Answer != "saya" && input.Answer != "bukan" && input.Answer != "dicatat" { util.Error(w, http.StatusBadRequest, fmt.Errorf("jawaban tidak dikenal")); return }
		rows, err := d1.Query(`UPDATE security_alerts SET acknowledged_at = CURRENT_TIMESTAMP, answer = ? WHERE id = ? AND acknowledged_at IS NULL RETURNING title, kind`, input.Answer+" · "+principal.Name, input.ID)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if input.Answer == "bukan" && len(rows) > 0 {
			Raise("darurat", "denied", "denied:"+input.ID, "Kejadian tidak dikenali: "+text(rows[0], "title"),
				principal.Name+" menyatakan kejadian ini bukan dirinya. Anggap ada penyusup: aktifkan Mode Darurat lalu kunci dari luar.")
		}
		if input.Answer == "saya" && len(rows) > 0 && text(rows[0], "kind") == "commit" && owner {
			if sha := strings.TrimPrefix(text(rows[0], "title"), "Commit baru di repo DevControl: "); len(sha) >= 7 { setState("approved_commit_prefix", sha) }
		}
		util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "lockdown":
		if !owner { util.Error(w, http.StatusForbidden, fmt.Errorf("Mode Darurat hanya dapat diaktifkan owner")); return }
		if !reauth(w, r) { return }
		for _, item := range outsideChecklist { setState("fp:"+item.Env, fingerprint(os.Getenv(item.Env))) }
		if err := auth.StartLockdown(w, r); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		Raise("darurat", "lockdown", "lockdown:"+time.Now().UTC().Format("2006-01-02T15:04:05"), "Mode Darurat diaktifkan",
			"Semua sesi lain dan token member dikeluarkan, API key dibekukan, perubahan dikunci. Selesaikan checklist \"kunci dari luar\".")
		util.JSON(w, http.StatusOK, map[string]interface{}{"active": true, "checklist": checklist()})
	case "unlock":
		if !owner { util.Error(w, http.StatusForbidden, fmt.Errorf("hanya owner")); return }
		if !reauth(w, r) { return }
		if err := auth.EndLockdown(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]bool{"active": false})
	case "revoke_session":
		if !owner { util.Error(w, http.StatusForbidden, fmt.Errorf("hanya owner yang dapat mengeluarkan perangkat")); return }
		if !reauth(w, r) { return }
		done, err := auth.RevokeSession(input.ID)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]bool{"revoked": done})
	case "test_alert":
		if !owner { util.Error(w, http.StatusForbidden, fmt.Errorf("hanya owner")); return }
		webpush.Notify(webpush.Message{Event: webpush.EventSecurity, URL: "/security", Tag: "security-test", Title: "🧪 Uji alarm keamanan", Body: "Notifikasi keamanan perangkat ini berfungsi."})
		if err := telegram("🧪 DevControl · uji alarm keamanan\nSaluran Telegram berfungsi."); err != nil {
			util.JSON(w, http.StatusOK, map[string]interface{}{"push": true, "telegram": false, "error": err.Error()}); return
		}
		util.JSON(w, http.StatusOK, map[string]interface{}{"push": true, "telegram": true})
	default:
		util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi tidak dikenal"))
	}
}

// OpenAlerts counts unanswered siaga/darurat alerts (used by the heartbeat).
func OpenAlerts() int64 {
	rows, err := d1.Query(`SELECT COUNT(*) AS n FROM security_alerts WHERE acknowledged_at IS NULL AND level IN ('siaga', 'darurat')`)
	if err != nil || len(rows) == 0 { return -1 }
	return number(rows[0]["n"])
}

// HandleHeartbeat answers the independent watchdog. It needs
// Authorization: Bearer DEVCONTROL_HEARTBEAT_SECRET and signs its answer with
// the same secret, so the watchdog notices a replaced or silenced app.
func HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	secret := os.Getenv("DEVCONTROL_HEARTBEAT_SECRET")
	if len(secret) < 32 { util.Error(w, http.StatusNotFound, fmt.Errorf("tidak ditemukan")); return }
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 { util.Error(w, http.StatusUnauthorized, fmt.Errorf("tidak diizinkan")); return }
	nonce := r.URL.Query().Get("nonce")
	if len(nonce) < 16 || len(nonce) > 128 { util.Error(w, http.StatusBadRequest, fmt.Errorf("nonce tidak valid")); return }
	now := time.Now().UTC().Format(time.RFC3339)
	setState("heartbeat_seen", time.Now().UTC().Format("2006-01-02 15:04:05"))
	open := OpenAlerts()
	lockdown := auth.LockdownActive()
	lastPatrol := state("last_patrol")
	message := fmt.Sprintf("%s|%s|%d|%t|%s", nonce, now, open, lockdown, lastPatrol)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	util.JSON(w, http.StatusOK, map[string]interface{}{"time": now, "open_alerts": open, "lockdown": lockdown, "last_patrol": lastPatrol,
		"commit": os.Getenv("VERCEL_GIT_COMMIT_SHA"), "signature": hex.EncodeToString(mac.Sum(nil))})
}

// ---------- patrol ----------

// Patrol compares the live state with its baseline. It runs every 15 minutes
// from the runner ping and returns how many new alerts it raised.
func Patrol(ctx context.Context) int {
	raised := 0
	count := func(level, kind, key, title, detail string) {
		before, _ := d1.Query(`SELECT 1 AS found FROM security_alerts WHERE alert_key = ? LIMIT 1`, key)
		if len(before) > 0 { return }
		Raise(level, kind, key, title, detail)
		raised++
	}

	// Members and API keys that appeared without DevControl's own audit
	// entry were written straight into the database.
	if rows, err := d1.Query(`SELECT m.id, m.name, m.role FROM members m WHERE m.created_at > datetime('now', '-2 days')
		AND NOT EXISTS (SELECT 1 FROM admin_audit_log a WHERE a.action = 'member_create' AND a.target LIKE m.name || ' (%')`); err == nil {
		for _, row := range rows {
			count("darurat", "member_injected", "member-noaudit:"+text(row, "id"), "Member muncul tanpa jejak: "+text(row, "name")+" ("+text(row, "role")+")",
				"Member ini ada di database tetapi tidak pernah dibuat lewat DevControl. Kemungkinan database diubah dari luar dengan token Cloudflare.")
		}
	}
	if rows, err := d1.Query(`SELECT k.id, k.name FROM api_keys k WHERE k.created_at > datetime('now', '-2 days')
		AND NOT EXISTS (SELECT 1 FROM admin_audit_log a WHERE a.action = 'create_key' AND a.target = k.id)`); err == nil {
		for _, row := range rows {
			count("darurat", "key_injected", "key-noaudit:"+text(row, "id"), "API key muncul tanpa jejak: "+text(row, "name"),
				"API key ini ada di database tetapi tidak pernah dibuat lewat DevControl.")
		}
	}

	// 2FA that disappeared without the owner switching it off.
	if enabled, err := auth.OwnerTwoFactorEnabled(); err == nil {
		if enabled {
			setState("totp_seen", "1")
		} else if state("totp_seen") == "1" && strings.TrimSpace(os.Getenv("DEVCONTROL_TOTP_RESET")) != "1" {
			recent, _ := d1.Query(`SELECT 1 AS found FROM admin_audit_log WHERE action = 'totp_disabled' AND created_at > datetime('now', '-1 day') LIMIT 1`)
			if len(recent) == 0 {
				count("darurat", "totp_missing", "totp-vanished:"+time.Now().UTC().Format("2006-01-02"), "2FA owner hilang tanpa dimatikan",
					"2FA sebelumnya aktif, tetapi rahasianya tidak ada lagi di database dan tidak ada catatan owner mematikannya.")
			}
			setState("totp_seen", "0")
		}
	}

	raised += patrolCommit(ctx, count)
	raised += patrolEnv(count)
	setState("last_patrol", time.Now().UTC().Format("2006-01-02 15:04:05"))
	return raised
}

// patrolCommit: a newer commit on DevControl's branch that was not pushed by
// Update Diri (and is not the running version) is reported once.
func patrolCommit(ctx context.Context, count func(level, kind, key, title, detail string)) int {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	owner, slug := strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_OWNER")), strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_SLUG"))
	branch, running := strings.TrimSpace(os.Getenv("VERCEL_GIT_COMMIT_REF")), strings.TrimSpace(os.Getenv("VERCEL_GIT_COMMIT_SHA"))
	if token == "" || owner == "" || slug == "" || branch == "" || running == "" { return 0 }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(slug)+"/commits/"+url.PathEscape(branch), nil)
	if err != nil { return 0 }
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil { return 0 }
	defer resp.Body.Close()
	var head struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct { Name string `json:"name"`; Date string `json:"date"` } `json:"author"`
		} `json:"commit"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&head) != nil || head.SHA == "" { return 0 }
	if strings.EqualFold(head.SHA, running) { return 0 }
	if approved := state("approved_commit_prefix"); approved != "" && strings.HasPrefix(head.SHA, approved) { return 0 }
	// Update Diri itself moves the branch ahead of the running version for a
	// few minutes; those commits are expected.
	recent, _ := d1.Query(`SELECT 1 AS found FROM deployment_jobs WHERE kind = 'self_update' AND updated_at > datetime('now', '-45 minutes') LIMIT 1`)
	if len(recent) > 0 { return 0 }
	short := head.SHA
	if len(short) > 12 { short = short[:12] }
	message := strings.SplitN(head.Commit.Message, "\n", 2)[0]
	if len(message) > 140 { message = message[:140] }
	count("siaga", "commit", "commit:"+head.SHA, "Commit baru di repo DevControl: "+short,
		fmt.Sprintf("Branch %s di %s/%s berisi commit %s oleh %s (\"%s\") yang tidak lewat Update Diri. Periksa https://github.com/%s/%s/commit/%s. Kalau ini perubahan Anda sendiri, pilih \"Ini saya\".",
			branch, owner, slug, short, head.Commit.Author.Name, message, owner, slug, head.SHA))
	return 0
}

// patrolEnv: DevControl's own Environment Variables changed since the last
// round. Changes made through DevControl are Waspada, others Siaga.
func patrolEnv(count func(level, kind, key, title, detail string)) int {
	token, project := strings.TrimSpace(os.Getenv("VERCEL_TOKEN")), strings.TrimSpace(os.Getenv("VERCEL_PROJECT_ID"))
	if token == "" || project == "" { return 0 }
	endpoint := "https://api.vercel.com/v10/projects/" + url.PathEscape(project) + "/env"
	if team := strings.TrimSpace(os.Getenv("VERCEL_TEAM_ID")); team != "" { endpoint += "?teamId=" + url.QueryEscape(team) }
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil { return 0 }
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil { return 0 }
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || resp.StatusCode != http.StatusOK { return 0 }
	type entry struct {
		Key       string      `json:"key"`
		UpdatedAt json.Number `json:"updatedAt"`
	}
	var entries []entry
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		_ = json.Unmarshal(trimmed, &entries)
	} else {
		var wrapper struct { Envs []entry `json:"envs"` }
		_ = json.Unmarshal(trimmed, &wrapper)
		entries = wrapper.Envs
	}
	lines, keys := []string{}, []string{}
	for _, item := range entries { lines = append(lines, item.Key+"|"+item.UpdatedAt.String()); keys = append(keys, item.Key) }
	sort.Strings(lines)
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	current := hex.EncodeToString(sum[:])
	previous, previousKeys := state("env_hash"), state("env_keys")
	setState("env_hash", current)
	setState("env_keys", strings.Join(keys, ","))
	if previous == "" || previous == current { return 0 }
	added, removed := diff(strings.Split(previousKeys, ","), keys)
	changes := []string{}
	if len(added) > 0 { changes = append(changes, "ditambah: "+strings.Join(added, ", ")) }
	if len(removed) > 0 { changes = append(changes, "dihapus: "+strings.Join(removed, ", ")) }
	if len(changes) == 0 { changes = append(changes, "nilai variabel yang ada diubah") }
	level := "siaga"
	if recent, _ := d1.Query(`SELECT 1 AS found FROM admin_audit_log WHERE action LIKE 'vercel_env_%' AND target LIKE ? AND created_at > datetime('now', '-30 minutes') LIMIT 1`, project+":%"); len(recent) > 0 {
		level = "waspada"
	}
	count(level, "env", "env:"+current[:16], "Environment Variables DevControl berubah",
		"Perubahan sejak patroli sebelumnya — "+strings.Join(changes, "; ")+". Kalau bukan Anda, anggap token/kata sandi sudah dibaca penyusup.")
	return 0
}

func diff(before, after []string) (added, removed []string) {
	old, now := map[string]bool{}, map[string]bool{}
	for _, key := range before { if key != "" { old[key] = true } }
	for _, key := range after { if key != "" { now[key] = true } }
	for key := range now { if !old[key] { added = append(added, key) } }
	for key := range old { if !now[key] { removed = append(removed, key) } }
	sort.Strings(added)
	sort.Strings(removed)
	return
}

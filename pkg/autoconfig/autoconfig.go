// Package autoconfig lets a fresh DevControl installation run from a few core
// variables. Everything that can be computed or discovered is filled in at
// runtime (os.Setenv), so the rest of the code keeps reading os.Getenv as
// before. A value set explicitly in Vercel always wins over a derived one.
//
// Core variables:
//   CF_API_TOKEN, DEVCONTROL_ADMIN_PASSWORD   (required)
//   GITHUB_TOKEN, VERCEL_TOKEN                (deployment features)
//
// Derived / discovered:
//   DEVCONTROL_SESSION_SECRET  HMAC of the admin password and CF token
//   ZIP_ARCHIVE_ACCESS_TOKEN   falls back to the admin password
//   CF_ACCOUNT_ID              the only account, or the one holding devcontrol-db
//   CF_D1_DATABASE_ID          database "devcontrol-db", created when missing
//   CF_R2_BUCKET               "devcontrol-<account prefix>", created by setup
//   VERCEL_TEAM_ID             team that owns this Vercel project
//
// When a choice cannot be made safely (for example several Cloudflare
// accounts), a confirmation is returned to the app so the admin answers it
// right away instead of editing environment variables.
package autoconfig

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"devcontrol/pkg/archive"
	"devcontrol/pkg/auth"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
)

// DatabaseName is the D1 database DevControl looks for and creates.
const DatabaseName = "devcontrol-db"

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

type Confirmation struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Detail  string   `json:"detail"`
	Options []Option `json:"options"`
}

type Step struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Status   string `json:"status"` // ok | pending | confirm | missing | error | optional
	Detail   string `json:"detail"`
	Source   string `json:"source,omitempty"` // env | auto | derived
	Required bool   `json:"required"`
}

type account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var state struct {
	sync.Mutex
	lastDiscovery time.Time
	discoveryErr  string
	accounts      []account
	withDB        map[string]string // account id -> D1 uuid
	sources       map[string]string // env key -> auto | derived
	teamChecked   bool
	deepAt        time.Time
	deepSteps     []Step
}

var client = &http.Client{Timeout: 8 * time.Second}

func env(key string) string { return strings.TrimSpace(os.Getenv(key)) }

// setAuto must be called with state locked.
func setAuto(key, value, source string) {
	if value == "" { return }
	_ = os.Setenv(key, value)
	if state.sources == nil { state.sources = map[string]string{} }
	state.sources[key] = source
}

func sourceOf(key string) string {
	if source, ok := state.sources[key]; ok { return source }
	if env(key) != "" { return "env" }
	return ""
}

// Apply runs at the start of every API request. It is instant once the
// instance is configured; discovery only runs while something is missing
// and is retried at most every 20 seconds per instance.
func Apply(ctx context.Context) {
	state.Lock()
	defer state.Unlock()
	deriveLocked()
	detectVercelTeamLocked(ctx)
	if env("CF_API_TOKEN") == "" { return }
	if env("CF_ACCOUNT_ID") != "" && env("CF_D1_DATABASE_ID") != "" { return }
	if time.Since(state.lastDiscovery) < 20*time.Second { return }
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	discoverLocked(discoverCtx)
	deriveLocked()
}

func deriveLocked() {
	password := os.Getenv("DEVCONTROL_ADMIN_PASSWORD")
	if len(password) >= 16 {
		if len(os.Getenv("DEVCONTROL_SESSION_SECRET")) < 32 {
			mac := hmac.New(sha256.New, []byte(password))
			_, _ = mac.Write([]byte("devcontrol/session/v1|" + env("CF_API_TOKEN")))
			setAuto("DEVCONTROL_SESSION_SECRET", hex.EncodeToString(mac.Sum(nil)), "derived")
		}
		if len(os.Getenv("ZIP_ARCHIVE_ACCESS_TOKEN")) < 16 {
			setAuto("ZIP_ARCHIVE_ACCESS_TOKEN", password, "derived")
		}
	}
	if env("CF_R2_BUCKET") == "" {
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') { return r }
			return -1
		}, strings.ToLower(env("CF_ACCOUNT_ID")))
		if len(clean) >= 12 { setAuto("CF_R2_BUCKET", "devcontrol-"+clean[:12], "derived") }
	}
}

// ---------- Cloudflare ----------

type cfEnvelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func cloudflare(ctx context.Context, method, path string, body interface{}, destination interface{}) (int, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil { return 0, err }
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4"+path, reader)
	if err != nil { return 0, err }
	req.Header.Set("Authorization", "Bearer "+env("CF_API_TOKEN"))
	if body != nil { req.Header.Set("Content-Type", "application/json") }
	resp, err := client.Do(req)
	if err != nil { return 0, err }
	defer resp.Body.Close()
	var parsed cfEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&parsed); err != nil {
		return resp.StatusCode, fmt.Errorf("respons Cloudflare tidak valid (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 300 || !parsed.Success {
		message := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if len(parsed.Errors) > 0 { message = parsed.Errors[0].Message }
		return resp.StatusCode, fmt.Errorf("%s", message)
	}
	if destination != nil && len(parsed.Result) > 0 {
		if err := json.Unmarshal(parsed.Result, destination); err != nil { return resp.StatusCode, err }
	}
	return resp.StatusCode, nil
}

func listAccounts(ctx context.Context) ([]account, error) {
	var result []account
	if _, err := cloudflare(ctx, http.MethodGet, "/accounts?per_page=50", nil, &result); err != nil {
		return nil, fmt.Errorf("gagal membaca akun Cloudflare (%v); pastikan CF_API_TOKEN benar dan punya izin tingkat Account", err)
	}
	clean := make([]account, 0, len(result))
	for _, item := range result { if item.ID != "" { clean = append(clean, item) } }
	if len(clean) == 0 { return nil, fmt.Errorf("CF_API_TOKEN tidak memiliki akses ke akun Cloudflare mana pun") }
	sort.Slice(clean, func(i, j int) bool { return strings.ToLower(clean[i].Name) < strings.ToLower(clean[j].Name) })
	return clean, nil
}

func findDatabase(ctx context.Context, accountID string) (string, error) {
	var result []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	}
	path := "/accounts/" + url.PathEscape(accountID) + "/d1/database?per_page=100&name=" + url.QueryEscape(DatabaseName)
	if _, err := cloudflare(ctx, http.MethodGet, path, nil, &result); err != nil {
		return "", fmt.Errorf("gagal membaca daftar D1 (%v); token perlu izin D1 Edit", err)
	}
	for _, item := range result { if item.Name == DatabaseName && item.UUID != "" { return item.UUID, nil } }
	return "", nil
}

func databaseExists(ctx context.Context, accountID, databaseID string) bool {
	status, err := cloudflare(ctx, http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/d1/database/"+url.PathEscape(databaseID), nil, nil)
	return err == nil && status == http.StatusOK
}

func createDatabase(ctx context.Context, accountID string) (string, error) {
	var result struct { UUID string `json:"uuid"` }
	_, err := cloudflare(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/d1/database", map[string]string{"name": DatabaseName}, &result)
	if err == nil && result.UUID != "" { return result.UUID, nil }
	// Another instance may have created it a moment ago.
	if id, findErr := findDatabase(ctx, accountID); findErr == nil && id != "" { return id, nil }
	if err == nil { err = fmt.Errorf("Cloudflare tidak mengembalikan ID database") }
	return "", fmt.Errorf("gagal membuat database D1 %s (%v); token perlu izin D1 Edit", DatabaseName, err)
}

// discoverLocked is read-only: it never creates resources.
func discoverLocked(ctx context.Context) {
	state.lastDiscovery = time.Now()
	state.discoveryErr = ""
	accountID, databaseID := env("CF_ACCOUNT_ID"), env("CF_D1_DATABASE_ID")
	if accountID != "" {
		state.accounts = []account{{ID: accountID}}
		if databaseID == "" {
			id, err := findDatabase(ctx, accountID)
			if err != nil { state.discoveryErr = err.Error(); return }
			state.withDB = map[string]string{}
			if id != "" { state.withDB[accountID] = id; setAuto("CF_D1_DATABASE_ID", id, "auto") }
		}
		return
	}
	accounts, err := listAccounts(ctx)
	if err != nil { state.discoveryErr = err.Error(); return }
	if len(accounts) > 20 { accounts = accounts[:20] }
	state.accounts = accounts
	found := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var lastErr error
	for _, item := range accounts {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if databaseID != "" {
				if databaseExists(ctx, id, databaseID) { mu.Lock(); found[id] = databaseID; mu.Unlock() }
				return
			}
			uuid, err := findDatabase(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil { lastErr = err; return }
			if uuid != "" { found[id] = uuid }
		}(item.ID)
	}
	wg.Wait()
	state.withDB = found
	if len(accounts) == 1 {
		setAuto("CF_ACCOUNT_ID", accounts[0].ID, "auto")
		if databaseID == "" { setAuto("CF_D1_DATABASE_ID", found[accounts[0].ID], "auto") }
		if lastErr != nil && found[accounts[0].ID] == "" { state.discoveryErr = lastErr.Error() }
		return
	}
	if len(found) == 1 {
		for id, uuid := range found {
			setAuto("CF_ACCOUNT_ID", id, "auto")
			if databaseID == "" { setAuto("CF_D1_DATABASE_ID", uuid, "auto") }
		}
		return
	}
	if lastErr != nil && len(found) == 0 { state.discoveryErr = lastErr.Error() }
}

// ---------- Vercel ----------

func vercelGET(ctx context.Context, path string, destination interface{}) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.vercel.com"+path, nil)
	if err != nil { return 0, err }
	req.Header.Set("Authorization", "Bearer "+env("VERCEL_TOKEN"))
	resp, err := client.Do(req)
	if err != nil { return 0, err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || destination == nil { return resp.StatusCode, nil }
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(destination)
}

// detectVercelTeamLocked finds the team owning this project once per instance.
func detectVercelTeamLocked(ctx context.Context) {
	if state.teamChecked { return }
	projectID := env("VERCEL_PROJECT_ID")
	if env("VERCEL_TEAM_ID") != "" || env("VERCEL_TOKEN") == "" || projectID == "" { state.teamChecked = true; return }
	state.teamChecked = true
	lookup, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	projectPath := "/v9/projects/" + url.PathEscape(projectID)
	if status, err := vercelGET(lookup, projectPath, nil); err == nil && status == http.StatusOK { return }
	var teams struct { Teams []struct { ID string `json:"id"` } `json:"teams"` }
	if status, err := vercelGET(lookup, "/v2/teams?limit=50", &teams); err != nil || status != http.StatusOK { return }
	for _, team := range teams.Teams {
		if team.ID == "" { continue }
		if status, err := vercelGET(lookup, projectPath+"?teamId="+url.QueryEscape(team.ID), nil); err == nil && status == http.StatusOK {
			setAuto("VERCEL_TEAM_ID", team.ID, "auto")
			return
		}
	}
}

// persistToVercel stores a chosen value so new instances start with it.
func persistToVercel(ctx context.Context, key, value string) error {
	token, projectID := env("VERCEL_TOKEN"), env("VERCEL_PROJECT_ID")
	if token == "" || projectID == "" { return fmt.Errorf("VERCEL_TOKEN dan VERCEL_PROJECT_ID diperlukan") }
	endpoint := "https://api.vercel.com/v10/projects/" + url.PathEscape(projectID) + "/env?upsert=true"
	if team := env("VERCEL_TEAM_ID"); team != "" { endpoint += "&teamId=" + url.QueryEscape(team) }
	payload, _ := json.Marshal(map[string]interface{}{"key": key, "value": value, "type": "encrypted", "target": []string{"production", "preview"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated { return fmt.Errorf("HTTP %d", resp.StatusCode) }
	return nil
}

// ---------- status & confirmations ----------

func confirmationsLocked() []Confirmation {
	out := []Confirmation{}
	if env("CF_ACCOUNT_ID") == "" && len(state.accounts) > 1 {
		options := make([]Option, 0, len(state.accounts))
		for _, item := range state.accounts {
			label := item.Name
			if label == "" { label = item.ID }
			hint := "Database dan bucket baru akan dibuat di sini"
			if state.withDB[item.ID] != "" { hint = "Sudah memiliki database " + DatabaseName }
			options = append(options, Option{Value: item.ID, Label: label, Hint: hint})
		}
		detail := fmt.Sprintf("Token Cloudflare ini dapat mengakses %d akun. Pilih akun tempat DevControl menyimpan database D1 dan bucket R2-nya.", len(state.accounts))
		if len(state.withDB) > 1 { detail = fmt.Sprintf("Lebih dari satu akun sudah memiliki database %s. Pilih yang dipakai DevControl.", DatabaseName) }
		out = append(out, Confirmation{ID: "cf-account", Title: "Pilih akun Cloudflare", Detail: detail, Options: options})
	}
	return out
}

func baseStepsLocked() []Step {
	steps := []Step{}
	if len(os.Getenv("DEVCONTROL_ADMIN_PASSWORD")) >= 16 {
		steps = append(steps, Step{ID: "admin", Label: "Kata sandi admin", Status: "ok", Source: "env", Required: true,
			Detail: "Rahasia sesi dan kunci arsip ZIP diturunkan otomatis bila tidak diisi."})
	} else {
		steps = append(steps, Step{ID: "admin", Label: "Kata sandi admin", Status: "missing", Required: true,
			Detail: "Isi DEVCONTROL_ADMIN_PASSWORD (minimal 16 karakter) di Vercel, lalu Redeploy."})
	}
	switch {
	case env("CF_API_TOKEN") == "":
		steps = append(steps, Step{ID: "cf_token", Label: "Token Cloudflare", Status: "missing", Required: true,
			Detail: "Isi CF_API_TOKEN di Vercel. Izin: Account D1 Edit, Workers R2 Storage Edit, Workers Scripts Edit, Account Analytics Read, Zone Read."})
	case state.discoveryErr != "" && env("CF_D1_DATABASE_ID") == "":
		steps = append(steps, Step{ID: "cf_token", Label: "Token Cloudflare", Status: "error", Required: true, Detail: state.discoveryErr})
	default:
		steps = append(steps, Step{ID: "cf_token", Label: "Token Cloudflare", Status: "ok", Source: "env", Required: true, Detail: "Token terbaca."})
	}
	if env("CF_API_TOKEN") != "" {
		accountStep := Step{ID: "cf_account", Label: "Akun Cloudflare", Required: true, Source: sourceOf("CF_ACCOUNT_ID")}
		switch {
		case env("CF_ACCOUNT_ID") != "":
			accountStep.Status, accountStep.Detail = "ok", "Akun terdeteksi."
			for _, item := range state.accounts { if item.ID == env("CF_ACCOUNT_ID") && item.Name != "" { accountStep.Detail = item.Name } }
		case len(state.accounts) > 1:
			accountStep.Status, accountStep.Detail = "confirm", "Menunggu pilihan Anda."
		default:
			accountStep.Status, accountStep.Detail = "pending", "Sedang dideteksi."
		}
		steps = append(steps, accountStep)
		database := Step{ID: "d1_database", Label: "Database D1 (" + DatabaseName + ")", Required: true, Source: sourceOf("CF_D1_DATABASE_ID")}
		if env("CF_D1_DATABASE_ID") != "" {
			database.Status, database.Detail = "ok", "Terhubung."
		} else if env("CF_ACCOUNT_ID") != "" {
			database.Status, database.Detail = "pending", "Belum ada; akan dibuat otomatis."
		} else {
			database.Status, database.Detail = "pending", "Menunggu akun Cloudflare."
		}
		steps = append(steps, database)
	}
	return steps
}

func optionalStepsLocked() []Step {
	steps := []Step{}
	if env("GITHUB_TOKEN") != "" {
		steps = append(steps, Step{ID: "github", Label: "GitHub", Status: "ok", Source: "env", Detail: "Aplikasi Baru, Update Aplikasi, dan Update Diri aktif."})
	} else {
		steps = append(steps, Step{ID: "github", Label: "GitHub", Status: "optional", Detail: "Isi GITHUB_TOKEN untuk fitur deployment."})
	}
	if env("VERCEL_TOKEN") != "" {
		detail := "Akun pribadi."
		if env("VERCEL_TEAM_ID") != "" { detail = "Team terdeteksi." }
		if env("VERCEL_PROJECT_ID") == "" { detail = "Aktifkan System Environment Variables di Vercel agar project ini dikenali." }
		steps = append(steps, Step{ID: "vercel", Label: "Vercel", Status: "ok", Source: "env", Detail: detail})
	} else {
		steps = append(steps, Step{ID: "vercel", Label: "Vercel", Status: "optional", Detail: "Isi VERCEL_TOKEN untuk fitur deployment."})
	}
	return steps
}

// deepSteps checks schema and bucket; cached briefly so polling stays cheap.
func deepSteps(force bool) []Step {
	state.Lock()
	if !force && time.Since(state.deepAt) < 15*time.Second && state.deepSteps != nil {
		cached := state.deepSteps
		state.Unlock()
		return cached
	}
	ready := env("CF_ACCOUNT_ID") != "" && env("CF_D1_DATABASE_ID") != ""
	state.Unlock()
	if !ready { return []Step{} }
	steps := []Step{}
	schema := Step{ID: "d1_schema", Label: "Struktur tabel D1", Required: true}
	if current, err := setup.IsCurrent(); err != nil {
		schema.Status, schema.Detail = "error", err.Error()
	} else if current {
		schema.Status, schema.Detail = "ok", "Sesuai versi aplikasi."
	} else {
		schema.Status, schema.Detail = "pending", "Akan dipasang otomatis."
	}
	steps = append(steps, schema)
	bucket := Step{ID: "r2_bucket", Label: "Bucket R2 (" + env("CF_R2_BUCKET") + ")", Required: true, Source: sourceOf("CF_R2_BUCKET")}
	if store, err := archive.New(); err != nil {
		bucket.Status, bucket.Detail = "error", err.Error()
	} else if found, err := store.BucketStatus(); err != nil {
		bucket.Status, bucket.Detail = "error", err.Error()
	} else if found {
		bucket.Status, bucket.Detail = "ok", "Tersedia."
	} else {
		bucket.Status, bucket.Detail = "pending", "Akan dibuat otomatis."
	}
	steps = append(steps, bucket)
	state.Lock()
	state.deepSteps, state.deepAt = steps, time.Now()
	state.Unlock()
	return steps
}

func fullStatus(force bool) map[string]interface{} {
	state.Lock()
	steps := baseStepsLocked()
	confirmations := confirmationsLocked()
	optional := optionalStepsLocked()
	derived := map[string]string{}
	for key, source := range state.sources { derived[key] = source }
	state.Unlock()
	steps = append(steps, deepSteps(force)...)
	steps = append(steps, optional...)
	ready, runnable, blocked := true, false, false
	for _, step := range steps {
		if !step.Required { continue }
		switch step.Status {
		case "ok":
		case "pending": ready, runnable = false, true
		default: ready, blocked = false, true
		}
	}
	return map[string]interface{}{
		"authenticated": true, "ready": ready, "auto_runnable": runnable && len(confirmations) == 0,
		"blocked": blocked, "steps": steps, "confirmations": confirmations, "derived": derived,
	}
}

// run performs every step that needs no human decision.
func run(ctx context.Context) []string {
	notes := []string{}
	state.Lock()
	state.lastDiscovery = time.Time{}
	if env("CF_API_TOKEN") != "" && (env("CF_ACCOUNT_ID") == "" || env("CF_D1_DATABASE_ID") == "") { discoverLocked(ctx) }
	if accountID := env("CF_ACCOUNT_ID"); accountID != "" && env("CF_D1_DATABASE_ID") == "" && state.discoveryErr == "" {
		if id, err := createDatabase(ctx, accountID); err != nil {
			state.discoveryErr = err.Error()
			notes = append(notes, err.Error())
		} else {
			setAuto("CF_D1_DATABASE_ID", id, "auto")
			notes = append(notes, "Database D1 "+DatabaseName+" dibuat.")
		}
	}
	deriveLocked()
	ready := env("CF_ACCOUNT_ID") != "" && env("CF_D1_DATABASE_ID") != ""
	state.Unlock()
	if !ready { return notes }
	if err := setup.Prepare(); err != nil {
		notes = append(notes, "Struktur D1: "+err.Error())
	} else {
		notes = append(notes, "Struktur D1 siap.")
	}
	if store, err := archive.New(); err != nil {
		notes = append(notes, "Bucket R2: "+err.Error())
	} else if created, err := store.EnsureBucket(); err != nil {
		notes = append(notes, "Bucket R2: "+err.Error())
	} else if created {
		notes = append(notes, "Bucket R2 "+env("CF_R2_BUCKET")+" dibuat.")
	}
	return notes
}

func choose(ctx context.Context, id, value string) ([]string, error) {
	if id != "cf-account" { return nil, fmt.Errorf("konfirmasi tidak dikenal atau sudah selesai") }
	state.Lock()
	if env("CF_ACCOUNT_ID") != "" { state.Unlock(); return []string{"Akun Cloudflare sudah ditetapkan."}, nil }
	valid := false
	for _, item := range state.accounts { if item.ID == value { valid = true } }
	if !valid { state.Unlock(); return nil, fmt.Errorf("akun tidak ada dalam daftar; muat ulang pilihan") }
	ambiguous := len(state.withDB) > 1
	databaseID := state.withDB[value]
	setAuto("CF_ACCOUNT_ID", value, "auto")
	if databaseID != "" && env("CF_D1_DATABASE_ID") == "" { setAuto("CF_D1_DATABASE_ID", databaseID, "auto") }
	deriveLocked()
	state.Unlock()
	notes := run(ctx)
	if ambiguous && env("CF_D1_DATABASE_ID") != "" {
		// New instances could not tell the accounts apart, so store the choice.
		errAccount := persistToVercel(ctx, "CF_ACCOUNT_ID", value)
		errDatabase := persistToVercel(ctx, "CF_D1_DATABASE_ID", env("CF_D1_DATABASE_ID"))
		if errAccount != nil || errDatabase != nil {
			notes = append(notes, "Pilihan aktif sekarang, tetapi belum tersimpan ke Vercel. Isi CF_ACCOUNT_ID dan CF_D1_DATABASE_ID secara manual agar permanen.")
		} else {
			notes = append(notes, "Pilihan disimpan ke Vercel dan berlaku penuh setelah deploy berikutnya.")
		}
	}
	return notes, nil
}

// Handle serves GET/POST /api/auto-setup.
// Anyone may see which core variables are filled (no values); the full
// status, confirmations and actions require the admin session.
func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !auth.IsAdmin(r) {
		if r.Method != http.MethodGet { util.Error(w, http.StatusForbidden, fmt.Errorf("sesi admin diperlukan")); return }
		core := []map[string]interface{}{
			{"key": "CF_API_TOKEN", "set": env("CF_API_TOKEN") != "", "required": true},
			{"key": "DEVCONTROL_ADMIN_PASSWORD", "set": len(os.Getenv("DEVCONTROL_ADMIN_PASSWORD")) >= 16, "required": true},
			{"key": "GITHUB_TOKEN", "set": env("GITHUB_TOKEN") != "", "required": false},
			{"key": "VERCEL_TOKEN", "set": env("VERCEL_TOKEN") != "", "required": false},
		}
		util.JSON(w, http.StatusOK, map[string]interface{}{"authenticated": false, "admin_configured": auth.Configured(), "core": core})
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.JSON(w, http.StatusOK, fullStatus(r.URL.Query().Get("fresh") == "1"))
	case http.MethodPost:
		var input struct {
			Action string `json:"action"`
			ID     string `json:"id"`
			Value  string `json:"value"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan setup tidak valid")); return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		var notes []string
		switch input.Action {
		case "run":
			notes = run(ctx)
		case "choose":
			var err error
			notes, err = choose(ctx, input.ID, input.Value)
			if err != nil { util.Error(w, http.StatusBadRequest, err); return }
		default:
			util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi setup tidak dikenal")); return
		}
		status := fullStatus(true)
		status["notes"] = notes
		util.JSON(w, http.StatusOK, status)
	default:
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST"))
	}
}

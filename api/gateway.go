// Package handler implements every DevControl API endpoint as ONE Vercel
// serverless function.
//
// Why a single file: Vercel's Go builder treats every .go file directly
// under /api as its own candidate function and requires each one to
// export a matching handler — so a second file in this folder with no
// exported function fails the build ("Could not find an exported
// function in ..."), and a second file that also exported `Handler`
// collides with this one ("Handler redeclared"). Either way, more than
// one file in this folder breaks the build. Keeping the whole API
// surface in this single file removes that failure mode entirely: there
// is exactly one exported Handler in the whole project, and nothing else
// lives in /api for Vercel to trip over. (trigger-deployment and
// self-update were previously split into their own deploy.go /
// selfupdate.go files with unexported handlers — that avoided the
// "Handler redeclared" collision but not the "no exported function"
// one, since Vercel still scans every file in /api individually. They're
// merged in below for the same reason the rest of this file is here.)
//
// Routing: vercel.json rewrites each public path (/api/overview,
// /api/services, ...) to /api/gateway?resource=<name>, and Handler below
// dispatches on the `resource` query parameter.
package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"devcontrol/pkg/apimanagement"
	"devcontrol/pkg/activescan"
	"devcontrol/pkg/appaudit"
	"devcontrol/pkg/autoconfig"
	"devcontrol/pkg/archive"
	"devcontrol/pkg/branding"
	"devcontrol/pkg/environmentstatus"
	"devcontrol/pkg/projectdelete"
	"devcontrol/pkg/reposync"
	"devcontrol/pkg/securitycenter"
	"devcontrol/pkg/projectthumbnail"
	"devcontrol/pkg/trafficmetrics"
	"devcontrol/pkg/zonemanagement"
	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/databrowser"
	"devcontrol/pkg/deploymentrunner"
	"devcontrol/pkg/diagnose"
	"devcontrol/pkg/history"
	"devcontrol/pkg/repoinventory"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
	"devcontrol/pkg/vercelapp"
	"devcontrol/pkg/vercelenv"
	"devcontrol/pkg/webpush"
)

// Handler is the sole entrypoint for the whole API surface.
func Handler(w http.ResponseWriter, r *http.Request) {
	if util.HandleCORSPreflight(w, r) {
		return
	}
	// Fill in every setting derivable from the core variables before any
	// handler (including the Cloudflare runner) reads os.Getenv.
	autoconfig.Apply(r.Context())
	auth.SecurityEvent = securitycenter.Raise
	resource := r.URL.Query().Get("resource")
	if resource == "deployment-runner" {
		if r.Method != http.MethodPost || !deploymentrunner.Authorized(r.Header.Get("Authorization")) {
			util.Error(w, http.StatusUnauthorized, fmt.Errorf("runner tidak diizinkan")); return
		}
		handleDeploymentRunner(w, r)
		return
	}
	// Scheduled by the same Cloudflare Worker every 15 minutes; checks events
	// that have no request of their own and sends Web Push notifications.
	if resource == "push-watch" {
		if r.Method != http.MethodPost || !deploymentrunner.Authorized(r.Header.Get("Authorization")) {
			util.Error(w, http.StatusUnauthorized, fmt.Errorf("runner tidak diizinkan")); return
		}
		handlePushWatch(w, r)
		return
	}
	if !auth.SameOrigin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("permintaan harus berasal dari aplikasi ini")); return }
	if resource == "branding-icon" || resource == "branding-manifest" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau HEAD")); return
		}
		branding.HandlePublic(w, r, resource)
		return
	}
	if resource == "session" {
		// Sign-in needs the lockout tables; create them before the first login.
		if r.Method == http.MethodPost && os.Getenv("CF_D1_DATABASE_ID") != "" { _ = setup.Prepare() }
		auth.HandleSession(w, r); return
	}
	if resource == "heartbeat" { securitycenter.HandleHeartbeat(w, r); return }
	if resource == "auto-setup" { autoconfig.Handle(w, r); return }
	// A leaked token cannot flood the API (per server instance, per address).
	if auth.RateLimited(auth.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		util.Error(w, http.StatusTooManyRequests, fmt.Errorf("terlalu banyak permintaan; tunggu sebentar")); return
	}
	// ZIP downloads need both the archive key and an owner/admin session.
	if resource == "zip-archives" && !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("arsip ZIP hanya untuk owner/admin")); return }
	if resource != "zip-archives" {
		if !auth.Configured() { util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("isi DEVCONTROL_ADMIN_PASSWORD (minimal 16 karakter) di Vercel untuk mengaktifkan panel admin; rahasia sesi dibuat otomatis")); return }
		if !auth.Allowed(r, resource) {
			if auth.Current(r) != nil && auth.LockdownActive() {
				util.Error(w, http.StatusForbidden, fmt.Errorf("Mode Darurat aktif: hanya owner yang dapat membuka DevControl dan semua perubahan dikunci sampai Mode Darurat dimatikan di Pusat Keamanan")); return
			}
			if auth.Current(r) != nil { util.Error(w, http.StatusForbidden, fmt.Errorf("role Anda tidak memiliki akses ke fitur ini")); return }
			util.Error(w, http.StatusUnauthorized, fmt.Errorf("login atau API key dengan hak baca diperlukan")); return
		}
	}
	// Step-up: actions that expose secrets, replace code or remove access
	// need the credential typed again within the last 15 minutes.
	if needsFreshConfirm(resource, r) && !auth.RecentlyConfirmed(r) { auth.ReauthRequired(w); return }
	// Record only traffic handled by this authenticated Go API. CDN pages,
	// images and static files do not pass through this function.
	meter := &trafficmetrics.CountingWriter{ResponseWriter: w}
	w = meter
	defer func() { trafficmetrics.Record(resource, meter.BytesWritten) }()

	switch resource {
	case "overview":
		handleOverview(w, r)
	case "deployments":
		handleDeployments(w, r)
	case "environments":
		environmentstatus.Handle(w, r, vercelAppProjectName)
	case "health":
		handleHealth(w, r)
	case "zone-approval":
		zonemanagement.Handle(w, r, queryCloudflareTraffic)
	case "performance":
		handlePerformance(w, r)
	case "services":
		handleServices(w, r)
	case "activity":
		handleActivity(w, r)
	case "logs":
		handleLogs(w, r)
	case "trigger-deployment":
		handleTriggerDeployment(w, r)
	case "self-update":
		handleSelfUpdate(w, r)
	case "github-repos":
		handleGithubRepos(w, r)
	case "vercel-projects":
		repoinventory.HandleList(w, r, vercelAppProjectName)
	case "project":
		projectdelete.Handle(w, r, vercelAppProjectName)
	case "app-repair":
		handleAppRepair(w, r)
	case "project-thumbnails":
		projectthumbnail.Handle(w, r)
	case "app-promo":
		handleAppPromo(w, r)
	case "branding":
		branding.Handle(w, r)
	case "push":
		webpush.Handle(w, r, ensurePushRunner)
	case "github-branches":
		handleGithubBranches(w, r)
	case "zip-archives":
		handleZipArchives(w, r)
	case "diagnose":
		handleDiagnose(w, r)
	case "deploy-history":
		handleDeployHistory(w, r)
	case "vercel-env":
		vercelenv.Handle(w, r)
	case "members":
		if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		auth.HandleMembers(w, r)
	case "two-factor":
		if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		auth.HandleTwoFactor(w, r)
	case "security":
		if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		securitycenter.Handle(w, r)
	case "app-audit":
		if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		appaudit.Handle(w, r, selfAuditTarget(r))
	case "active-scan":
		if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		activescan.Handle(w, r, selfAuditTarget(r))
	case "databases":
		handleDatabases(w, r)
	case "api-management":
		apimanagement.Handle(w, r)
	default:
		util.Error(w, http.StatusNotFound, fmt.Errorf("unknown or missing ?resource="))
	}
}

// needsFreshConfirm lists the requests that require a recently typed
// credential (see auth.RecentlyConfirmed). Update Diri checks it itself,
// only when a new update starts.
func needsFreshConfirm(resource string, r *http.Request) bool {
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	switch resource {
	case "vercel-env":
		return write || r.URL.Query().Get("reveal") != ""
	case "members":
		return write
	case "project":
		return r.Method == http.MethodDelete
	case "zip-archives":
		return true
	case "active-scan":
		return true
	}
	return false
}

// selfAuditTarget describes DevControl itself for the app audit.
func selfAuditTarget(r *http.Request) appaudit.Target {
	host := strings.TrimSpace(os.Getenv("VERCEL_PROJECT_PRODUCTION_URL"))
	if host == "" { host = r.Host }
	repo := ""
	if owner, slug := strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_OWNER")), strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_SLUG")); owner != "" && slug != "" {
		repo = owner + "/" + slug
	}
	return appaudit.Target{Key: appaudit.SelfKey, Name: "DevControl", URL: "https://" + host, Repo: repo, Self: true}
}

// GET /api/overview -> top summary stat cards.
func handleOverview(w http.ResponseWriter, r *http.Request) {
	rows, err := d1.Query(`
		SELECT active_projects, active_projects_change,
		       deployments_today, deployments_change,
		       uptime, uptime_change,
		       open_incidents, incidents_change
		FROM overview_stats
		ORDER BY id DESC
		LIMIT 1
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}
	if len(rows) == 0 {
		// A fresh database has no overview_stats row. Derive real counters
		// from deployed services and successful ZIP archives instead of
		// returning an empty object (which hides the numbers in the UI).
		live, err := d1.Query(`
			SELECT
			  (SELECT COUNT(*) FROM services WHERE status <> 'Down') AS active_projects,
			  (SELECT COUNT(*) FROM deployment_jobs
			   WHERE status = 'Success' AND date(created_at) = date('now')) AS deployments_today,
			  (SELECT COALESCE(ROUND(AVG(uptime), 2), 0) FROM services) AS uptime,
			  (SELECT COUNT(*) FROM services WHERE status IN ('Degraded', 'Down')) AS open_incidents
		`)
		if err != nil {
			util.Error(w, http.StatusInternalServerError, err)
			return
		}
		stats := map[string]interface{}{
			"active_projects": 0, "active_projects_change": 0,
			"deployments_today": 0, "deployments_change": 0,
			"uptime": 0, "uptime_change": 0,
			"open_incidents": 0, "incidents_change": 0,
		}
		if len(live) > 0 {
			for key, value := range live[0] { stats[key] = value }
		}
		util.JSON(w, http.StatusOK, stats)
		return
	}
	util.JSON(w, http.StatusOK, rows[0])
}

type deploymentStage struct {
	ID int `json:"id"`
	Stage string `json:"stage"`
	Duration string `json:"duration"`
	Status string `json:"status"`
	Position int `json:"position"`
	// D1's json_set may serialize bound Unix seconds as 1790304023.0.
	// JSON float64 accepts both that representation and older integer values.
	StartedAt float64 `json:"started_at,omitempty"`
	FinishedAt float64 `json:"finished_at,omitempty"`
}

type deploymentJob struct {
	ID string `json:"id"`
	Kind string `json:"kind"`
	Target string `json:"target"`
	Status string `json:"status"`
	Stages []deploymentStage `json:"stages"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Message string `json:"message,omitempty"`
	Diagnosis json.RawMessage `json:"diagnosis,omitempty"`
}

// Each deployment owns its pipeline and a lease on its repo. The Cloudflare
// runner renews the lease even when the uploading device is closed.
func expireDeploymentJobs() error {
	rows, err := d1.Query(`UPDATE deployment_jobs SET status = 'Interrupted', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'Running' AND lease_until <= CURRENT_TIMESTAMP RETURNING id`)
	for _, row := range rows { notifyJobResult(rowText(row, "id")) }
	return err
}

// Terminal pipelines remain readable for thirty minutes, then are removed
// from D1 on the next read or deployment. Active jobs are never pruned.
func pruneDeploymentJobs() error {
    if err := expireDeploymentJobs(); err != nil { return err }
    // Failed runs stay 24 hours so their error diagnosis can still be copied.
    _, err := d1.Query(`DELETE FROM deployment_jobs WHERE (status = 'Success' AND updated_at <= datetime('now', '-30 minutes'))
        OR (status IN ('Failed', 'Interrupted') AND updated_at <= datetime('now', '-24 hours'))`)
    if err != nil { return err }
    _, err = d1.Query(`DELETE FROM deployment_runner WHERE id NOT IN (SELECT id FROM deployment_jobs)`)
    return err
}

// GET /api/deployments -> independent recent pipelines.
func handleDeployments(w http.ResponseWriter, r *http.Request) {
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	if r.Method == http.MethodDelete { dismissDeployment(w, r); return }
	sweepArchives()
	if err := pruneDeploymentJobs(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	rows, err := d1.Query(`SELECT j.id, j.kind, j.target, j.status, j.stages, j.created_at, j.updated_at,
		COALESCE(r.message, '') AS message, j.diagnosis FROM deployment_jobs j
		LEFT JOIN deployment_runner r ON r.id = j.id ORDER BY j.created_at DESC, j.rowid DESC LIMIT 30`)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	jobs := make([]deploymentJob, 0, len(rows))
	for _, row := range rows {
		value := func(key string) string { result, _ := row[key].(string); return result }
		var stages []deploymentStage
		if err := json.Unmarshal([]byte(value("stages")), &stages); err != nil {
			util.Error(w, http.StatusBadGateway, fmt.Errorf("status pipeline tidak valid: %w", err)); return
		}
		job := deploymentJob{ID: value("id"), Kind: value("kind"), Target: value("target"),
			Status: value("status"), Stages: stages, CreatedAt: value("created_at"), UpdatedAt: value("updated_at"), Message: value("message")}
		if raw := value("diagnosis"); raw != "" && json.Valid([]byte(raw)) { job.Diagnosis = enrichDiagnosis(raw) }
		jobs = append(jobs, job)
	}
	util.JSON(w, http.StatusOK, jobs)
}

func startDeploymentPipeline(id, kind, target, lockKey, displayName string, names [4]string) error {
	if err := pruneDeploymentJobs(); err != nil { return err }
	stages := make([]deploymentStage, 0, 4)
	startedAt := time.Now().Unix()
	for index, name := range names {
		status := "Pending"
		if index == 0 { status = "Running" }
		stage := deploymentStage{ID: index+1, Stage: name, Duration: "-", Status: status, Position: index+1}
		if index == 0 { stage.StartedAt = float64(startedAt) }
		stages = append(stages, stage)
	}
	encoded, err := json.Marshal(stages)
	if err != nil { return err }
	_, err = d1.Query(`INSERT INTO deployment_jobs (id, kind, target, lock_key, display_name, status, stages, lease_until)
		VALUES (?, ?, ?, ?, ?, 'Running', ?, datetime('now', '+10 minutes'))`, id, kind, target, lockKey, displayName, string(encoded))
	if err != nil {
		rows, queryErr := d1.Query(`SELECT id FROM deployment_jobs WHERE lock_key = ? AND status = 'Running' LIMIT 1`, lockKey)
		if queryErr == nil && len(rows) > 0 { return fmt.Errorf("repo ini sedang diproses oleh deployment lain; tunggu sampai selesai") }
		return fmt.Errorf("gagal membuat pipeline deployment: %w", err)
	}
	return nil
}

func requireActiveDeployment(id string) error {
	rows, err := d1.Query(`UPDATE deployment_jobs SET lease_until = datetime('now', '+10 minutes'),
		updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'Running'
		AND lease_until > CURRENT_TIMESTAMP RETURNING id`, id)
	if err != nil { return err }
	if len(rows) == 0 {
		_ = expireDeploymentJobs()
		return fmt.Errorf("proses deployment tidak aktif atau sudah kedaluwarsa; periksa hasil sebelum mengulang")
	}
	return nil
}

func updateDeploymentStage(id string, position int, status string) {
	if id == "" || position < 1 || position > 4 { return }
	duration := "-"
	if status == "Success" { duration = "Selesai" }
	if status == "Failed" { duration = "Gagal" }
	timestampKey := "started_at"
	if status != "Running" { timestampKey = "finished_at" }
	statusPath := fmt.Sprintf("$[%d].status", position-1)
	durationPath := fmt.Sprintf("$[%d].duration", position-1)
	timestampPath := fmt.Sprintf("$[%d].%s", position-1, timestampKey)
	_, _ = d1.Query(`UPDATE deployment_jobs SET
		stages = json_set(stages, ?, ?, ?, ?, ?, CAST(COALESCE(json_extract(stages, ?), ?) AS INTEGER)),
		status = CASE WHEN ? = 'Failed' THEN 'Failed'
			WHEN ? = 4 AND ? = 'Success' THEN 'Success' ELSE status END,
		lease_until = datetime('now', '+10 minutes'), updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = 'Running'`,
		statusPath, status, durationPath, duration, timestampPath, timestampPath, time.Now().Unix(),
		status, position, status, id)
	// Fully successful: keep only this ZIP for the target, drop older ones.
	if position == 4 && status == "Success" {
		recordHistory(id, "Success", "")
		if store, err := archive.New(); err == nil { _ = store.Finalize(id) }
	}
	if status == "Failed" || (position == 4 && status == "Success") { notifyJobResult(id) }
}

// A Go function stops when its HTTP request ends. Cloudflare's scheduled
// Worker starts fresh requests; R2 holds the ZIP and D1 holds the build ticket.
func prepareDeploymentRunner(r *http.Request) error {
	host := os.Getenv("VERCEL_PROJECT_PRODUCTION_URL")
	if host == "" { host = r.Host }
	if os.Getenv("VERCEL_ENV") == "preview" {
		return fmt.Errorf("jalankan deployment dari domain production agar runner tetap tersedia setelah update diri")
	}
	if err := deploymentrunner.Ensure(host); err != nil { return err }
	webpush.SetRunnerVersion(deploymentrunner.Version())
	return nil
}

// ensurePushRunner installs the current scheduled Worker once after an
// update, so 404 and confirmation checks run without a new deployment.
// With force (admin pressed "Pasang ulang penjadwal") it reinstalls even when
// the stored version matches, and returns the Cloudflare error if it fails.
func ensurePushRunner(r *http.Request, force bool) error {
	if os.Getenv("VERCEL_ENV") == "preview" { return nil }
	if !force && webpush.RunnerVersion() == deploymentrunner.Version() { return nil }
	host := os.Getenv("VERCEL_PROJECT_PRODUCTION_URL")
	if host == "" { host = r.Host }
	if err := deploymentrunner.Ensure(host); err != nil { return err }
	webpush.SetRunnerVersion(deploymentrunner.Version())
	return nil
}

func init() {
	projectdelete.OnJobsInterrupted = func(ids []string) { for _, id := range ids { notifyJobResult(id) } }
}

// renotifyRecentJobs retries the notification of pipelines that finished in
// the last 30 minutes. webpush.Notify skips every job whose message a device
// already accepted (its de-duplication key is kept), so only a notification
// that could not be delivered is sent again.
func renotifyRecentJobs() {
	rows, err := d1.Query(`SELECT id FROM deployment_jobs WHERE status IN ('Success', 'Failed', 'Interrupted')
		AND updated_at >= datetime('now', '-30 minutes') ORDER BY updated_at DESC LIMIT 20`)
	if err != nil { return }
	for _, row := range rows { notifyJobResult(rowText(row, "id")) }
}

// notifyJobResult sends one push per finished pipeline (Success, Failed or
// Interrupted); the key makes repeated calls harmless.
func notifyJobResult(id string) {
	if id == "" { return }
	rows, err := d1.Query(`SELECT kind, target, display_name, status, diagnosis FROM deployment_jobs WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 { return }
	kind, target, name, status := rowText(rows[0], "kind"), rowText(rows[0], "target"), rowText(rows[0], "display_name"), rowText(rows[0], "status")
	if status != "Success" && status != "Failed" && status != "Interrupted" { return }
	event, label := webpush.EventDeploy, "Deploy aplikasi"
	switch kind {
	case "new_app": label = "Aplikasi baru"
	case "update_app": label = "Update aplikasi"
	case "self_update": event, label = webpush.EventSelfUpdate, "Update diri DevControl"
	}
	if name == "" { name = target }
	message := webpush.Message{Event: event, Key: "job:" + id + ":" + status, URL: "/deployments", Tag: "job-" + id}
	switch status {
	case "Success":
		message.Title = "✅ " + label + " berhasil"
		message.Body = name + " sudah selesai dan online."
	case "Failed":
		message.Title = "❌ " + label + " gagal"
		var found struct { Category string `json:"category"`; Summary string `json:"summary"` }
		_ = json.Unmarshal([]byte(rowText(rows[0], "diagnosis")), &found)
		detail := strings.TrimSpace(found.Summary)
		if detail == "" { detail = "buka Deployments untuk melihat penyebab dan saran perbaikannya." }
		message.Body = name + ": " + detail
	default:
		message.Title = "⚠️ " + label + " terhenti"
		message.Body = name + ": proses tidak selesai. Periksa hasilnya sebelum mengulang."
	}
	webpush.Notify(message)
}

// handlePushWatch is the once-per-15-minutes periodic upkeep: it checks
// application links for Vercel's 404 and a waiting Cloudflare zone
// confirmation (notifying subscribed devices), and sweeps leftover Vercel
// test projects — all independent of whether push notifications are set up.
func handlePushWatch(w http.ResponseWriter, r *http.Request) {
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	webpush.MarkWatch()
	webpush.Prune()
	renotifyRecentJobs()
	sweepOrphanTestProjects()
	checked := 0
	if webpush.Wants(webpush.EventApp404) {
		rows, err := d1.Query(`SELECT repo, name, display_name, app_url FROM services WHERE app_url != '' ORDER BY id LIMIT 40`)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		var wait sync.WaitGroup
		for _, row := range rows {
			repo, appURL := rowText(row, "repo"), rowText(row, "app_url")
			name := rowText(row, "display_name")
			if name == "" { name = rowText(row, "name") }
			if name == "" { name = repo }
			checked++
			wait.Add(1)
			go func(repo, name, appURL string) {
				defer wait.Done()
				broken, err := vercelapp.Platform404(appURL)
				if err != nil { return }
				key := "app404:" + strings.ToLower(repo) + ":" + appURL
				if !broken { webpush.Forget(key); return }
				webpush.Notify(webpush.Message{Event: webpush.EventApp404, Key: key, URL: "/projects", Tag: "app404-" + strings.ToLower(repo),
					Title: "⚠️ Aplikasi menampilkan 404", Body: name + " (" + appURL + ") menampilkan 404 Vercel. Buka Projects → menu kartu untuk memperbaikinya."})
			}(repo, name, appURL)
		}
		wait.Wait()
	}
	if webpush.Wants(webpush.EventConfirm) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		zoneID, zoneName, err := zonemanagement.PendingZone(ctx)
		cancel()
		if err == nil && zoneID != "" {
			// Remind at most once a week, like "Nanti" in the app.
			week := strconv.FormatInt(time.Now().Unix()/604800, 10)
			webpush.Notify(webpush.Message{Event: webpush.EventConfirm, Key: "confirm:zone:" + zoneID + ":" + week, URL: "/", Tag: "confirm-zone",
				Title: "🔔 Konfirmasi menunggu", Body: "Aktifkan metrik trafik Cloudflare untuk zona " + zoneName + "? Buka DevControl untuk menjawab."})
		}
	}
	// Pusat Keamanan patrol: compare the live state with its baseline.
	securitycenter.Patrol(r.Context())
	// Audit Aplikasi: audit one app whose last audit is older than a day
	// (round robin), within the time left in this run.
	audited := appaudit.RunScheduled(r.Context(), selfAuditTarget(r))
	util.JSON(w, http.StatusOK, map[string]int{"checked_apps": checked, "audited_apps": audited})
}

func enqueueDeployment(id, phase, ticket, branch, environment string) error {
	_, err := d1.Query(`INSERT INTO deployment_runner (id, phase, ticket, branch, environment, message)
		VALUES (?, ?, ?, ?, ?, 'ZIP tersimpan; runner Cloudflare akan melanjutkan proses')`,
		id, phase, ticket, branch, environment)
	if err != nil { return err }
	// Newly configured Cron Triggers can take up to 15 minutes to propagate.
	_, err = d1.Query(`UPDATE deployment_jobs SET lease_until = datetime('now', '+30 minutes') WHERE id = ?`, id)
	return err
}

type runnerJob struct {
	id, kind, target, phase, ticket, branch, environment string
	attempts int
}

func rowText(row map[string]interface{}, key string) string { value, _ := row[key].(string); return value }

func runnerStage(job runnerJob) int {
	if job.kind == "self_update" {
		switch job.phase { case "baseline", "start-test", "status": return 2; case "commit": return 3; default: return 4 }
	}
	switch job.phase {
	case "baseline", "vercel-test", "vercel-test-status": return 2
	case "github": return 3
	default: return 4
	}
}

func isStatusCheck(phase string) bool {
	return phase == "status" || phase == "production-status" ||
		phase == "vercel-test-status" || phase == "vercel-live-status"
}

func haltRunner(job runnerJob, message string, uncertain bool) { haltRunnerWithLog(job, message, "", uncertain) }

func haltRunnerWithLog(job runnerJob, message, buildLog string, uncertain bool) {
	// Diagnose before discarding: the snippet is read from the uploaded ZIP.
	found := recordDiagnosis(job.id, job.kind, job.target, runnerStage(job), message, buildLog)
	historyStatus := "Failed"
	if uncertain { historyStatus = "Interrupted" }
	recordHistory(job.id, historyStatus, found.Category+": "+found.Summary)
	if len(message) > 700 { message = message[:700] }
	status := "Failed"
	if uncertain { status = "Interrupted" }
	if !uncertain { updateDeploymentStage(job.id, runnerStage(job), "Failed") } else {
		_, _ = d1.Query(`UPDATE deployment_jobs SET status = 'Interrupted', updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = 'Running'`, job.id)
		notifyJobResult(job.id)
	}
	_, _ = d1.Query(`UPDATE deployment_runner SET phase = 'error', message = ?, claim_until = CURRENT_TIMESTAMP,
		updated_at = CURRENT_TIMESTAMP WHERE id = ?`, message, job.id)
	// A failed run never keeps its ZIP; the last successful ZIP stays current.
	if store, err := archive.New(); err == nil { _ = store.Discard(job.id) }
	logLiveLog("ERROR", fmt.Sprintf("Deployment %s %s: %s (%s)", job.kind, job.target, message, status))
}

func invokeRunnerStep(job runnerJob, zipBytes []byte) (int, []byte, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	values := map[string]string{"phase": job.phase, "ticket": job.ticket, "archive_id": job.id}
	if job.kind == "self_update" {
		values["branch"] = job.branch
		values["repo"] = strings.TrimSuffix(job.target, "@"+job.branch)
	} else {
		values["type"] = job.kind
		values["name"] = job.target
		values["branch"] = job.branch
		values["environment"] = job.environment
	}
	for key, value := range values {
		if err := form.WriteField(key, value); err != nil { return 0, nil, err }
	}
	if len(zipBytes) > 0 {
		part, err := form.CreateFormFile("zip", "source.zip")
		if err != nil { return 0, nil, err }
		if _, err = part.Write(zipBytes); err != nil { return 0, nil, err }
	}
	if err := form.Close(); err != nil { return 0, nil, err }
	req, err := http.NewRequest(http.MethodPost, "https://internal/api/deployment-runner", &body)
	if err != nil { return 0, nil, err }
	req.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	if job.kind == "self_update" { handleSelfUpdate(response, req) } else { handleTriggerDeployment(response, req) }
	return response.Code, response.Body.Bytes(), nil
}

func runDeploymentJob(job runnerJob) error {
	rows, err := d1.Query(`UPDATE deployment_runner SET claim_until = datetime('now', '+90 seconds'),
		attempts = attempts + 1 WHERE id = ? AND phase = ? AND claim_until <= CURRENT_TIMESTAMP
		RETURNING attempts`, job.id, job.phase)
	if err != nil || len(rows) == 0 { return err }
	if count, ok := rows[0]["attempts"].(float64); ok { job.attempts = int(count) }
	// If the previous function died during a side effect, blindly repeating
	// GitHub commits or Vercel deployments would be unsafe. A read-only status
	// check can safely be retried, while a mutating step needs inspection.
	if job.attempts > 1 && !isStatusCheck(job.phase) {
		haltRunner(job, "Respons tahap sebelumnya tidak diketahui; periksa GitHub dan Vercel sebelum mengulang.", true)
		return nil
	}
	if job.attempts > 10 {
		haltRunner(job, "Status Vercel gagal diperiksa berulang kali; periksa GitHub dan Vercel.", true)
		return nil
	}
	var zipBytes []byte
	if !isStatusCheck(job.phase) {
		store, storeErr := archive.New()
		if storeErr != nil { haltRunner(job, storeErr.Error(), false); return nil }
		record, getErr := store.Get(job.id)
		if getErr != nil { haltRunner(job, getErr.Error(), false); return nil }
		if (job.kind == "self_update" && (record.Scope != "self" || record.Target != job.target)) ||
			(job.kind != "self_update" && (record.Scope != "app" || record.Target != job.target)) {
			haltRunner(job, "ZIP tidak cocok dengan target deployment", false); return nil
		}
		zipBytes, err = store.Download(record)
		if err != nil { haltRunner(job, "ZIP tersimpan tidak dapat dibaca: "+err.Error(), false); return nil }
	}
	code, data, err := invokeRunnerStep(job, zipBytes)
	if err != nil { haltRunner(job, err.Error(), !isStatusCheck(job.phase)); return nil }
	var result struct {
		OK bool `json:"ok"`
		Step string `json:"step"`
		Status string `json:"status"`
		Ticket string `json:"ticket"`
		Message string `json:"message"`
		BuildLog string `json:"build_log"`
	}
	if err = json.Unmarshal(data, &result); err != nil || code != http.StatusOK || result.Step == "" {
		message := fmt.Sprintf("Runner tidak menerima respons tahap %s (HTTP %d)", job.phase, code)
		if err == nil { var failure struct { Error string `json:"error"` }; _ = json.Unmarshal(data, &failure); if failure.Error != "" { message += ": " + failure.Error } }
		if isStatusCheck(job.phase) {
			_, _ = d1.Query(`UPDATE deployment_runner SET claim_until = CURRENT_TIMESTAMP, message = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, message, job.id)
			return nil
		}
		haltRunner(job, message, true)
		return nil
	}
	if !result.OK { haltRunnerWithLog(job, result.Message, result.BuildLog, false); return nil }
	next := job.phase
	if job.kind == "self_update" {
		switch job.phase {
		case "baseline": next = "start-test"
		case "start-test": next = "status"
		case "status": if result.Status == "ready" { next = "commit" }
		case "commit": next = "production-status"
		case "production-status": if result.Step == "done" { next = "done" }
		}
	} else {
		switch job.phase {
		case "baseline": next = "vercel-test"
		case "vercel-test": next = "vercel-test-status"
		case "vercel-test-status": if result.Status == "ready" { next = "github" }
		case "github": next = "vercel-live"
		case "vercel-live": next = "vercel-live-status"
		case "vercel-live-status": if result.Step == "done" { next = "done" }
		}
	}
	if result.Ticket == "" && next != job.phase && next != "done" && job.phase != "baseline" {
		haltRunner(job, "Sesi tahap berikutnya tidak tersedia; periksa status Vercel.", true); return nil
	}
	if result.Ticket == "" { result.Ticket = job.ticket }
	if len(result.Message) > 700 { result.Message = result.Message[:700] }
	_, err = d1.Query(`UPDATE deployment_runner SET phase = ?, ticket = ?, message = ?, attempts = 0,
		claim_until = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND phase = ?`,
		next, result.Ticket, result.Message, job.id, job.phase)
	return err
}

func handleDeploymentRunner(w http.ResponseWriter, _ *http.Request) {
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	if err := expireDeploymentJobs(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	sweepArchives()
	rows, err := d1.Query(`SELECT j.id, j.kind, j.target, r.phase, r.ticket, r.branch, r.environment
		FROM deployment_runner r JOIN deployment_jobs j ON j.id = r.id
		WHERE j.status = 'Running' AND r.phase NOT IN ('done', 'error')
		AND r.claim_until <= CURRENT_TIMESTAMP ORDER BY r.updated_at ASC LIMIT 1`)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	count := 0
	for _, row := range rows {
		job := runnerJob{id: rowText(row, "id"), kind: rowText(row, "kind"), target: rowText(row, "target"),
			phase: rowText(row, "phase"), ticket: rowText(row, "ticket"), branch: rowText(row, "branch"), environment: rowText(row, "environment")}
		if job.id == "" || job.phase == "" { continue }
		if err := runDeploymentJob(job); err != nil {
			util.Error(w, http.StatusBadGateway, fmt.Errorf("runner tidak dapat menyimpan status: %w", err)); return
		}
		count++
	}
	util.JSON(w, http.StatusOK, map[string]int{"processed": count})
}

// GET /api/services -> services status table.
func handleServices(w http.ResponseWriter, r *http.Request) {
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	rows, err := d1.Query(`
		SELECT id, name, display_name, status, uptime, version, repo, branch, app_url
		FROM services
		ORDER BY id ASC
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}
	util.JSON(w, http.StatusOK, rows)
}

// App repair is separate from ZIP updates: a standard repair can reuse a
// healthy Git import; an explicitly confirmed reimport creates a new web
// project and cleans up the old one only after the replacement is online.
type repairService struct { Name, Repo, Branch, URL string }

func readRepairService(repo string) (repairService, error) {
	owner, name, ok := splitRepo(repo)
	if !ok || owner == "" || name == "" || strings.ContainsAny(repo, " ?#\\\n\r\t") || len(repo) > 200 {
		return repairService{}, fmt.Errorf("nama repo tidak valid")
	}
	rows, err := d1.Query(`SELECT name, repo, branch, app_url FROM services WHERE lower(repo) = lower(?) LIMIT 2`, repo)
	if err != nil { return repairService{}, err }
	if len(rows) != 1 { return repairService{}, fmt.Errorf("repo tidak terhubung tepat ke satu aplikasi tersimpan") }
	row := rows[0]
	service := repairService{}
	service.Name, _ = row["name"].(string)
	service.Repo, _ = row["repo"].(string)
	service.Branch, _ = row["branch"].(string)
	service.URL, _ = row["app_url"].(string)
	if service.Name == "" || service.Repo == "" || service.URL == "" { return repairService{}, fmt.Errorf("aplikasi belum memiliki repo dan tautan production tersimpan") }
	return service, nil
}

func saveRepairedURL(service repairService, newURL string) error {
	if _, err := d1.Query(`UPDATE services SET app_url = ?, status = 'Healthy' WHERE lower(repo) = lower(?) AND app_url = ?`, newURL, service.Repo, service.URL); err != nil { return err }
	rows, err := d1.Query(`SELECT app_url FROM services WHERE lower(repo) = lower(?) LIMIT 1`, service.Repo)
	if err != nil { return err }
	if len(rows) != 1 || rows[0]["app_url"] != newURL { return fmt.Errorf("tautan aplikasi berubah selama pemulihan; muat ulang halaman") }
	logActivity("Tautan aplikasi dipulihkan", service.Repo+" → "+newURL, "check")
	return nil
}

func githubRepairRoot(token, owner, repo, branch string) (string, string, string, error) {
	sha, err := currentGithubBranchSHA(token, owner, repo, branch)
	if err != nil { return "", "", "", err }
	client := &http.Client{Timeout: 15 * time.Second}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	_, entries, truncated, err := githubCommitTree(client, token, baseURL, sha)
	if err != nil { return "", "", "", err }
	if truncated { return "", "", "", fmt.Errorf("daftar file GitHub terpotong; Root Directory tidak dapat dipastikan") }
	files := []vercelapp.File{}
	manifests := 0
	for _, entry := range entries {
		if entry.Type != "blob" { continue }
		if path.Base(entry.Path) == "index.html" { files = append(files, vercelapp.File{Path: entry.Path}) }
		if path.Base(entry.Path) != "package.json" { continue }
		manifests++
		if manifests > 12 { return "", "", "", fmt.Errorf("terlalu banyak package.json; Root Directory tidak dapat dipastikan") }
		var blob struct { Content string `json:"content"`; Encoding string `json:"encoding"`; Size int `json:"size"` }
		if _, err := githubJSONRequest(client, token, http.MethodGet, baseURL+"/git/blobs/"+url.PathEscape(entry.SHA), nil, &blob); err != nil { return "", "", "", err }
		if blob.Encoding != "base64" || blob.Size > 256<<10 { return "", "", "", fmt.Errorf("package.json tidak dapat dibaca dengan aman") }
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
		if err != nil { return "", "", "", err }
		files = append(files, vercelapp.File{Path: entry.Path, Data: data})
	}
	root, err := vercelapp.WebRoot(files)
	if err != nil { return "", "", "", err }
	if !vercelapp.ExpectsHomePage(vercelapp.FilesAtRoot(files, root)) {
		return "", "", "", fmt.Errorf("repo tidak memiliki halaman web yang dapat dipastikan")
	}
	framework := vercelapp.DetectFramework(vercelapp.FilesAtRoot(files, root))
	return sha, root, framework, nil
}

func isVercelAppURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") && strings.HasSuffix(strings.ToLower(u.Hostname()), ".vercel.app")
}

func verifiedRepairURL(client *vercelapp.Client, deploymentID string, candidates []string) string {
	checked := 0
	for _, candidate := range candidates {
		if !isVercelAppURL(candidate) { continue }
		checked++
		if checked > 2 { break }
		if vercelapp.CheckHomePage(candidate) != nil { continue }
		aliasID, err := client.DeploymentIDForURL(candidate)
		if err == nil && aliasID == deploymentID { return candidate }
	}
	return ""
}

func selectRepairProject(client *vercelapp.Client, service repairService, owner, repo, root string, linked []vercelapp.Project) (vercelapp.Project, error) {
	// The saved URL is the strongest identity hint, but never override a
	// project whose root explicitly belongs to the API half of a monorepo.
	if projectID, err := client.ProjectIDForURL(service.URL); err == nil {
		for _, project := range linked {
			if project.ID == projectID && (project.RootDirectory == root || (project.RootDirectory == "" && project.Name == vercelAppProjectName(owner, repo))) {
				return clientRepairProject(client, project, owner, repo)
			}
		}
	}
	exact := []vercelapp.Project{}
	legacy := []vercelapp.Project{}
	for _, project := range linked {
		if project.RootDirectory == root { exact = append(exact, project) }
		if project.RootDirectory == "" { legacy = append(legacy, project) }
	}
	if len(exact) == 1 { return clientRepairProject(client, exact[0], owner, repo) }
	if len(exact) > 1 { return vercelapp.Project{}, fmt.Errorf("beberapa project Vercel memakai Root Directory %q; pemulihan otomatis tidak bisa menentukan target", root) }
	if len(legacy) == 1 && legacy[0].Name == vercelAppProjectName(owner, repo) { return clientRepairProject(client, legacy[0], owner, repo) }
	return vercelapp.Project{}, fmt.Errorf("project Vercel yang terhubung ke repo %s tidak dapat diidentifikasi; periksa VERCEL_TEAM_ID dan koneksi Git", service.Repo)
}

func clientRepairProject(client *vercelapp.Client, project vercelapp.Project, owner, repo string) (vercelapp.Project, error) {
	full, found, err := client.GetProject(project.ID)
	if err != nil { return full, err }
	if !found || !full.LinkedTo(owner, repo) { return vercelapp.Project{}, fmt.Errorf("koneksi Git project Vercel berubah saat diperiksa") }
	return full, nil
}

// Never infer that a same-repo project is the web project: monorepos may
// have a second Vercel project for their API. A legacy empty root is accepted
// only under DevControl's stable app name and with a matching Git link or a
// direct proof that the saved URL belongs to that project.
func safeReimportSource(project vercelapp.Project, service repairService, owner, repo, root string, urlOwned bool) bool {
	if project.ID == "" || project.ID == os.Getenv("VERCEL_PROJECT_ID") || project.Name == "" { return false }
	stable := vercelAppProjectName(owner, repo)
	if project.RootDirectory != root && !(project.RootDirectory == "" && project.Name == stable) { return false }
	if project.LinkType != "" && !project.LinkedTo(owner, repo) { return false }
	if !project.LinkedTo(owner, repo) && !urlOwned && !(project.Name == stable && strings.EqualFold(strings.TrimSuffix(service.URL, "/"), "https://"+stable+".vercel.app")) { return false }
	// A web root shared with the current DevControl installation must not be
	// removed even if its name accidentally resembles the app's name.
	return true
}

func selectReimportSource(client *vercelapp.Client, service repairService, owner, repo, root string) (vercelapp.Project, error) {
	if id, err := client.ProjectIDForURL(service.URL); err == nil {
		project, found, getErr := client.GetProject(id)
		if getErr != nil { return vercelapp.Project{}, getErr }
		if found && safeReimportSource(project, service, owner, repo, root, true) { return project, nil }
		return vercelapp.Project{}, fmt.Errorf("URL rusak menunjuk project Vercel lain atau root API; project tidak akan dihapus")
	}
	// A platform 404 often has no deployment lookup. The stable project name
	// is then a fallback, with the same root and Git ownership checks.
	project, found, err := client.GetProject(vercelAppProjectName(owner, repo))
	if err != nil { return vercelapp.Project{}, err }
	if found {
		owned := strings.EqualFold(strings.TrimSuffix(service.URL, "/"), "https://"+project.Name+".vercel.app")
		if !owned {
			owned, err = client.ProjectHasDeploymentURL(project.ID, service.URL)
			if err != nil { return vercelapp.Project{}, fmt.Errorf("kepemilikan URL project lama tidak dapat diperiksa: %w", err) }
		}
		if owned && safeReimportSource(project, service, owner, repo, root, true) { return project, nil }
	}
	linked, err := client.FindLinkedProjects(owner, repo)
	if err != nil { return vercelapp.Project{}, err }
	parsed, _ := url.Parse(service.URL)
	var candidate vercelapp.Project
	for _, match := range linked {
		if match.RootDirectory != root || !strings.EqualFold(parsed.Hostname(), match.Name+".vercel.app") { continue }
		if candidate.ID != "" { return vercelapp.Project{}, fmt.Errorf("beberapa project web cocok dengan URL rusak; target penghapusan tidak pasti") }
		candidate = match
	}
	if candidate.ID != "" {
		full, exists, getErr := client.GetProject(candidate.ID)
		if getErr != nil { return vercelapp.Project{}, getErr }
		if exists && safeReimportSource(full, service, owner, repo, root, true) { return full, nil }
	}
	return vercelapp.Project{}, fmt.Errorf("project web lama tidak dapat dibuktikan dari URL atau nama project; periksa koneksi Git dan VERCEL_TEAM_ID. Project API tidak disentuh")
}

func reimportProjectName(owner, repo string) string {
	base := vercelAppProjectName(owner, repo)
	suffix := fmt.Sprintf("-web-%d", time.Now().UnixNano())
	if len(base)+len(suffix) > 80 { base = strings.Trim(base[:80-len(suffix)], "-") }
	return base+suffix
}

func validAPIURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil { return false }
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") { return false }
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()) { return false }
	return u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == ""
}

func startWebReimport(w http.ResponseWriter, client *vercelapp.Client, token string, service repairService, owner, repo, branch, sha, root, framework, apiURL string) {
	source, err := selectReimportSource(client, service, owner, repo, root)
	if err != nil { util.Error(w, http.StatusConflict, err); return }
	if domains, err := client.CustomDomains(source.ID); err != nil {
		util.Error(w, http.StatusBadGateway, fmt.Errorf("domain project lama belum dapat diperiksa: %w", err)); return
	} else if len(domains) != 0 {
		util.Error(w, http.StatusConflict, fmt.Errorf("project web lama memiliki domain khusus; pindahkan domain tersebut secara manual terlebih dahulu agar tidak terputus saat project dihapus")); return
	}
	variables, err := client.ReadProjectEnvironment(source.ID)
	if err != nil { util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("variabel project web lama belum dapat disalin: %w", err)); return }
	if apiURL != "" {
		if !validAPIURL(apiURL) { util.Error(w, http.StatusBadRequest, fmt.Errorf("API_URL harus URL HTTPS publik tanpa path atau parameter")); return }
		found := false
		for index := range variables { if variables[index].Key == "API_URL" { variables[index].Value = apiURL; found = true } }
		if !found { variables = append(variables, vercelapp.ProjectEnvironment{Key:"API_URL", Value:apiURL, Type:"encrypted", Target:[]string{"production", "preview", "development"}}) }
	}
	name := reimportProjectName(owner, repo)
	replacement, err := client.CreateProject(name, framework, root, "")
	if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("project pengganti tidak dapat dibuat: %w", err)); return }
	// Every error until the signed ticket is issued leaves the old project and
	// D1 URL untouched; a partially made replacement is disposable.
	complete := false
	defer func() { if !complete { if cleanErr := client.DeleteProject(replacement.ID); cleanErr != nil { logLiveLog("WARN", "Gagal membersihkan project pengganti "+replacement.Name+": "+cleanErr.Error()) } } }()
	if err := client.CopyProjectEnvironment(replacement.ID, variables); err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("variabel tidak dapat disalin ke project pengganti: %w", err)); return }
	if err := client.LinkProject(replacement.ID, service.Repo); err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("project pengganti tidak dapat dihubungkan ke GitHub: %w", err)); return }
	deployment, err := client.CreateGitDeployment(replacement.Name, replacement.ID, owner, repo, branch, sha)
	if err != nil {
		// Linking the repo can start the identical production build itself.
		// Only adopt that build when its Git SHA matches the requested commit.
		id, lookupErr := client.LatestProduction(replacement.ID, sha)
		if lookupErr != nil || id == "" { util.Error(w, http.StatusBadGateway, fmt.Errorf("commit Git tidak dapat dideploy pada project baru: %w", err)); return }
		deployment.ID = id
	}
	ticket, err := signBuildTicket(token, buildTicket{Stage:"app-reimport", Repo:service.Repo, Name:service.Name, Branch:branch, CommitSHA:sha, URL:service.URL, Project:replacement.Name, ProjectID:replacement.ID, OldProjectID:source.ID, DeploymentID:deployment.ID, RootDirectory:root, Framework:framework})
	if err != nil { util.Error(w, http.StatusInternalServerError, err); return }
	complete = true
	logLiveLog("INFO", "Impor ulang web "+service.Repo+": commit "+sha+" dibangun pada project pengganti "+replacement.Name)
	util.JSON(w, http.StatusOK, map[string]interface{}{"status":"pending", "ticket":ticket, "message":"Project web pengganti dibuat, variabel disalin, dan commit Git sedang dibangun. Project lama tetap tersedia sampai halaman baru sehat"})
}

func finishWebReimport(w http.ResponseWriter, client *vercelapp.Client, service repairService, ticket buildTicket, liveURL string) {
	if service.URL != ticket.URL && service.URL != liveURL { util.Error(w, http.StatusConflict, fmt.Errorf("tautan aplikasi berubah selama impor ulang; project lama dipertahankan")); return }
	if service.URL == ticket.URL {
		if err := saveRepairedURL(service, liveURL); err != nil { util.Error(w, http.StatusConflict, err); return }
	}
	message := "Aplikasi online pada project web baru; tautan tersimpan diperbarui. "
	owner, repo, ok := splitRepo(ticket.Repo)
	if !ok || ticket.OldProjectID == ticket.ProjectID || ticket.OldProjectID == os.Getenv("VERCEL_PROJECT_ID") {
		util.JSON(w, http.StatusOK, map[string]interface{}{"status":"ready", "app_url":liveURL, "message":message+"Project lama dipertahankan karena identitasnya tidak aman untuk dihapus"}); return
	}
	old, found, err := client.GetProject(ticket.OldProjectID)
	if err == nil && found && safeReimportSource(old, repairService{Repo:service.Repo, URL:ticket.URL}, owner, repo, ticket.RootDirectory, true) {
		var domains []string
		domains, err = client.CustomDomains(old.ID)
		if err == nil && len(domains) != 0 { err = fmt.Errorf("project lama masih memiliki domain khusus") }
		if err == nil { err = client.DeleteProject(old.ID) }
	} else if err == nil && found { err = fmt.Errorf("identitas atau root project lama berubah") }
	if err != nil { message += "Project web lama tetap ada: " + err.Error() } else { message += "Project web lama berhasil dihapus; project API tetap ada" }
	logLiveLog("INFO", "Impor ulang web "+service.Repo+": "+message)
	util.JSON(w, http.StatusOK, map[string]interface{}{"status":"ready", "app_url":liveURL, "message":message})
}

func handleAppRepair(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("pemulihan aplikasi hanya untuk owner/admin")); return }
	if r.Method != http.MethodGet && r.Method != http.MethodPost { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST")); return }
	vercelToken := os.Getenv("VERCEL_TOKEN")
	if r.Method == http.MethodGet && r.URL.Query().Get("ticket") != "" {
		ticket, err := verifyBuildTicket(vercelToken, r.URL.Query().Get("ticket"))
		if vercelToken == "" || err != nil || (ticket.Stage != "app-repair" && ticket.Stage != "app-reimport") || ticket.Repo == "" || ticket.URL == "" || ticket.DeploymentID == "" || ticket.ProjectID == "" || (ticket.Stage == "app-reimport" && ticket.OldProjectID == "") {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("sesi pemulihan tidak valid atau kedaluwarsa")); return
		}
		service, err := readRepairService(ticket.Repo)
		if err != nil || (service.URL != ticket.URL && ticket.Stage != "app-reimport") { util.Error(w, http.StatusConflict, fmt.Errorf("tautan aplikasi berubah selama pemulihan; muat ulang halaman")); return }
		state, urls, detail, err := getVercelBuildStatus(vercelToken, ticket.DeploymentID)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if state == "ERROR" || state == "CANCELED" {
			if detail == "" {
				if buildLog, logErr := getVercelBuildLog(vercelToken, ticket.DeploymentID); logErr == nil { detail = vercelFailureSummary(buildLog) }
			}
			if ticket.Stage == "app-reimport" && service.URL == ticket.URL {
				client := vercelapp.New(vercelToken)
				owner, repo, ok := splitRepo(ticket.Repo)
				if replacement, found, getErr := client.GetProject(ticket.ProjectID); getErr == nil && found && ok && replacement.LinkedTo(owner, repo) && replacement.RootDirectory == ticket.RootDirectory {
					if cleanErr := client.DeleteProject(replacement.ID); cleanErr != nil { logLiveLog("WARN", "Project web pengganti yang gagal belum dapat dibersihkan: "+cleanErr.Error()) }
				}
			}
			logLiveLog("ERROR", "Pemulihan "+service.Repo+": build Vercel "+state+" "+detail)
			util.JSON(w, http.StatusOK, map[string]interface{}{"status":"failed", "message":"Build pemulihan Vercel "+state+": "+orDefault(detail, "periksa Build Logs di project Vercel")+". Project web lama dan tautan lama tetap ada"}); return
		}
		if state != "READY" { util.JSON(w, http.StatusOK, map[string]interface{}{"status":"pending", "message":"Build Vercel: "+state}); return }
		client := vercelapp.New(vercelToken)
		owner, repo, repoOK := splitRepo(service.Repo)
		if current, found, err := client.GetProject(ticket.ProjectID); err != nil || !found || !repoOK || !current.LinkedTo(owner, repo) || (ticket.Stage == "app-reimport" && current.RootDirectory != ticket.RootDirectory) {
			util.Error(w, http.StatusConflict, fmt.Errorf("project Vercel tidak lagi terhubung ke repo aplikasi")); return
		}
		liveURL := verifiedRepairURL(client, ticket.DeploymentID, urls)
		if liveURL == "" {
			if ticket.Stage == "app-reimport" && time.Now().Unix()-ticket.CreatedAt < 10*60 {
				util.JSON(w, http.StatusOK, map[string]interface{}{"status":"pending", "message":"Build READY; menunggu tautan project baru dapat dibuka dan terikat pada deployment yang benar. Project lama belum dihapus"}); return
			}
			util.JSON(w, http.StatusOK, map[string]interface{}{"status":"failed", "message":"Build READY, tetapi URL belum melayani halaman web yang terverifikasi; periksa Root Directory, variabel API_URL, dan Deployment Protection di Vercel. Tautan lama tetap tersimpan."}); return
		}
		if ticket.Stage == "app-reimport" { finishWebReimport(w, client, service, ticket, liveURL); return }
		if err := saveRepairedURL(service, liveURL); err != nil { util.Error(w, http.StatusConflict, err); return }
		util.JSON(w, http.StatusOK, map[string]interface{}{"status":"ready", "app_url":liveURL, "message":"Aplikasi sudah online; tautan tersimpan diperbarui"})
		return
	}
	service, err := readRepairService(r.URL.Query().Get("repo"))
	if err != nil {
		if r.Method == http.MethodGet { util.JSON(w, http.StatusOK, map[string]bool{"broken":false}); return }
		util.Error(w, http.StatusNotFound, err); return
	}
	broken, probeErr := vercelapp.Platform404(service.URL)
	if r.Method == http.MethodGet {
		util.JSON(w, http.StatusOK, map[string]interface{}{"broken":broken && probeErr == nil, "app_url":service.URL}); return
	}
	var input struct { Action string `json:"action"`; Confirmation string `json:"confirmation"`; APIURL string `json:"api_url"` }
	if r.Body != nil && r.Body != http.NoBody && (r.ContentLength != 0 || len(r.TransferEncoding) != 0) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") { util.Error(w, http.StatusUnsupportedMediaType, fmt.Errorf("permintaan harus berupa JSON")); return }
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil { util.Error(w, http.StatusBadRequest, fmt.Errorf("data pemulihan tidak valid: %w", err)); return }
		var trailing interface{}
		if err := decoder.Decode(&trailing); err != io.EOF { util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan pemulihan berisi data tambahan")); return }
	}
	if input.Action != "" && input.Action != "reimport" { util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi pemulihan tidak dikenal")); return }
	if input.Action == "reimport" && !strings.EqualFold(input.Confirmation, service.Repo) { util.Error(w, http.StatusBadRequest, fmt.Errorf("ketik nama lengkap repo untuk mengonfirmasi impor ulang project web")); return }
	if input.Action != "reimport" && input.APIURL != "" { util.Error(w, http.StatusBadRequest, fmt.Errorf("API_URL hanya berlaku saat impor ulang")); return }
	if probeErr != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("URL belum dapat diverifikasi: %w", probeErr)); return }
	if !broken { util.Error(w, http.StatusConflict, fmt.Errorf("tautan tidak lagi menampilkan 404 Vercel; muat ulang halaman")); return }
	active, err := d1.Query(`SELECT id FROM deployment_jobs WHERE lower(lock_key) = lower(?) AND status = 'Running' LIMIT 1`, service.Repo)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	if len(active) != 0 { util.Error(w, http.StatusConflict, fmt.Errorf("repo sedang diproses oleh deployment lain; tunggu hingga selesai")); return }
	githubToken := os.Getenv("GITHUB_TOKEN")
	if vercelToken == "" || githubToken == "" { util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("GITHUB_TOKEN dan VERCEL_TOKEN diperlukan untuk pemulihan")); return }
	owner, repo, _ := splitRepo(service.Repo)
	branch := orDefault(service.Branch, "main")
	sha, root, framework, err := githubRepairRoot(githubToken, owner, repo, branch)
	if err != nil { util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("repo GitHub belum dapat dianalisis: %w", err)); return }
	client := vercelapp.New(vercelToken)
	if input.Action == "reimport" { startWebReimport(w, client, vercelToken, service, owner, repo, branch, sha, root, framework, strings.TrimSpace(input.APIURL)); return }
	linked, err := client.FindLinkedProjects(owner, repo)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	// An already healthy manual import is the best repair: only switch the
	// saved link, without redeploying or touching either Vercel project.
	exact := []vercelapp.Project{}
	for _, project := range linked { if project.RootDirectory == root { exact = append(exact, project) } }
	if len(exact) > 1 { util.Error(w, http.StatusConflict, fmt.Errorf("beberapa project Vercel memakai root %q; target belum pasti", root)); return }
	if len(exact) == 1 {
		project, checkErr := clientRepairProject(client, exact[0], owner, repo)
		if checkErr != nil { util.Error(w, http.StatusBadGateway, checkErr); return }
		id, lookupErr := client.LatestProduction(project.ID)
		if lookupErr != nil { util.Error(w, http.StatusBadGateway, lookupErr); return }
		if id != "" {
			state, urls, _, statusErr := getVercelBuildStatus(vercelToken, id)
			if statusErr != nil { util.Error(w, http.StatusBadGateway, statusErr); return }
			if state == "READY" {
				if liveURL := verifiedRepairURL(client, id, urls); liveURL != "" {
					if err := saveRepairedURL(service, liveURL); err != nil { util.Error(w, http.StatusConflict, err); return }
					util.JSON(w, http.StatusOK, map[string]interface{}{"status":"ready", "app_url":liveURL, "message":"Project import manual sudah sehat; tautan diperbaiki tanpa deployment baru"}); return
				}
				platformBroken := false
				for _, candidate := range urls {
					if !isVercelAppURL(candidate) { continue }
					bad, probeErr := vercelapp.Platform404(candidate)
					if probeErr == nil && bad { platformBroken = true }
					break
				}
				if !platformBroken {
					util.Error(w, http.StatusConflict, fmt.Errorf("project web %s sudah READY, tetapi halaman production tidak dapat diverifikasi sebagai web sehat atau 404 Vercel; periksa Deployment Protection dan Runtime Logs sebelum mengubah project", project.Name)); return
				}
			}
		}
	}
	project, err := selectRepairProject(client, service, owner, repo, root, linked)
	if err != nil { util.Error(w, http.StatusConflict, err); return }
	if _, err := client.AlignBuildSettings(project, framework, root, true); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	deployment, err := client.CreateGitDeployment(project.Name, project.ID, owner, repo, branch, sha)
	if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("commit Git tidak dapat dideploy: %w", err)); return }
	ticket, err := signBuildTicket(vercelToken, buildTicket{Stage:"app-repair", Repo:service.Repo, Name:service.Name, Branch:branch, CommitSHA:sha, URL:service.URL, Project:project.Name, ProjectID:project.ID, DeploymentID:deployment.ID, RootDirectory:root, Framework:framework})
	if err != nil { util.Error(w, http.StatusInternalServerError, err); return }
	logLiveLog("INFO", "Pemulihan 404 "+service.Repo+": membangun ulang commit "+sha+" pada project "+project.Name)
	util.JSON(w, http.StatusOK, map[string]interface{}{"status":"pending", "ticket":ticket, "message":"Root Directory disesuaikan dan commit Git yang sudah ada sedang dibangun ulang di Vercel"})
}

// GET /api/activity -> recent activity feed (latest 10).
func handleActivity(w http.ResponseWriter, r *http.Request) {
	rows, err := d1.Query(`
		SELECT id, title, description, icon, created_at
		FROM activity_log
		ORDER BY id DESC
		LIMIT 10
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}
	util.JSON(w, http.StatusOK, rows)
}

// GET /api/logs -> live log stream, oldest-first (latest 50).
func handleLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := d1.Query(`
		SELECT id, level, message, created_at
		FROM live_logs
		ORDER BY id DESC
		LIMIT 50
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	util.JSON(w, http.StatusOK, rows)
}

// GET /api/performance -> API response time / request volume / error rate.
func handlePerformance(w http.ResponseWriter, r *http.Request) {
	result, err := apimanagement.GetPerformance(r)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	util.JSON(w, http.StatusOK, result)
}

// Database setup runs only on an admin action or before the first ZIP stage.
func handleDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Read-only table browser: ?view=tables and ?table=<name>.
	if databrowser.Handle(w, r) { return }
	// GitHub repo vs Vercel project inventory: ?view=inventory (GET list, POST connect/delete).
	if repoinventory.Handle(w, r, vercelAppProjectName) { return }
	if r.Method == http.MethodGet {
		status, err := setup.Inspect()
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		result := map[string]interface{}{"d1": status, "r2_configured": false, "r2_exists": false}
		store, storeErr := archive.New()
		if storeErr != nil { result["r2_error"] = storeErr.Error() } else {
			result["r2_configured"] = true
			found, checkErr := store.BucketStatus()
			if checkErr != nil { result["r2_error"] = checkErr.Error() } else { result["r2_exists"] = found }
		}
		util.JSON(w, http.StatusOK, result)
		return
	}
	if r.Method != http.MethodPost { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST")); return }
	var input struct { Action string `json:"action"` }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.Action != "prepare" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi penyiapan tidak valid")); return
	}
	status, err := setup.Ensure()
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	store, err := archive.New()
	if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
	created, err := store.EnsureBucket()
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	_, _ = d1.Query(`INSERT INTO admin_audit_log (action, target) VALUES ('prepare_storage', 'D1/R2')`)
	util.JSON(w, http.StatusOK, map[string]interface{}{"d1": status, "r2_exists": true, "r2_created": created})
}

func prepareArchiveStorage() error {
	if err := setup.Prepare(); err != nil { return err }
	store, err := archive.New()
	if err != nil { return err }
	if _, err := store.EnsureBucket(); err != nil { return err }
	return nil
}

type infraSeries struct {
	Metric  string    `json:"metric"`
	Values  []float64 `json:"values"`
	Current *float64  `json:"current"`
	Unit    string    `json:"unit"`
	Source  string    `json:"source"`
	Note    string    `json:"note"`
}

var trafficCache struct {
	sync.Mutex
	zoneID    string
	fetchedAt time.Time
	network   infraSeries
	requests  infraSeries
}

// GET /api/health -> this Go instance plus HTTP traffic through the configured Cloudflare zone.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	series := make([]infraSeries, 0, 4)
	cpu := infraSeries{Metric: "cpu", Values: []float64{}, Unit: "%", Source: "Instans Go", Note: "Rata-rata penggunaan CPU proses Go sejak instans ini mulai"}
	samples := []metrics.Sample{
		{Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/cpu/classes/idle:cpu-seconds"},
	}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindFloat64 && samples[1].Value.Kind() == metrics.KindFloat64 {
		total, idle := samples[0].Value.Float64(), samples[1].Value.Float64()
		if total > 0 {
			used := math.Round(math.Max(0, math.Min(100, (total-idle)/total*100))*10) / 10
			cpu.Current = &used
		}
	}
	if cpu.Current == nil { cpu.Note = "Metrik CPU instans Go belum tersedia" }
	series = append(series, cpu)

	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	heapMiB := math.Round(float64(memory.HeapAlloc)/1024/1024*10) / 10
	series = append(series, infraSeries{Metric: "memory", Values: []float64{}, Current: &heapMiB, Unit: "MiB", Source: "Instans Go", Note: "Heap Go saat ini pada instans yang melayani permintaan"})

	zoneID, token := strings.TrimSpace(os.Getenv("CF_ZONE_ID")), strings.TrimSpace(os.Getenv("CF_API_TOKEN"))
	mode, _ := trafficmetrics.Mode()
	// An admin-approved zone is immediately active, even on a running Vercel
	// deployment whose environment snapshot predates the approval.
	if mode != "api" {
		if approved, err := zonemanagement.ApprovedZone(); err == nil && approved != "" { zoneID = approved }
	}
	network := infraSeries{Metric: "network", Values: []float64{}, Unit: "MiB", Source: "Cloudflare · zona", Note: "Pilih dan setujui zona Cloudflare di Pengaturan; token perlu izin Account Analytics Read"}
	requests := infraSeries{Metric: "requests", Values: []float64{}, Unit: "req", Source: "Cloudflare · zona", Note: network.Note}
	if mode == "api" {
		network, requests = apiTrafficSeries()
	} else if zoneID != "" && token != "" {
		network, requests = cachedCloudflareTraffic(r.Context(), zoneID, token)
	}
	series = append(series, network, requests)
	util.JSON(w, http.StatusOK, series)
}

func apiTrafficSeries() (infraSeries, infraSeries) {
	note := "Respons dan request yang ditangani Go API DevControl; tidak termasuk halaman atau aset CDN Vercel. Data per jam."
	network := infraSeries{Metric: "network", Values: []float64{}, Unit: "MiB", Source: "DevControl · API", Note: note}
	requests := infraSeries{Metric: "requests", Values: []float64{}, Unit: "req", Source: "DevControl · API", Note: note}
	bytesByHour, countByHour, err := trafficmetrics.Series()
	if err != nil {
		network.Note = "Pengukuran API belum tersedia; periksa koneksi D1."
		requests.Note = network.Note
		return network, requests
	}
	bytesTotal, countTotal := 0.0, 0.0
	for i, count := range bytesByHour {
		bytesTotal += count
		countTotal += countByHour[i]
		bytesByHour[i] = math.Round(count/1024/1024*10000) / 10000
	}
	bandwidth := math.Round(bytesTotal/1024/1024*1000) / 1000
	network.Current, network.Values = &bandwidth, bytesByHour
	requests.Current, requests.Values = &countTotal, countByHour
	return network, requests
}

func cachedCloudflareTraffic(ctx context.Context, zoneID, token string) (infraSeries, infraSeries) {
	trafficCache.Lock()
	defer trafficCache.Unlock()
	if trafficCache.zoneID == zoneID && time.Since(trafficCache.fetchedAt) < time.Minute {
		return trafficCache.network, trafficCache.requests
	}
	network := infraSeries{Metric: "network", Values: []float64{}, Unit: "MiB", Source: "Cloudflare · zona", Note: "Estimasi trafik semua host di zona, 24 jam terakhir"}
	requests := infraSeries{Metric: "requests", Values: []float64{}, Unit: "req", Source: "Cloudflare · zona", Note: network.Note}
	bytesByHour, countByHour, err := queryCloudflareTraffic(ctx, zoneID, token)
	if err != nil {
		network.Note = "Analitik Cloudflare tidak tersedia. Periksa CF_ZONE_ID dan izin Account Analytics Read"
		requests.Note = network.Note
	} else {
		bytesTotal, countTotal := 0.0, 0.0
		for i, bytes := range bytesByHour {
			bytesByHour[i] = math.Round(bytes/1024/1024*1000) / 1000
			bytesTotal += bytes
			countTotal += countByHour[i]
		}
		bandwidth := math.Round(bytesTotal/1024/1024*10) / 10
		network.Current, network.Values = &bandwidth, bytesByHour
		requests.Current, requests.Values = &countTotal, countByHour
	}
	if ctx.Err() == nil {
		trafficCache.zoneID, trafficCache.fetchedAt = zoneID, time.Now()
		trafficCache.network, trafficCache.requests = network, requests
	}
	return network, requests
}

func queryCloudflareTraffic(ctx context.Context, zoneID, token string) ([]float64, []float64, error) {
	const query = `query($zoneTag: string, $start: Time, $end: Time) {
		viewer { zones(filter: {zoneTag: $zoneTag}) {
			traffic: httpRequestsAdaptiveGroups(limit: 25, orderBy: [datetimeHour_ASC], filter: {
				datetime_geq: $start, datetime_lt: $end, requestSource: "eyeball"
			}) { count sum { edgeResponseBytes } dimensions { datetimeHour } }
		} }
	}`
	now := time.Now().UTC()
	start := now.Add(-24 * time.Hour)
	input, _ := json.Marshal(map[string]interface{}{
		"query": query,
		"variables": map[string]string{
			"zoneTag": zoneID,
			"start": start.Format(time.RFC3339),
			"end": now.Format(time.RFC3339),
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudflare.com/client/v4/graphql", bytes.NewReader(input))
	if err != nil { return nil, nil, err }
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 7 * time.Second}).Do(req)
	if err != nil { return nil, nil, err }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { return nil, nil, fmt.Errorf("Cloudflare Analytics HTTP %d", response.StatusCode) }
	var body struct {
		Errors []json.RawMessage `json:"errors"`
		Data struct {
			Viewer struct {
				Zones []struct {
					Traffic []struct {
						Count float64 `json:"count"`
						Sum struct {
							EdgeResponseBytes float64 `json:"edgeResponseBytes"`
						} `json:"sum"`
						Dimensions struct {
							DatetimeHour string `json:"datetimeHour"`
						} `json:"dimensions"`
					} `json:"traffic"`
				} `json:"zones"`
			} `json:"viewer"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body); err != nil { return nil, nil, err }
	if len(body.Errors) > 0 || len(body.Data.Viewer.Zones) == 0 {
		return nil, nil, fmt.Errorf("Cloudflare Analytics returned no accessible zone")
	}
	startHour := start.Truncate(time.Hour)
	bytesByHour, countByHour := make([]float64, 25), make([]float64, 25)
	for _, group := range body.Data.Viewer.Zones[0].Traffic {
		hour, err := time.Parse(time.RFC3339, group.Dimensions.DatetimeHour)
		if err != nil { return nil, nil, err }
		index := int(hour.Sub(startHour) / time.Hour)
		if index < 0 || index >= len(bytesByHour) { continue }
		bytesByHour[index] += group.Sum.EdgeResponseBytes
		countByHour[index] += group.Count
	}
	return bytesByHour, countByHour, nil
}

// --- merged from deploy.go (trigger-deployment) ---


// Keep multipart uploads under Vercel Functions' 4.5 MB request limit.
const maxDeployZipBytes = 4 << 20 // 4 MiB

// deployResult is returned to the browser after handleTriggerDeployment
// finishes (or aborts), mirroring selfUpdateResult below so the "Aplikasi
// Baru"/"Update Aplikasi" modal can show the same kind of step-by-step
// progress/failure reporting that "Update Diri" does.
type deployResult struct {
	Step    string `json:"step"` // "extract" | "vercel-test" | "github" | "vercel-live" | "done"
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Repo    string `json:"repo,omitempty"`
	AppURL  string `json:"app_url,omitempty"`
	Status  string `json:"status,omitempty"` // pending | ready; a pending build must never advance the pipeline
	Ticket  string `json:"ticket,omitempty"`
	ArchiveID string `json:"archive_id,omitempty"`
	BuildLog string `json:"build_log,omitempty"`
}

func deployStagePosition(phase string) int {
	switch phase {
	case "extract": return 1
	case "baseline", "vercel-test": return 2
	case "github": return 3
	default: return 4
	}
}

func appDeploymentLockKey(token, name string, isUpdate bool) (string, error) {
	repo := ""
	if isUpdate {
		rows, err := d1.Query(`SELECT repo FROM services WHERE name = ? LIMIT 1`, name)
		if err != nil { return "", err }
		if len(rows) == 0 { return "", fmt.Errorf("aplikasi %q tidak ditemukan", name) }
		repo, _ = rows[0]["repo"].(string)
	}
	if repo == "" {
		owner, err := fetchGithubUsername(token)
		if err != nil { return "", err }
		repo = owner + "/" + sanitizeGithubRepoName(name)
	}
	// Apps on different branches still share one Vercel project and one
	// current ZIP per app. Hold the repo lock until either rollout ends.
	return strings.ToLower(repo), nil
}

// handleTriggerDeployment implements the "Aplikasi Baru" and "Update
// Aplikasi" options in the New Deployment dropdown: POST multipart/form-data
// with `type` ("new_app"|"update_app"), `name`, an optional `branch` and
// `environment`, and a `zip` file — no repo field. There's nothing to type
// or pick: for "Aplikasi Baru" the target repo is created automatically
// (under the account behind GITHUB_TOKEN, named after the app) the first
// time it's deployed; for "Update Aplikasi" the repo is read back from the
// row that "Aplikasi Baru" recorded for that app (falling back to the same
// derivation if it's an older row from before repo tracking existed).
func handleTriggerDeployment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	vercelToken := os.Getenv("VERCEL_TOKEN")
	if githubToken == "" || vercelToken == "" {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf(
			"deployment belum dikonfigurasi: set GITHUB_TOKEN dan VERCEL_TOKEN di environment variables Vercel",
		))
		return
	}

	if err := r.ParseMultipartForm(maxDeployZipBytes); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("gagal membaca form: %w", err))
		return
	}

	deployType := strings.TrimSpace(r.FormValue("type")) // "new_app" | "update_app"
	phase := strings.TrimSpace(r.FormValue("phase"))
	if (deployType != "new_app" && deployType != "update_app") ||
		(phase != "extract" && phase != "baseline" && phase != "vercel-test" && phase != "vercel-test-status" && phase != "github" && phase != "vercel-live" && phase != "vercel-live-status") {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("jenis atau tahap deployment tidak valid"))
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	// An operator limited to chosen apps may only update those apps, and
	// cannot create new ones.
	if principal := auth.Current(r); principal != nil && principal.Role == auth.RoleOperator && principal.Apps != nil {
		if deployType == "new_app" {
			util.Error(w, http.StatusForbidden, fmt.Errorf("akun operator ini hanya boleh memperbarui aplikasi tertentu, tidak membuat aplikasi baru")); return
		}
		if !auth.CanUseApp(principal, name) {
			util.Error(w, http.StatusForbidden, fmt.Errorf("aplikasi %q di luar cakupan akun operator ini", name)); return
		}
	}
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	branchInput := strings.TrimSpace(r.FormValue("branch"))
	environment := orDefault(r.FormValue("environment"), "Production")
	isUpdate := deployType == "update_app"

	if name == "" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("nama aplikasi wajib diisi"))
		return
	}
	if deployType == "new_app" && phase == "extract" {
		if name != sanitizeGithubRepoName(name) || name == "." || name == ".." || len(name) > 100 {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("nama repo GitHub tidak valid (maksimal 100 karakter; gunakan huruf, angka, titik, garis bawah, atau tanda hubung)"))
			return
		}
		// Older clients did not send a display name; keep their current behavior.
		if displayName == "" { displayName = name }
		if utf8.RuneCountInString(displayName) > 100 {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("nama tampilan aplikasi maksimal 100 karakter"))
			return
		}
	}
	if phase == "vercel-test-status" || phase == "vercel-live-status" {
		handleDeployStatus(w, r, vercelToken, deployType, name, phase)
		return
	}

	file, header, err := r.FormFile("zip")
	if err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("file zip wajib diunggah: %w", err))
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("hanya file .zip yang diterima"))
		return
	}
	zipBytes, err := io.ReadAll(io.LimitReader(file, maxDeployZipBytes+1))
	if err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("gagal membaca file zip: %w", err))
		return
	}
	if len(zipBytes) > maxDeployZipBytes {
		util.Error(w, http.StatusRequestEntityTooLarge, fmt.Errorf("file zip melebihi batas 4MB"))
		return
	}
	if phase == "extract" {
		if err := prepareArchiveStorage(); err != nil {
			util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("penyiapan D1/R2 gagal: %w", err))
			return
		}
		if err := prepareDeploymentRunner(r); err != nil {
			util.Error(w, http.StatusPreconditionFailed, err); return
		}
	}
	store, err := archive.New()
	if err != nil {
		util.Error(w, http.StatusPreconditionFailed, err); return
	}
	archiveID := strings.TrimSpace(r.FormValue("archive_id"))
	if phase == "extract" {
		record, saveErr := store.Save("app", name, header.Filename, "upload", "pending", zipBytes)
		if saveErr != nil {
			util.Error(w, http.StatusBadGateway, fmt.Errorf("ZIP tidak dapat disimpan, update dibatalkan: %w", saveErr)); return
		}
		archiveID = record.ID
		lockKey, lockErr := appDeploymentLockKey(githubToken, name, isUpdate)
		if lockErr != nil { _ = store.Fail(archiveID); util.Error(w, http.StatusBadGateway, lockErr); return }
		if err := startDeploymentPipeline(archiveID, deployType, name, lockKey, displayName,
			[4]string{"Ekstrak ZIP", "Uji Vercel", "GitHub", "Online Vercel"}); err != nil {
			_ = store.Fail(archiveID)
			util.Error(w, http.StatusConflict, err)
			return
		}
	} else if err := store.Verify(archiveID, "app", name, zipBytes); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip ZIP wajib tersimpan sebelum deployment: %w", err)); return
	} else if err := requireActiveDeployment(archiveID); err != nil {
		util.Error(w, http.StatusConflict, err); return
	}

	label := "aplikasi baru"
	if isUpdate {
		label = "update aplikasi"
	}

	// From here the request itself is valid, so failures are reported as
	// part of the step pipeline (HTTP 200 + deployResult) instead of a bare
	// HTTP error, so the modal can show exactly which step failed — same
	// convention as handleSelfUpdate below.
	files, err := extractZip(zipBytes)
	if err != nil {
		_ = store.Fail(archiveID)
		msg := "[Ekstrak ZIP] Gagal mengekstrak file zip: " + err.Error()
		updateDeploymentStage(archiveID, deployStagePosition(phase), "Failed")
		logLiveLog("ERROR", label+": "+msg)
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	if len(files) == 0 {
		_ = store.Fail(archiveID)
		msg := "[Ekstrak ZIP] Zip kosong atau tidak berisi file yang valid"
		updateDeploymentStage(archiveID, deployStagePosition(phase), "Failed")
		logLiveLog("ERROR", label+": "+msg)
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	logLiveLog("INFO", fmt.Sprintf("%s: mengekstrak %d file dari zip untuk %q", label, len(files), name))
	if phase == "extract" {
		updateDeploymentStage(archiveID, 1, "Success")
		updateDeploymentStage(archiveID, 2, "Running")
		firstPhase := "vercel-test"
		if isUpdate { firstPhase = "baseline" }
		if err := enqueueDeployment(archiveID, firstPhase, "", branchInput, environment); err != nil {
			_ = store.Fail(archiveID)
			updateDeploymentStage(archiveID, 2, "Failed")
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, ArchiveID: archiveID,
				Message: "ZIP tersimpan, tetapi runner tidak dapat dijadwalkan: " + err.Error()})
			return
		}
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: true, Status: "pending", Message: fmt.Sprintf("ZIP disimpan; %d file diekstrak. Tahap berikutnya berjalan otomatis di server.", len(files)), ArchiveID: archiveID})
		return
	}
	if phase == "baseline" {
		if backupErr := ensureAppBaseline(store, githubToken, name); backupErr != nil {
			_ = store.Fail(archiveID)
			updateDeploymentStage(archiveID, 2, "Failed")
			util.JSON(w, http.StatusOK, deployResult{Step: "vercel-test", OK: false, ArchiveID: archiveID,
				Message: "Versi sebelumnya belum dapat diarsipkan; update dibatalkan: " + backupErr.Error()})
			return
		}
		util.JSON(w, http.StatusOK, deployResult{Step: "vercel-test", OK: true, ArchiveID: archiveID,
			Message: "Versi sebelumnya diarsipkan; memulai uji build Vercel."})
		return
	}
	if phase == "vercel-test" {
		updateDeploymentStage(archiveID, 2, "Running")
		id, previewURL, tempProject, framework, rootDirectory, testErr := createVercelTestDeployment(vercelToken, name, files)
		if testErr != nil {
			_ = store.Fail(archiveID)
			cleanupTestProject(vercelToken, tempProject)
			msg := "[Uji Build Vercel] " + testErr.Error()
			updateDeploymentStage(archiveID, 2, "Failed")
			logLiveLog("ERROR", label+": "+msg)
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
			return
		}
		ticket, ticketErr := signBuildTicket(vercelToken, buildTicket{
			Stage: "app-test", Mode: deployType, Name: name, DeploymentID: id,
			Project: tempProject, URL: previewURL, ZipSHA: zipDigest(zipBytes), ArchiveID: archiveID,
			Framework: framework, RootDirectory: rootDirectory,
			Probe: vercelapp.ExpectsHomePage(vercelapp.FilesAtRoot(toVercelFiles(files), rootDirectory)),
		})
		if ticketErr != nil {
			cleanupTestProject(vercelToken, tempProject)
			util.Error(w, http.StatusInternalServerError, ticketErr)
			return
		}
		startMessage := "Build uji Vercel dimulai; menunggu hasil build..."
		if framework != "" { startMessage = "Build uji Vercel (" + framework + ") dimulai; menunggu hasil build..." }
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: true, Status: "pending", Ticket: ticket, ArchiveID: archiveID, Message: startMessage})
		return
	}

	var owner, repoName, branch string
	if isUpdate {
		rows, qErr := d1.Query(`SELECT repo, branch FROM services WHERE name = ? LIMIT 1`, name)
		if qErr != nil {
			util.Error(w, http.StatusInternalServerError, qErr)
			return
		}
		if len(rows) == 0 {
			_ = store.Fail(archiveID)
			msg := fmt.Sprintf("[GitHub] Aplikasi %q tidak ditemukan", name)
			updateDeploymentStage(archiveID, deployStagePosition(phase), "Failed")
			logLiveLog("ERROR", label+": "+msg)
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
			return
		}
		if existingRepo, _ := rows[0]["repo"].(string); existingRepo != "" {
			owner, repoName, _ = splitRepo(existingRepo)
		}
		existingBranch, _ := rows[0]["branch"].(string)
		branch = orDefault(branchInput, orDefault(existingBranch, "main"))
	} else {
		branch = orDefault(branchInput, "main")
	}

	if owner == "" || repoName == "" {
		// New app, or an update for a row saved before repo tracking
		// existed — derive owner/repo from the account + app name.
		derivedOwner, uErr := fetchGithubUsername(githubToken)
		if uErr != nil {
			_ = store.Fail(archiveID)
			msg := "[GitHub] Gagal membaca akun GitHub: " + uErr.Error()
			updateDeploymentStage(archiveID, deployStagePosition(phase), "Failed")
			logLiveLog("ERROR", label+": "+msg)
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
			return
		}
		owner = derivedOwner
		repoName = sanitizeGithubRepoName(name)
	}
	repoFullName := owner + "/" + repoName
	var testProjectToCleanup string
	var phaseTicket buildTicket
	if phase == "github" || phase == "vercel-live" {
		ticket, ticketErr := verifyBuildTicket(vercelToken, r.FormValue("ticket"))
		expectedStage := "app-push"
		if phase == "vercel-live" { expectedStage = "app-live-start" }
		if ticketErr != nil || ticket.Stage != expectedStage || ticket.Mode != deployType || ticket.Name != name || ticket.ZipSHA != zipDigest(zipBytes) || ticket.ArchiveID != archiveID ||
			(phase == "github" && ticket.Project == "") ||
			(phase == "vercel-live" && (ticket.Repo != repoFullName || ticket.Branch != branch)) {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("sesi deployment tidak valid; mulai lagi dari tahap uji build"))
			return
		}
		phaseTicket = ticket
		if phase == "github" { testProjectToCleanup = ticket.Project }
	}
	if testProjectToCleanup != "" { defer cleanupTestProject(vercelToken, testProjectToCleanup) }

	if phase == "vercel-live" {
		updateDeploymentStage(archiveID, 4, "Running")
		// Reuse the exact project selected before the Git push, including a
		// manually imported project whose name differs from our default.
		project := orDefault(phaseTicket.Project, vercelAppProjectName(owner, repoName))
		var id, deploymentURL, source string
		var deployErr error
		if phaseTicket.Linked {
			// Connected to GitHub: build the pushed commit, the same way a
			// manual import deploys, so Vercel shows the repo and commit.
			deployment, gitErr := vercelapp.New(vercelToken).CreateGitDeployment(project, phaseTicket.ProjectID, owner, repoName, branch, phaseTicket.CommitSHA)
			if gitErr == nil {
				id, deploymentURL, source = deployment.ID, deployment.URL, "GitHub "+repoFullName+"@"+branch
			} else {
				// A push may already have triggered a Git deployment. A separate
				// ZIP build could pass the check, then the Git build could replace
				// its alias after this flow says Success.
				_ = store.Fail(archiveID)
				msg := "[Onlinekan di Vercel] Deployment commit GitHub tidak dapat dimulai: " + gitErr.Error() + ". Periksa deployment otomatis untuk commit " + phaseTicket.CommitSHA + " di project " + project + "."
				if !isUpdate { msg += rollbackFailedNewApp(name, owner, repoName, phaseTicket.CreatedRepo, phaseTicket.CreatedProject, project, false) }
				updateDeploymentStage(archiveID, 4, "Failed")
				logLiveLog("ERROR", label+": "+msg)
				util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, Repo: repoFullName})
				return
			}
		}
		if id == "" {
			id, deploymentURL, _, deployErr = createVercelDeployment(vercelToken, project, "production", files, phaseTicket.Framework, phaseTicket.RootDirectory, phaseTicket.ProjectID)
			source = "ZIP"
		}
		if deployErr != nil {
			_ = store.Fail(archiveID)
			msg := "[Onlinekan di Vercel] " + deployErr.Error()
			if !isUpdate { msg += rollbackFailedNewApp(name, owner, repoName, phaseTicket.CreatedRepo, phaseTicket.CreatedProject, project, false) }
			updateDeploymentStage(archiveID, 4, "Failed")
			logLiveLog("ERROR", label+": "+msg)
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, Repo: repoFullName})
			return
		}
		ticket, ticketErr := signBuildTicket(vercelToken, buildTicket{
			Stage: "app-live", Mode: deployType, Name: name, DeploymentID: id,
			URL: deploymentURL, Repo: repoFullName, Branch: branch, Environment: environment,
			ArchiveID: archiveID, ZipSHA: zipDigest(zipBytes),
			Framework: phaseTicket.Framework, RootDirectory: phaseTicket.RootDirectory, Probe: phaseTicket.Probe, Linked: phaseTicket.Linked,
			CreatedRepo: phaseTicket.CreatedRepo, CreatedProject: phaseTicket.CreatedProject, Project: project,
		})
		if ticketErr != nil {
			util.Error(w, http.StatusInternalServerError, ticketErr)
			return
		}
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: true, Status: "pending", Ticket: ticket, Repo: repoFullName, Message: "Deployment production dari " + source + " dimulai; menunggu aplikasi online..."})
		return
	}

	updateDeploymentStage(archiveID, 3, "Running")
	createdRepo := false
	// DevControl's own repo is never a deployment target (only Update Diri,
	// owner-only, may change it).
	if isDevControlRepo(owner, repoName) {
		_ = store.Fail(archiveID)
		msg := "[GitHub] Repo " + owner + "/" + repoName + " adalah repo DevControl sendiri dan tidak dapat dipakai sebagai aplikasi."
		logLiveLog("ERROR", label+": "+msg)
		updateDeploymentStage(archiveID, 3, "Failed")
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
		return
	}
	if !isUpdate {
		var err error
		if createdRepo, err = ensureGithubRepo(githubToken, repoName); err != nil {
			_ = store.Fail(archiveID)
			msg := "[GitHub] Gagal membuat repo: " + err.Error()
			logLiveLog("ERROR", label+": "+msg)
			updateDeploymentStage(archiveID, 3, "Failed")
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
			return
		}
		// "Aplikasi Baru" must never push into a repo that already has code
		// (another app, another project of the account, or DevControl): an
		// existing name is accepted only when the repo is still empty.
		if !createdRepo {
			if empty, checkErr := githubRepoEmpty(githubToken, owner, repoName); checkErr != nil || !empty {
				_ = store.Fail(archiveID)
				msg := "[GitHub] Repo " + owner + "/" + repoName + " sudah ada dan berisi kode. Aplikasi Baru tidak menimpa repo yang sudah ada; pakai nama lain, atau pakai Update di kartu aplikasi itu."
				if checkErr != nil { msg = "[GitHub] Repo " + owner + "/" + repoName + " sudah ada dan isinya tidak dapat diperiksa (" + checkErr.Error() + "); deploy dihentikan agar repo itu tidak tertimpa." }
				logLiveLog("ERROR", label+": "+msg)
				updateDeploymentStage(archiveID, 3, "Failed")
				securitycenter.Raise("siaga", "repo_overwrite", "repo-overwrite:"+strings.ToLower(owner+"/"+repoName)+":"+time.Now().UTC().Format("2006-01-02T15"), "Aplikasi Baru mencoba menimpa repo yang sudah ada",
					"Deploy ke "+owner+"/"+repoName+" dihentikan karena repo itu sudah berisi kode. Kalau ini bukan kekeliruan nama, periksa siapa yang menjalankannya di Riwayat Keamanan.")
				util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
				return
			}
		}
	}

	// An existing Vercel project is connected to the repo (and receives the
	// .env values) BEFORE the push: once connected, the push itself starts
	// Vercel's build, which must already see those values.
	vercelClient := vercelapp.New(vercelToken)
	project := vercelAppProjectName(owner, repoName)
	envVars := vercelapp.EnvFromFiles(vercelapp.FilesAtRoot(toVercelFiles(files), phaseTicket.RootDirectory))
	gitState, envErr := prepareExistingVercelProject(vercelClient, project, owner, repoName, phaseTicket.Framework, phaseTicket.RootDirectory, envVars)
	if envErr == nil && isUpdate && !gitState.Found {
		envErr = fmt.Errorf("project Vercel untuk %s tidak ditemukan di akun/team token ini; periksa VERCEL_TEAM_ID dan koneksi Git pada project import manual", repoFullName)
	}
	if envErr != nil {
		_ = store.Fail(archiveID)
		msg := "[GitHub] Pengaturan project Vercel belum siap, jadi tidak ada yang didorong ke GitHub: " + envErr.Error()
		if !isUpdate { msg += rollbackFailedNewApp(name, owner, repoName, createdRepo, false, "", false) }
		logLiveLog("ERROR", label+": "+msg)
		updateDeploymentStage(archiveID, 3, "Failed")
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, Repo: repoFullName})
		return
	}

	// A connected project builds exactly what is in the repo, so files the
	// ZIP dropped are removed in this same commit instead of after going
	// online (reposync keeps lockfiles, CI files and secrets rules).
	commitSHA, pushPlan, err := pushFilesToGitHub(githubToken, owner, repoName, branch, files, githubPushOptions{DeleteStale: true})
	setSyncNote(archiveID, pushPlan.Summary())
	if err != nil {
		_ = store.Fail(archiveID)
		msg := "[GitHub] Gagal mendorong file: " + err.Error()
		if !isUpdate { msg += rollbackFailedNewApp(name, owner, repoName, createdRepo, false, "", false) }
		logLiveLog("ERROR", label+": "+msg)
		updateDeploymentStage(archiveID, 3, "Failed")
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, Repo: repoFullName})
		return
	}
	// No project existed before this push, so on a first deployment the
	// project that ends up under this name is created by this run (here, or
	// by the ZIP deployment in the next stage when creation fails here).
	createdProject := !isUpdate && !gitState.Found
	if !gitState.Found {
		// First deployment: create the project connected to the repo that
		// now has its first commit (like "Import Git Repository").
		gitState = createVercelGitProject(vercelClient, project, owner, repoName, phaseTicket.Framework, phaseTicket.RootDirectory, envVars)
	}
	for _, note := range gitState.Notes {
		setSyncNote(archiveID, note)
		logLiveLog("INFO", label+": "+note)
	}

	updateDeploymentStage(archiveID, 3, "Success")
	updateDeploymentStage(archiveID, 4, "Running")
	logLiveLog("INFO", fmt.Sprintf("%s: %q didorong ke %s@%s", label, name, repoFullName, branch))
	nextTicket, ticketErr := signBuildTicket(vercelToken, buildTicket{
		Stage: "app-live-start", Mode: deployType, Name: name, ZipSHA: zipDigest(zipBytes),
		Repo: repoFullName, Branch: branch, ArchiveID: archiveID, CommitSHA: commitSHA,
		Project: gitState.Name, ProjectID: gitState.ID,
		Framework: phaseTicket.Framework, RootDirectory: phaseTicket.RootDirectory, Probe: phaseTicket.Probe, Linked: gitState.Linked,
		CreatedRepo: createdRepo, CreatedProject: createdProject,
	})
	if ticketErr != nil {
		util.Error(w, http.StatusInternalServerError, ticketErr)
		return
	}

	connection := "Vercel belum terhubung ke GitHub; production memakai ZIP"
	if gitState.Linked { connection = "Vercel terhubung ke GitHub" }
	util.JSON(w, http.StatusOK, deployResult{
		Step: phase, OK: true,
		Message: fmt.Sprintf("Berhasil didorong ke %s@%s; %s.", repoFullName, branch, connection),
		Repo:    repoFullName, Ticket: nextTicket, ArchiveID: archiveID,
	})
}

// vercelGitState: the app's Vercel project and whether production is built
// from its GitHub repo. Notes are shown in the deployment history.
type vercelGitState struct {
	ID     string
	Name   string
	Found  bool
	Linked bool
	Notes  []string
}

func vercelGitAccessNote(repo string, err error) string {
	return "Vercel belum dapat mengakses repo " + repo + " (" + err.Error() + "). Izinkan repo ini untuk aplikasi Vercel di GitHub (Settings → Applications → Vercel → Repository access); sampai itu, production tetap dionlinekan dari ZIP."
}

func syncVercelEnv(client *vercelapp.Client, projectID string, env []vercelapp.EnvVar) (string, error) {
	if len(env) == 0 { return "", nil }
	added, failed, err := client.AddMissingEnv(projectID, env)
	if err != nil { return "", err }
	parts := []string{}
	if len(added) > 0 { parts = append(parts, fmt.Sprintf("%d variabel .env disalin ke Environment Variables Vercel (%s)", len(added), strings.Join(added, ", "))) }
	if len(failed) > 0 { parts = append(parts, "ditolak Vercel: "+strings.Join(failed, ", ")) }
	return strings.Join(parts, "; "), nil
}

// prepareExistingVercelProject runs before the push. Found=false means the
// project does not exist yet (first deployment). Settings errors stop the
// push before GitHub can trigger a build with the wrong framework or env.
func prepareExistingVercelProject(client *vercelapp.Client, project, owner, repo, framework, rootDirectory string, env []vercelapp.EnvVar) (vercelGitState, error) {
	state := vercelGitState{}
	info, found, err := client.GetProject(project)
	if err != nil {
		return state, fmt.Errorf("project %s tidak dapat diperiksa: %w", project, err)
	}
	full := owner + "/" + repo
	if found && info.LinkType != "" && !info.LinkedTo(owner, repo) {
		return state, fmt.Errorf("project Vercel %s terhubung ke repo lain; periksa project dan akun/team Vercel", project)
	}
	if !found || !info.LinkedTo(owner, repo) || info.RootDirectory != rootDirectory {
		// A monorepo can have a web and API project connected to the same repo.
		// Select the web root exactly; never turn the API project into a web app.
		matches, listErr := client.FindLinkedProjects(owner, repo)
		if listErr != nil { return state, fmt.Errorf("project Git %s tidak dapat dicari di Vercel: %w", full, listErr) }
		exact := []vercelapp.Project{}
		legacy := []vercelapp.Project{}
		for _, match := range matches {
			if match.RootDirectory == rootDirectory { exact = append(exact, match) }
			if match.RootDirectory == "" { legacy = append(legacy, match) }
		}
		selectedMatch := vercelapp.Project{}
		if found && info.LinkedTo(owner, repo) && info.RootDirectory == rootDirectory { selectedMatch = info }
		if selectedMatch.ID == "" && len(exact) == 1 { selectedMatch = exact[0] }
		if selectedMatch.ID == "" && len(exact) > 1 {
			return state, fmt.Errorf("repo %s memiliki beberapa project Vercel dengan root %q; pilih project melalui Vercel sebelum mencoba lagi", full, rootDirectory)
		}
		if selectedMatch.ID == "" && found && (info.RootDirectory == "" || info.RootDirectory == rootDirectory) { selectedMatch = info }
		if selectedMatch.ID == "" && len(legacy) == 1 && legacy[0].Name == project { selectedMatch = legacy[0] }
		if selectedMatch.ID == "" && found { return state, fmt.Errorf("project %s memakai Root Directory %q, sedangkan web berada di %q; project API tidak akan diubah", info.Name, info.RootDirectory, rootDirectory) }
		if selectedMatch.ID != "" && selectedMatch.ID != info.ID {
			selected, selectedFound, getErr := client.GetProject(selectedMatch.ID)
			if getErr != nil { return state, getErr }
			if !selectedFound || !selected.LinkedTo(owner, repo) {
				return state, fmt.Errorf("koneksi project Vercel %s berubah saat diperiksa", selectedMatch.Name)
			}
			info, found = selected, true
			if info.Name != project { state.Notes = append(state.Notes, "Menggunakan project Vercel import Git yang sudah ada: "+info.Name) }
		}
	}
	if !found { return state, nil }
	state.Found, state.ID, state.Name = true, info.ID, orDefault(info.Name, project)
	fields, alignErr := client.AlignBuildSettings(info, framework, rootDirectory)
	if alignErr != nil { return state, fmt.Errorf("pengaturan build project Vercel belum dapat disamakan dengan uji ZIP: %w", alignErr) }
	if len(fields) > 0 { state.Notes = append(state.Notes, "Pengaturan project Vercel disamakan dengan uji ZIP: "+strings.Join(fields, ", ")) }
	note, envErr := syncVercelEnv(client, info.ID, env)
	if envErr != nil { return state, envErr }
	if note != "" { state.Notes = append(state.Notes, note) }
	if info.LinkedTo(owner, repo) {
		state.Linked = true
	} else if linkErr := client.LinkProject(info.ID, full); linkErr != nil {
		state.Notes = append(state.Notes, vercelGitAccessNote(full, linkErr))
	} else {
		state.Linked = true
		state.Notes = append(state.Notes, "Project Vercel dihubungkan ke GitHub "+full)
	}
	return state, nil
}

// createVercelGitProject creates the app's project connected to its repo.
// Without repo access it still creates the project with the right framework
// and production is deployed from the ZIP, as before.
func createVercelGitProject(client *vercelapp.Client, project, owner, repo, framework, rootDirectory string, env []vercelapp.EnvVar) vercelGitState {
	state := vercelGitState{Found: true, Name: project}
	full := owner + "/" + repo
	// Prepare the project and environment first. Linking a populated Git repo
	// can immediately start a build, so its first build must have the env.
	info, createErr := client.CreateProject(project, framework, rootDirectory, "")
	if createErr != nil {
		state.Notes = append(state.Notes, "Project Vercel dibuat oleh deployment ZIP ("+createErr.Error()+")")
		return state
	}
	state.ID = info.ID
	note, envErr := syncVercelEnv(client, info.ID, env)
	if envErr != nil {
		state.Notes = append(state.Notes, "Variabel .env belum dapat disalin ke Vercel ("+envErr.Error()+"); production kali ini dionlinekan dari ZIP")
		return state
	}
	if note != "" { state.Notes = append(state.Notes, note) }
	if linkErr := client.LinkProject(info.ID, full); linkErr != nil {
		state.Notes = append(state.Notes, vercelGitAccessNote(full, linkErr))
		return state
	}
	state.Linked = true
	state.Notes = append(state.Notes, "Project Vercel dibuat dan terhubung ke GitHub "+full)
	return state
}

// Pending status requests only poll Vercel once. Once READY, the handler also
// opens the page and verifies that any alias resolves to this deployment.
func handleDeployStatus(w http.ResponseWriter, r *http.Request, token, mode, name, phase string) {
	ticket, err := verifyBuildTicket(token, r.FormValue("ticket"))
	expected := "app-test"
	step := "vercel-test"
	position := 2
	if phase == "vercel-live-status" { expected, step, position = "app-live", "vercel-live", 4 }
	if err != nil || ticket.Stage != expected || ticket.Mode != mode || ticket.Name != name || ticket.DeploymentID == "" ||
		(step == "vercel-test" && ticket.Project == "") || ticket.ArchiveID == "" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("sesi pemantauan build tidak valid atau kedaluwarsa"))
		return
	}
	store, storeErr := archive.New()
	if storeErr != nil { util.Error(w, http.StatusPreconditionFailed, storeErr); return }
	record, recordErr := store.Get(ticket.ArchiveID)
	if recordErr != nil || record.Scope != "app" || record.Target != name || record.SHA256 != ticket.ZipSHA {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip sesi deployment tidak valid")); return
	}
	if step == "vercel-live" && record.Status == "current" {
		appURL := ticket.URL
		if rows, queryErr := d1.Query(`SELECT app_url FROM services WHERE name = ? LIMIT 1`, name); queryErr == nil && len(rows) > 0 {
			if saved, ok := rows[0]["app_url"].(string); ok && saved != "" { appURL = saved }
		}
		util.JSON(w, http.StatusOK, deployResult{Step: "done", OK: true, Status: "ready", ArchiveID: record.ID,
			Message: "Aplikasi sudah online; ZIP aktif tersimpan", Repo: ticket.Repo, AppURL: appURL})
		return
	}
	if record.Status != "pending" { util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip sesi deployment tidak lagi menunggu proses")); return }
	if err := requireActiveDeployment(ticket.ArchiveID); err != nil { util.Error(w, http.StatusConflict, err); return }
	state, liveURLs, detail, checkErr := getVercelBuildStatus(token, ticket.DeploymentID)
	if checkErr != nil {
		util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal memeriksa status Vercel: %w", checkErr))
		return
	}
	if state != "READY" && state != "ERROR" && state != "CANCELED" {
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: true, Status: "pending", Ticket: r.FormValue("ticket"),
			Message: "Vercel: " + state + ". Build masih berjalan; status akan diperiksa lagi."})
		return
	}
	if state != "READY" {
		_ = store.Fail(ticket.ArchiveID)
		msg := fmt.Sprintf("[%s] Build Vercel %s", map[string]string{"vercel-test": "Uji Build Vercel", "vercel-live": "Onlinekan di Vercel"}[step], state)
		// Read the compiler output before the temporary project is deleted.
		buildLog, logErr := getVercelBuildLog(token, ticket.DeploymentID)
		if detail != "" { msg += ": " + detail } else if buildLog != "" { msg += ": " + vercelFailureSummary(buildLog) }
		if step == "vercel-test" {
			if logErr == nil { cleanupTestProject(token, ticket.Project) } else {
				msg += ". Log belum dapat dibaca (" + logErr.Error() + "); project uji " + ticket.Project + " tetap ada sementara di Vercel untuk diperiksa, lalu dibersihkan otomatis."
				recordOrphanTestProject(ticket.Project)
			}
		} else if mode == "new_app" {
			if logErr != nil { msg += ". Log belum dapat dibaca (" + logErr.Error() + ")." }
			repoOwner, repoName, _ := splitRepo(ticket.Repo)
			msg += rollbackFailedNewApp(name, repoOwner, repoName, ticket.CreatedRepo, ticket.CreatedProject, ticket.Project, logErr != nil)
		}
		updateDeploymentStage(ticket.ArchiveID, position, "Failed")
		logLiveLog("ERROR", msg)
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: msg, Repo: ticket.Repo, BuildLog: buildLog})
		return
	}
	if step == "vercel-test" && ticket.Probe {
		// READY only means the build finished. Open the test page itself:
		// Vercel's own 404 there means the app would be unreachable online.
		// The test project is DevControl's own throwaway project. Teams often
		// apply Deployment Protection to every new project, which answers the
		// check with a redirect to Vercel's login. Lift it and, independently,
		// open the page with a protection-bypass secret for automation.
		vercelClient := vercelapp.New(token)
		protectErr := vercelClient.DisableProtection(ticket.Project)
		if protectErr != nil {
			logLiveLog("WARN", "Proteksi project uji tidak dapat dilepas: "+protectErr.Error())
		}
		bypass, bypassErr := vercelClient.ProtectionBypass(ticket.Project)
		if bypassErr != nil {
			logLiveLog("WARN", "Kunci bypass proteksi project uji tidak dapat dibuat: "+bypassErr.Error())
		}
		pageErr := vercelapp.CheckHomePageWith(ticket.URL, bypass)
		// Protection changes take a few seconds to reach Vercel's edge.
		for wait := 0; vercelapp.IsProtectionError(pageErr) && wait < 5; wait++ {
			time.Sleep(2 * time.Second)
			pageErr = vercelapp.CheckHomePageWith(ticket.URL, bypass)
		}
		if pageErr != nil {
			_ = store.Fail(ticket.ArchiveID)
			msg := unavailableHomePageMessage("Uji Build Vercel", ticket.URL, pageErr)
			if vercelapp.IsProtectionError(pageErr) {
				reasons := []string{}
				if protectErr != nil { reasons = append(reasons, "proteksi tidak dapat dilepas: "+protectErr.Error()) }
				if bypassErr != nil { reasons = append(reasons, "kunci bypass gagal dibuat: "+bypassErr.Error()) }
				if len(reasons) == 0 { reasons = append(reasons, "proteksi tetap aktif meski sudah dilepas dan kunci bypass dipakai") }
				msg += " Build aplikasi berhasil; yang gagal adalah pemeriksaan DevControl karena project uji dilindungi Deployment Protection (" + strings.Join(reasons, "; ") + ")."
			}
			msg += " Tidak ada yang didorong ke GitHub dan production tidak berubah. Project uji " + ticket.Project + " tetap ada sementara di Vercel untuk diperiksa, lalu dibersihkan otomatis."
			recordOrphanTestProject(ticket.Project)
			updateDeploymentStage(ticket.ArchiveID, 2, "Failed")
			logLiveLog("ERROR", msg)
			util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: msg})
			return
		}
	}
	if step == "vercel-test" {
		next, signErr := signBuildTicket(token, buildTicket{
			Stage: "app-push", Mode: mode, Name: name, ZipSHA: ticket.ZipSHA, Project: ticket.Project, ArchiveID: ticket.ArchiveID,
			Framework: ticket.Framework, RootDirectory: ticket.RootDirectory, Probe: ticket.Probe,
		})
		if signErr != nil { util.Error(w, http.StatusInternalServerError, signErr); return }
		updateDeploymentStage(ticket.ArchiveID, 2, "Success")
		// Show the next stage as running right away; the runner picks up the
		// GitHub phase on its next tick, which used to look like a stall.
		updateDeploymentStage(ticket.ArchiveID, 3, "Running")
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: true, Status: "ready", Ticket: next, Message: "Build uji Vercel berhasil"})
		return
	}
	// Check the exact link that will be saved. An alias can be broken while
	// another alias or the immutable deployment URL still works.
	if ticket.URL != "" {
		found := false
		for _, candidate := range liveURLs { if candidate == ticket.URL { found = true; break } }
		if !found { liveURLs = append(liveURLs, ticket.URL) }
	}
	var liveURL string
	var pageFailures []string
	vercelClient := vercelapp.New(token)
	for _, candidate := range liveURLs {
		if ticket.Probe {
			if pageErr := vercelapp.CheckHomePage(candidate); pageErr != nil {
				pageFailures = append(pageFailures, candidate+": "+pageErr.Error())
				continue
			}
		}
		if candidate != ticket.URL {
			// A redirecting alias may still open an older, working deployment.
			// Save it only when Vercel resolves it to this exact build.
			aliasID, aliasErr := vercelClient.DeploymentIDForURL(candidate)
			if aliasErr != nil || aliasID != ticket.DeploymentID {
				why := "alias tidak menunjuk ke deployment yang baru"
				if aliasErr != nil { why = "alias tidak dapat diverifikasi: " + aliasErr.Error() }
				pageFailures = append(pageFailures, candidate+": "+why)
				continue
			}
		}
		liveURL = candidate
		break
	}
	if liveURL == "" {
		// Never record Success or replace the saved link on an unverified site.
		_ = store.Fail(ticket.ArchiveID)
		msg := "[Onlinekan di Vercel] Build READY, tetapi tautan aplikasi gagal dibuka: " + strings.Join(pageFailures, "; ") +
			". Periksa Output/Runtime Logs, framework, Root Directory, Output Directory, dan Deployment Protection di Vercel."
		if mode == "new_app" {
			repoOwner, repoName, _ := splitRepo(ticket.Repo)
			msg += rollbackFailedNewApp(name, repoOwner, repoName, ticket.CreatedRepo, ticket.CreatedProject, ticket.Project, false)
		} else {
			msg += " Tautan lama tetap disimpan; jika versi lama tertimpa, pulihkan lewat Vercel → Deployments → Promote."
		}
		updateDeploymentStage(ticket.ArchiveID, 4, "Failed")
		logLiveLog("ERROR", msg)
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: msg, Repo: ticket.Repo, AppURL: ticket.URL})
		return
	}
	if mode == "update_app" {
		_, err = d1.Query(`UPDATE services SET repo = ?, branch = ?, app_url = ?, status = 'Healthy' WHERE name = ?`, ticket.Repo, ticket.Branch, liveURL, name)
	} else {
		// The runner survives the browser closing; keep the original label on
		// its durable job and associate it with the repo only after success.
		displayName := name
		rows, lookupErr := d1.Query(`SELECT display_name FROM deployment_jobs WHERE id = ? AND kind = 'new_app' LIMIT 1`, ticket.ArchiveID)
		if lookupErr != nil { util.Error(w, http.StatusBadGateway, lookupErr); return }
		if len(rows) > 0 { if saved, ok := rows[0]["display_name"].(string); ok && saved != "" { displayName = saved } }
		// A repeated status request must not insert a second service row.
		_, err = d1.Query(`INSERT INTO services (name, display_name, status, uptime, version, repo, branch, app_url)
			SELECT ?, ?, 'Healthy', 100, 'v1.0.0', ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM services WHERE name = ?)`,
			name, displayName, ticket.Repo, ticket.Branch, liveURL, name)
	}
	if err != nil {
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: "[Simpan tautan aplikasi] URL gagal disimpan: " + err.Error(), Repo: ticket.Repo, AppURL: liveURL})
		return
	}
	if err := store.Promote(ticket.ArchiveID, "app", name); err != nil {
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: "Aplikasi online, tetapi gagal mencatat versi ZIP aktif: " + err.Error(), Repo: ticket.Repo, AppURL: liveURL})
		return
	}
	// GitHub was synchronized before production started. No unverified
	// follow-up commit may trigger another Vercel deployment after success.
	updateDeploymentStage(ticket.ArchiveID, 4, "Success")
	label, icon := "aplikasi baru", "box"
	if mode == "update_app" { label, icon = "update aplikasi", "check" }
	logActivity(fmt.Sprintf("Deployment %s berhasil", label), fmt.Sprintf("%s (%s@%s) → %s", name, ticket.Repo, ticket.Branch, ticket.Environment), icon)
	logLiveLog("INFO", fmt.Sprintf("%s: %q sudah online di %s", label, name, liveURL))
	util.JSON(w, http.StatusOK, deployResult{Step: "done", OK: true, Status: "ready", Message: "Aplikasi sudah online di Vercel", Repo: ticket.Repo, AppURL: liveURL})
}

func unavailableHomePageMessage(stage, pageURL string, pageErr error) string {
	return "[" + stage + "] Build Vercel READY, tetapi halaman utama " + pageURL + " gagal dibuka: " + pageErr.Error() +
		". Periksa Output dan Runtime Logs deployment, framework, Root Directory, Output Directory, serta Deployment Protection di Vercel."
}

// An existing app predates ZIP history: preserve the current GitHub branch
// before the first update, and refuse to mutate it if that snapshot fails.
func ensureAppBaseline(store *archive.Store, token, name string) error {
	hasCurrent, err := store.HasCurrent("app", name)
	if err != nil || hasCurrent { return err }
	rows, err := d1.Query(`SELECT repo, branch FROM services WHERE name = ? LIMIT 1`, name)
	if err != nil { return err }
	if len(rows) == 0 { return fmt.Errorf("aplikasi lama tidak ditemukan di D1") }
	repo, _ := rows[0]["repo"].(string)
	branch, _ := rows[0]["branch"].(string)
	branch = orDefault(branch, "main")
	if repo == "" {
		owner, err := fetchGithubUsername(token)
		if err != nil { return err }
		repo = owner + "/" + sanitizeGithubRepoName(name)
	}
	return ensureGithubBaseline(store, token, "app", name, repo, branch)
}

func ensureGithubBaseline(store *archive.Store, token, scope, target, repo, branch string) error {
	hasCurrent, err := store.HasCurrent(scope, target)
	if err != nil || hasCurrent { return err }
	previousZip, err := fetchGithubArchive(token, repo, branch)
	if err != nil { return err }
	_, err = store.Save(scope, target, sanitizeGithubRepoName(repo)+"-sebelum-update.zip", "github_snapshot", "current", previousZip)
	return err
}

func fetchGithubArchive(token, repo, branch string) ([]byte, error) {
	owner, name, ok := splitRepo(repo)
	if !ok { return nil, fmt.Errorf("format repo lama tidak valid") }
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/zipball/%s", url.PathEscape(owner), url.PathEscape(name), url.PathEscape(branch))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil { return nil, err }
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return nil, fmt.Errorf("snapshot repo %s@%s gagal dibaca (HTTP %d)", repo, branch, resp.StatusCode) }
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDeployZipBytes+1))
	if err != nil { return nil, err }
	if len(data) > maxDeployZipBytes { return nil, fmt.Errorf("snapshot repo lama melampaui batas arsip 4 MiB") }
	files, err := extractZip(data)
	if err != nil || len(files) == 0 { return nil, fmt.Errorf("snapshot repo lama bukan ZIP sumber yang valid") }
	return data, nil
}

// Listing and downloading source code require a separately configured secret.
// The ZIP bytes never enter IndexedDB or the PWA API response cache.
func handleZipArchives(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("use GET")); return }
	// The owner/admin session and a recent confirmation always protect the
	// archives. ZIP_ARCHIVE_ACCESS_TOKEN is an optional extra key; it may not
	// be the admin password (that would reuse the password in a second place).
	secret := os.Getenv("ZIP_ARCHIVE_ACCESS_TOKEN")
	if secret != "" {
		if len(secret) < 16 || secret == os.Getenv("DEVCONTROL_ADMIN_PASSWORD") {
			util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("ZIP_ARCHIVE_ACCESS_TOKEN harus minimal 16 karakter dan berbeda dari kata sandi admin; ganti atau hapus variabel itu di Vercel")); return
		}
		lockKey := "zip:" + auth.ClientIP(r)
		if minutes, err := auth.Throttled(lockKey); err != nil {
			util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("status keamanan tidak dapat diperiksa; coba lagi")); return
		} else if minutes > 0 {
			util.Error(w, http.StatusTooManyRequests, fmt.Errorf("terlalu banyak kunci arsip salah; coba lagi dalam %d menit", minutes)); return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			auth.RecordFailure(lockKey)
			util.JSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "kunci arsip salah atau belum diisi", "key_required": true}); return
		}
		auth.ClearFailures(lockKey)
	}
	store, err := archive.New()
	if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" { util.Error(w, http.StatusBadRequest, fmt.Errorf("ID arsip tidak valid")); return }
		record, err := store.Get(id)
		if err != nil { util.Error(w, http.StatusNotFound, err); return }
		data, err := store.Download(record)
		if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal memeriksa ZIP tersimpan: %w", err)); return }
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeDownloadName(record.Filename)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
		return
	}
	// ?repo=owner/name: archives of one project card (latest successful ZIP,
	// plus a pending one while an update is running).
	if repo := strings.TrimSpace(r.URL.Query().Get("repo")); repo != "" {
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(repo) > 200 {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("format repo harus owner/nama")); return
		}
		if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		items, err := store.ListForRepo(repo, parts[1])
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "has_more": false})
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" { offset, err = 0, nil }
	if err != nil || offset < 0 || offset > 100000 { util.Error(w, http.StatusBadRequest, fmt.Errorf("offset arsip tidak valid")); return }
	items, more, err := store.List(offset)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	util.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "has_more": more})
}

// isDevControlRepo reports whether owner/repo is the repo DevControl runs from.
func isDevControlRepo(owner, repo string) bool {
	selfOwner, selfSlug := strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_OWNER")), strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_SLUG"))
	return selfOwner != "" && selfSlug != "" && strings.EqualFold(owner, selfOwner) && strings.EqualFold(repo, selfSlug)
}

// githubRepoEmpty: GitHub answers 409 for the commits of a repo that has none.
func githubRepoEmpty(token, owner, repo string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/commits?per_page=1", nil)
	if err != nil { return false, err }
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil { return false, err }
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusConflict:
		return true, nil
	case http.StatusOK:
		return false, nil
	}
	return false, fmt.Errorf("HTTP %d", resp.StatusCode)
}

// safeDownloadName keeps an uploaded ZIP name safe inside a header value.
func safeDownloadName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' { return r }
		return '_'
	}, name)
	if len(cleaned) > 120 { cleaned = cleaned[len(cleaned)-120:] }
	if cleaned == "" || !strings.HasSuffix(strings.ToLower(cleaned), ".zip") { cleaned += ".zip" }
	return cleaned
}

type githubUser struct {
	Login string `json:"login"`
}

// fetchGithubUsername returns the login behind GITHUB_TOKEN, so "Aplikasi
// Baru"/"Update Aplikasi" can build "owner/repo" automatically instead of
// asking the user to type or pick one.
func fetchGithubUsername(token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out githubUser
	decodeErr := json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d dari GitHub saat membaca akun", resp.StatusCode)
	}
	if decodeErr != nil {
		return "", decodeErr
	}
	if out.Login == "" {
		return "", fmt.Errorf("GitHub tidak mengembalikan username")
	}
	return out.Login, nil
}

// sanitizeGithubRepoName turns an app name into a valid GitHub repo name
// (letters, digits, '-', '_', '.'); anything else becomes '-'.
func sanitizeGithubRepoName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

type createRepoResponse struct {
	Message string `json:"message"`
}

// ensureGithubRepo creates repoName under the account behind token. If a
// repo with that name already exists it's treated as success (re-running a
// failed "Aplikasi Baru" submission, or deploying a second app with a name
// that collides, should never hard-fail here) — pushFilesToGitHub right
// after this will replace the selected branch with the uploaded package.
// ensureGithubRepo creates the private repo, or accepts one that already
// exists. created is true only when this call made it, so a failed first
// deployment removes only what it created itself.
func ensureGithubRepo(token, repoName string) (created bool, err error) {
	payload := map[string]interface{}{
		"name":      repoName,
		"private":   true,
		"auto_init": false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.github.com/user/repos", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		return true, nil
	}
	var out createRepoResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(out.Message), "already exists") {
		return false, nil
	}
	if out.Message != "" {
		return false, fmt.Errorf("%s", out.Message)
	}
	return false, fmt.Errorf("HTTP %d dari GitHub saat membuat repo", resp.StatusCode)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func logActivity(title, description, icon string) {
	_, _ = d1.Query(
		`INSERT INTO activity_log (title, description, icon) VALUES (?, ?, ?)`,
		title, description, icon,
	)
}

func logLiveLog(level, message string) {
	_, _ = d1.Query(
		`INSERT INTO live_logs (level, message) VALUES (?, ?)`,
		level, message,
	)
}

type githubRepo struct {
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
	Language      string `json:"language"`
	HTMLURL       string `json:"html_url"`
	PushedAt      string `json:"pushed_at"`
	Stars         int    `json:"stargazers_count"`
	OpenIssues    int    `json:"open_issues_count"`
}

const promoImagePrefix = "__devcontrol__/banner-"

func validPromoImageRepo(repo string) bool {
	if !strings.HasPrefix(repo, promoImagePrefix) { return false }
	id := strings.TrimPrefix(repo, promoImagePrefix)
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

func validPromoURL(raw string) bool {
	if raw == "" { return true }
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") &&
		parsed.Hostname() != "" && parsed.User == nil && !strings.ContainsAny(raw, "\r\n\t ")
}

// Banner text is stored in D1. Its image uses a separate verified R2 upload
// slot for each replacement, so a failed save cannot remove the live image.
func handleAppPromo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	// Every signed-in role may read the banner; only owner/admin change it.
	if (r.Method != http.MethodGet && !auth.IsAdmin(r)) || auth.Current(r) == nil { util.Error(w, http.StatusForbidden, fmt.Errorf("sesi admin diperlukan")); return }
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }

	switch r.Method {
	case http.MethodGet:
		rows, err := d1.Query(`SELECT target_repo AS repo, app_name, app_url, title, description, image_repo, version
			FROM app_promo_banner WHERE id = 1 LIMIT 1`)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(rows) == 0 { util.JSON(w, http.StatusOK, nil); return }
		util.JSON(w, http.StatusOK, rows[0])
	case http.MethodPost:
		var input struct {
			AppName string `json:"app_name"`
			AppURL string `json:"app_url"`
			Title string `json:"title"`
			Description string `json:"description"`
			ImageRepo string `json:"image_repo"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("data banner tidak valid")); return
		}
		input.AppName = strings.TrimSpace(input.AppName)
		input.AppURL = strings.TrimSpace(input.AppURL)
		input.Title = strings.TrimSpace(input.Title)
		input.Description = strings.TrimSpace(input.Description)
		if utf8.RuneCountInString(input.AppName) > 100 || len(input.AppURL) > 500 || !validPromoURL(input.AppURL) ||
			utf8.RuneCountInString(input.Title) > 80 ||
			utf8.RuneCountInString(input.Description) > 220 || !validPromoImageRepo(input.ImageRepo) ||
			len(input.Version) != 32 || strings.Trim(input.Version, "0123456789abcdef") != "" {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("nama aplikasi maksimal 100 karakter, judul 80 karakter, deskripsi 220 karakter, tautan harus HTTP(S), dan gambar banner wajib diunggah")); return
		}
		images, err := d1.Query(`SELECT version FROM project_thumbnails WHERE repo = ? LIMIT 1`, input.ImageRepo)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(images) != 1 || images[0]["version"] != input.Version {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("gambar banner belum selesai diunggah")); return
		}
		previous, err := d1.Query(`SELECT image_repo FROM app_promo_banner WHERE id = 1 LIMIT 1`)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if _, err := d1.Query(`INSERT INTO app_promo_banner (id, target_repo, app_name, app_url, title, description, image_repo, version)
			VALUES (1, '', ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET
			target_repo = '', app_name = excluded.app_name, app_url = excluded.app_url,
			title = excluded.title, description = excluded.description, image_repo = excluded.image_repo,
			version = excluded.version, updated_at = CURRENT_TIMESTAMP`,
			input.AppName, input.AppURL, input.Title, input.Description, input.ImageRepo, input.Version); err != nil {
			util.Error(w, http.StatusBadGateway, err); return
		}
		if len(previous) == 1 {
			old, _ := previous[0]["image_repo"].(string)
			if validPromoImageRepo(old) && old != input.ImageRepo {
				if store, storeErr := archive.New(); storeErr == nil { _ = projectthumbnail.Delete(old, store) }
			}
		}
		util.JSON(w, http.StatusOK, map[string]bool{"saved": true})
	case http.MethodDelete:
		rows, err := d1.Query(`SELECT image_repo FROM app_promo_banner WHERE id = 1 LIMIT 1`)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(rows) == 1 {
			imageRepo, _ := rows[0]["image_repo"].(string)
			if !validPromoImageRepo(imageRepo) { util.Error(w, http.StatusBadGateway, fmt.Errorf("gambar banner tersimpan tidak valid")); return }
			store, storeErr := archive.New()
			if storeErr != nil { util.Error(w, http.StatusPreconditionFailed, storeErr); return }
			if err := projectthumbnail.Delete(imageRepo, store); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		}
		if _, err := d1.Query(`DELETE FROM app_promo_banner WHERE id = 1`); err != nil {
			util.Error(w, http.StatusBadGateway, err); return
		}
		util.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET, POST, atau DELETE"))
	}
}

// GET /api/github-repos -> repos the configured GITHUB_TOKEN can see, so the
// "Update Diri" form can offer a picker instead of a free-text "owner/repo"
// field. ("Aplikasi Baru"/"Update Aplikasi" no longer need this — their repo
// is derived/created automatically, see handleTriggerDeployment.)
func handleGithubRepos(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf(
			"daftar repo belum tersedia: set GITHUB_TOKEN di environment variables Vercel",
		))
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var repos []githubRepo

	// Bounded at 5 pages (500 repos) so one slow/huge account can't stall
	// the serverless function indefinitely.
	for page := 1; page <= 5; page++ {
		endpoint := fmt.Sprintf(
			"https://api.github.com/user/repos?per_page=100&page=%d&sort=pushed&affiliation=owner,collaborator,organization_member",
			page,
		)
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			util.Error(w, http.StatusInternalServerError, err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := client.Do(req)
		if err != nil {
			util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal menghubungi GitHub: %w", err))
			return
		}
		status := resp.StatusCode
		if status >= 300 {
			var detail struct {
				Message string `json:"message"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&detail)
			resp.Body.Close()
			if detail.Message == "" {
				detail.Message = "periksa GITHUB_TOKEN dan akses repository"
			}
			util.Error(w, http.StatusBadGateway, fmt.Errorf("GitHub HTTP %d: %s", status, detail.Message))
			return
		}
		var batch []githubRepo
		decodeErr := json.NewDecoder(resp.Body).Decode(&batch)
		resp.Body.Close()
		if decodeErr != nil {
			util.Error(w, http.StatusInternalServerError, decodeErr)
			return
		}

		repos = append(repos, batch...)
		if len(batch) < 100 {
			break
		}
	}

	util.JSON(w, http.StatusOK, repos)
}

type githubBranch struct {
	Name string `json:"name"`
}

// GET /api/github-branches?repo=owner/repo -> branch names for that repo, so
// the branch field in the deployment forms (the "Update Diri" self-update
// form in particular) can offer a real picker instead of a free-text guess
// at what branches actually exist in the repo.
func handleGithubBranches(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf(
			"daftar branch belum tersedia: set GITHUB_TOKEN di environment variables Vercel",
		))
		return
	}

	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	owner, repoName, ok := splitRepo(repo)
	if !ok {
		util.Error(w, http.StatusBadRequest, fmt.Errorf(`parameter ?repo= wajib diisi dengan format "owner/repo"`))
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var names []string

	// Bounded at 3 pages (300 branches) for the same reason as github-repos:
	// keeps one huge repo from stalling the serverless function.
	for page := 1; page <= 3; page++ {
		endpoint := fmt.Sprintf(
			"https://api.github.com/repos/%s/%s/branches?per_page=100&page=%d",
			owner, repoName, page,
		)
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			util.Error(w, http.StatusInternalServerError, err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := client.Do(req)
		if err != nil {
			util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal menghubungi GitHub: %w", err))
			return
		}
		var batch []githubBranch
		decodeErr := json.NewDecoder(resp.Body).Decode(&batch)
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusNotFound {
			util.Error(w, http.StatusNotFound, fmt.Errorf("repo %q tidak ditemukan di GitHub", repo))
			return
		}
		if status >= 300 {
			util.Error(w, http.StatusBadGateway, fmt.Errorf("GitHub membalas HTTP %d", status))
			return
		}
		if decodeErr != nil {
			util.Error(w, http.StatusInternalServerError, decodeErr)
			return
		}

		for _, b := range batch {
			names = append(names, b.Name)
		}
		if len(batch) < 100 {
			break
		}
	}

	util.JSON(w, http.StatusOK, names)
}

// --- merged from selfupdate.go (self-update) ---


const (
// Both uploads and authenticated downloads pass through a Vercel Function.
	maxSelfUpdateZipBytes = maxDeployZipBytes
)

type selfUpdateFile struct {
	path string
	data []byte
}

// selfUpdateResult is returned after each stage. `step` says how far it got, so the frontend can highlight
// exactly which stage failed, and `message` always names the process that
// was running when it failed (e.g. "[Ekstrak ZIP] ...", "[Uji Build Vercel]
// ...", "[Perbarui GitHub] ...") so the error is never just a bare Vercel/
// GitHub API message with no context about where it happened.
type selfUpdateResult struct {
	Step       string `json:"step"` // "extract" | "vercel-test" | "github-push" | "vercel-production" | "done"
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	BuildLog   string `json:"build_log,omitempty"`
	PreviewURL string `json:"preview_url,omitempty"`
	Status     string `json:"status,omitempty"`
	Ticket     string `json:"ticket,omitempty"`
	ArchiveID  string `json:"archive_id,omitempty"`
}

// handleSelfUpdate implements the "Update Diri" option in the New
// Deployment dropdown:
//  1. Receive a target repo ("owner/repo") + a .zip upload, and extract it
//     in memory.
//  2. Create a throwaway Vercel *project* (named uniquely per run, unrelated
//     to any real project) and deploy the extracted files to it as a
//     one-off build, purely to make sure the zip actually builds — nothing
//     touches GitHub yet.
//  3. Only if that test build reaches READY does it push the extracted
//     files to the target GitHub repo/branch. A broken zip never reaches
//     the real codebase.
//  4. After GitHub is updated, the browser polls the Vercel check attached to
//     that exact commit. The update is only reported as successful when the
//     production check succeeds; rate limits and production build failures are
//     surfaced as failures instead of false success.
//
// ZIP bytes are uploaded again for the GitHub stage; a signed ticket checks
// that they are exactly the bytes whose build was approved.
func handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	// GET: DevControl's own repo and branch, read from Vercel's system env,
	// so the Update Diri form can lock them and only ask for the ZIP.
	if r.Method == http.MethodGet {
		w.Header().Set("Cache-Control", "no-store")
		owner, slug := strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_OWNER")), strings.TrimSpace(os.Getenv("VERCEL_GIT_REPO_SLUG"))
		repo := ""
		if owner != "" && slug != "" { repo = owner + "/" + slug }
		util.JSON(w, http.StatusOK, map[string]string{"repo": repo, "branch": strings.TrimSpace(os.Getenv("VERCEL_GIT_COMMIT_REF"))})
		return
	}
	if r.Method != http.MethodPost {
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	vercelToken := os.Getenv("VERCEL_TOKEN")
	if githubToken == "" || vercelToken == "" {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf(
			"self-update belum dikonfigurasi: set GITHUB_TOKEN dan VERCEL_TOKEN di environment variables Vercel",
		))
		return
	}

	if err := r.ParseMultipartForm(maxSelfUpdateZipBytes); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("gagal membaca form: %w", err))
		return
	}

	repo := strings.TrimSpace(r.FormValue("repo"))
	branch := orDefault(r.FormValue("branch"), "main")
	owner, repoName, ok := splitRepo(repo)
	if !ok {
		util.Error(w, http.StatusBadRequest, fmt.Errorf(`format repo harus "owner/repo"`))
		return
	}
	phase := orDefault(strings.TrimSpace(r.FormValue("phase")), "start")
	if phase != "start" && phase != "baseline" && phase != "start-test" && phase != "status" && phase != "commit" && phase != "production-status" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("tahap update tidak valid"))
		return
	}
	if phase == "start" && !auth.RecentlyConfirmed(r) { auth.ReauthRequired(w); return }
	if phase == "production-status" {
		handleSelfUpdateProductionStatus(w, r, githubToken, vercelToken, owner, repoName, branch)
		return
	}
	if phase == "status" {
		handleSelfUpdateStatus(w, r, vercelToken, repo, branch)
		return
	}

	file, header, err := r.FormFile("zip")
	if err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("file zip wajib diunggah: %w", err))
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("hanya file .zip yang diterima"))
		return
	}

	zipBytes, err := io.ReadAll(io.LimitReader(file, maxSelfUpdateZipBytes+1))
	if err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("gagal membaca file zip: %w", err))
		return
	}
	if len(zipBytes) > maxSelfUpdateZipBytes {
		util.Error(w, http.StatusRequestEntityTooLarge, fmt.Errorf("file zip melebihi batas 4MB"))
		return
	}
	if phase == "start" {
		if err := prepareArchiveStorage(); err != nil {
			util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("penyiapan D1/R2 gagal: %w", err))
			return
		}
		if err := prepareDeploymentRunner(r); err != nil {
			util.Error(w, http.StatusPreconditionFailed, err); return
		}
	}
	store, err := archive.New()
	if err != nil {
		util.Error(w, http.StatusPreconditionFailed, err); return
	}
	target := repo + "@" + branch
	archiveID := strings.TrimSpace(r.FormValue("archive_id"))
	if phase == "start" {
		record, saveErr := store.Save("self", target, header.Filename, "upload", "pending", zipBytes)
		if saveErr != nil {
			util.Error(w, http.StatusBadGateway, fmt.Errorf("ZIP tidak dapat disimpan, update dibatalkan: %w", saveErr)); return
		}
		archiveID = record.ID
		if err := startDeploymentPipeline(archiveID, "self_update", target, strings.ToLower(repo), "",
			[4]string{"Ekstrak ZIP", "Uji Vercel", "Perbarui GitHub", "Production Vercel"}); err != nil {
			_ = store.Fail(archiveID)
			util.Error(w, http.StatusConflict, err)
			return
		}
	} else if phase == "commit" {
		record, checkErr := store.Get(archiveID)
		if checkErr != nil || record.Scope != "self" || record.Target != target || record.SizeBytes != int64(len(zipBytes)) || record.SHA256 != archive.Digest(zipBytes) ||
			(record.Status != "pending" && record.Status != "current") {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip ZIP sesi update diri tidak cocok")); return
		}
		if err := requireActiveDeployment(archiveID); err != nil { util.Error(w, http.StatusConflict, err); return }
		updateDeploymentStage(archiveID, 3, "Running")
	} else if err := store.Verify(archiveID, "self", target, zipBytes); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip ZIP wajib tersimpan sebelum update diri: %w", err)); return
	} else if err := requireActiveDeployment(archiveID); err != nil {
		util.Error(w, http.StatusConflict, err); return
	}

	// From here on the request itself is valid, so failures are reported as
	// part of the step pipeline (HTTP 200 + selfUpdateResult) instead of a
	// bare HTTP error, so the modal can show exactly which step failed.
	files, err := extractZip(zipBytes)
	if err != nil {
		_ = store.Fail(archiveID)
		position := 1
		if phase == "commit" { position = 3 }
		if phase == "baseline" || phase == "start-test" { position = 2 }
		updateDeploymentStage(archiveID, position, "Failed")
		msg := "[Ekstrak ZIP] Gagal mengekstrak file zip: " + err.Error()
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "extract", OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	if len(files) == 0 {
		_ = store.Fail(archiveID)
		position := 1
		if phase == "commit" { position = 3 }
		if phase == "baseline" || phase == "start-test" { position = 2 }
		updateDeploymentStage(archiveID, position, "Failed")
		msg := "[Ekstrak ZIP] Zip kosong atau tidak berisi file yang valid"
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "extract", OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	if phase == "commit" {
		ticket, ticketErr := verifyBuildTicket(vercelToken, r.FormValue("ticket"))
		if ticketErr != nil || ticket.Stage != "self-push" || ticket.Repo != repo || ticket.Branch != branch || ticket.Project == "" || ticket.ZipSHA != zipDigest(zipBytes) || ticket.ArchiveID != archiveID {
			updateDeploymentStage(archiveID, 3, "Failed")
			util.Error(w, http.StatusBadRequest, fmt.Errorf("sesi uji build tidak valid; unggah ZIP yang sama dan mulai ulang"))
			return
		}
		record, checkErr := store.Get(archiveID)
		if checkErr != nil {
			updateDeploymentStage(archiveID, 3, "Failed")
			util.Error(w, http.StatusBadGateway, checkErr); return
		}
		if record.Status == "current" {
			cleanupTestProject(vercelToken, ticket.Project)
			commitSHA, headErr := currentGithubBranchSHA(githubToken, owner, repoName, branch)
			if headErr != nil {
				updateDeploymentStage(archiveID, 4, "Failed")
				util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-production", OK: false, ArchiveID: archiveID,
					Message: "GitHub sudah diperbarui, tetapi commit untuk verifikasi Vercel tidak dapat dibaca: " + headErr.Error()})
				return
			}
			startSelfUpdateProductionWatch(w, vercelToken, repo, branch, commitSHA, archiveID)
			return
		}
		finishSelfUpdate(w, githubToken, vercelToken, owner, repoName, branch, files, ticket.URL, ticket.Project, store, archiveID)
		return
	}
	if phase == "start" {
		updateDeploymentStage(archiveID, 1, "Success")
		updateDeploymentStage(archiveID, 2, "Running")
		if err := enqueueDeployment(archiveID, "baseline", "", branch, "Production"); err != nil {
			_ = store.Fail(archiveID)
			updateDeploymentStage(archiveID, 2, "Failed")
			util.JSON(w, http.StatusOK, selfUpdateResult{Step: "extract", OK: false, ArchiveID: archiveID,
				Message: "ZIP tersimpan, tetapi runner tidak dapat dijadwalkan: " + err.Error()})
			return
		}
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "extract", OK: true, Status: "pending", ArchiveID: archiveID,
			Message: "ZIP tersimpan dan diekstrak; tahap berikutnya berjalan otomatis di server."})
		return
	}
	if phase == "baseline" {
		if backupErr := ensureGithubBaseline(store, githubToken, "self", target, repo, branch); backupErr != nil {
			_ = store.Fail(archiveID)
			updateDeploymentStage(archiveID, 2, "Failed")
			util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: false, ArchiveID: archiveID,
				Message: "Versi sebelumnya belum dapat diarsipkan; update dibatalkan: " + backupErr.Error()})
			return
		}
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: true, ArchiveID: archiveID,
			Message: "Versi sebelumnya diarsipkan; memulai uji build Vercel."})
		return
	}
	updateDeploymentStage(archiveID, 2, "Running")
	logLiveLog("INFO", fmt.Sprintf("Self-update: mengekstrak %d file dari zip untuk %s/%s", len(files), owner, repoName))

	deploymentID, previewURL, tempProject, _, _, err := createVercelTestDeployment(vercelToken, repoName, files)
	if err != nil {
		_ = store.Fail(archiveID)
		updateDeploymentStage(archiveID, 2, "Failed")
		// A rejected deployment can still have created its temporary project.
		// Removing this unique name is safe even when Vercel returned 404.
		if cleanupErr := deleteVercelProject(vercelToken, tempProject); cleanupErr != nil {
			logLiveLog("WARN", "Gagal membersihkan project uji Vercel: "+cleanupErr.Error())
		}
		msg := "[Uji Build Vercel] Gagal membuat deployment uji di Vercel: " + err.Error()
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	ticket, ticketErr := signBuildTicket(vercelToken, buildTicket{
		Stage: "self-test", Repo: repo, Branch: branch, ZipSHA: zipDigest(zipBytes),
		DeploymentID: deploymentID, Project: tempProject, URL: previewURL, ArchiveID: archiveID,
	})
	if ticketErr != nil {
		cleanupTestProject(vercelToken, tempProject)
		updateDeploymentStage(archiveID, 2, "Failed")
		util.Error(w, http.StatusInternalServerError, ticketErr)
		return
	}
	logLiveLog("INFO", fmt.Sprintf("Self-update: build uji dimulai (project %q, deployment %s)", tempProject, deploymentID))
	util.JSON(w, http.StatusOK, selfUpdateResult{
		Step: "vercel-test", OK: true, Status: "pending", Ticket: ticket, PreviewURL: previewURL, ArchiveID: archiveID,
		Message: "ZIP disimpan; build uji dimulai. Tahap berikutnya berjalan otomatis di server.",
	})
}

func handleSelfUpdateStatus(w http.ResponseWriter, r *http.Request, token, repo, branch string) {
	ticket, err := verifyBuildTicket(token, r.FormValue("ticket"))
	if err != nil || ticket.Stage != "self-test" || ticket.Repo != repo || ticket.Branch != branch || ticket.DeploymentID == "" || ticket.Project == "" || ticket.ArchiveID == "" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("sesi pemantauan build tidak valid atau kedaluwarsa"))
		return
	}
	store, storeErr := archive.New()
	if storeErr != nil { util.Error(w, http.StatusPreconditionFailed, storeErr); return }
	if record, recordErr := store.Get(ticket.ArchiveID); recordErr != nil || record.Scope != "self" || record.Target != repo+"@"+branch || record.SHA256 != ticket.ZipSHA || record.Status != "pending" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip sesi update diri tidak valid")); return
	}
	if err := requireActiveDeployment(ticket.ArchiveID); err != nil { util.Error(w, http.StatusConflict, err); return }
	state, _, detail, checkErr := getVercelBuildStatus(token, ticket.DeploymentID)
	if checkErr != nil {
		util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal memeriksa status Vercel: %w", checkErr))
		return
	}
	if state != "READY" && state != "ERROR" && state != "CANCELED" {
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: true, Status: "pending", Ticket: r.FormValue("ticket"),
			Message: "Vercel: " + state + ". Build masih berjalan; status akan diperiksa lagi."})
		return
	}
	if state != "READY" {
		_ = store.Fail(ticket.ArchiveID)
		updateDeploymentStage(ticket.ArchiveID, 2, "Failed")
		msg := "[Uji Build Vercel] Build gagal (status: " + state + ")"
		buildLog, logErr := getVercelBuildLog(token, ticket.DeploymentID)
		if detail != "" {
			msg += ": " + detail
		} else if buildLog != "" {
			msg += ": " + vercelFailureSummary(buildLog)
		}
		if logErr != nil {
			// Keep the failed test available in Vercel when its logs could not be
			// read. Deleting it here would permanently hide the build error.
			msg += ". Log belum dapat dibaca (" + logErr.Error() + "); project uji " + ticket.Project + " tetap ada sementara di Vercel untuk diperiksa, lalu dibersihkan otomatis."
			recordOrphanTestProject(ticket.Project)
		} else {
			cleanupTestProject(token, ticket.Project)
		}
		logLiveLog("ERROR", "Self-update dibatalkan — "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: false, Message: msg, BuildLog: buildLog, PreviewURL: ticket.URL, ArchiveID: ticket.ArchiveID})
		return
	}
	next, signErr := signBuildTicket(token, buildTicket{Stage: "self-push", Repo: repo, Branch: branch, ZipSHA: ticket.ZipSHA, URL: ticket.URL, Project: ticket.Project, ArchiveID: ticket.ArchiveID})
	if signErr != nil {
		updateDeploymentStage(ticket.ArchiveID, 2, "Failed")
		util.Error(w, http.StatusInternalServerError, signErr); return
	}
	updateDeploymentStage(ticket.ArchiveID, 2, "Success")
	updateDeploymentStage(ticket.ArchiveID, 3, "Running")
	util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: true, Status: "ready", Ticket: next, ArchiveID: ticket.ArchiveID,
		Message: "Build uji Vercel berhasil; memperbarui GitHub...", PreviewURL: ticket.URL})
}

func finishSelfUpdate(w http.ResponseWriter, githubToken, vercelToken, owner, repoName, branch string, files []selfUpdateFile, previewURL, tempProject string, store *archive.Store, archiveID string) {
	defer cleanupTestProject(vercelToken, tempProject)

	logLiveLog("INFO", "Self-update: build Vercel lulus, memperbarui GitHub...")
	// Production of DevControl is built from this commit, so stale files are
	// removed here (after the Uji Vercel build passed without them); leaving
	// them for a later commit would build something that was never tested.
	commitSHA, syncPlan, err := pushFilesToGitHub(githubToken, owner, repoName, branch, files, githubPushOptions{DeleteStale: true})
	setSyncNote(archiveID, syncPlan.Summary())
	if err != nil {
		_ = store.Fail(archiveID)
		updateDeploymentStage(archiveID, 3, "Failed")
		msg := "[Perbarui GitHub] Build lulus tapi gagal memperbarui GitHub: " + err.Error()
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "github-push", OK: false, Message: msg, PreviewURL: previewURL, ArchiveID: archiveID})
		return
	}

	if err := store.Promote(archiveID, "self", owner+"/"+repoName+"@"+branch); err != nil {
		updateDeploymentStage(archiveID, 3, "Failed")
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "github-push", OK: false, Message: "GitHub diperbarui, tetapi arsip ZIP aktif gagal dicatat: " + err.Error(), ArchiveID: archiveID})
		return
	}
	logLiveLog("INFO", fmt.Sprintf("Self-update: GitHub %s/%s@%s diperbarui; menunggu check production Vercel", owner, repoName, branch))
	startSelfUpdateProductionWatch(w, vercelToken, owner+"/"+repoName, branch, commitSHA, archiveID)
}

func startSelfUpdateProductionWatch(w http.ResponseWriter, signingSecret, repo, branch, commitSHA, archiveID string) {
	if commitSHA == "" {
		updateDeploymentStage(archiveID, 4, "Failed")
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-production", OK: false, ArchiveID: archiveID,
			Message: "GitHub diperbarui, tetapi SHA commit untuk verifikasi Vercel kosong"})
		return
	}
	ticket, err := signBuildTicket(signingSecret, buildTicket{
		Stage: "self-production", Repo: repo, Branch: branch, CommitSHA: commitSHA, ArchiveID: archiveID,
	})
	if err != nil {
		updateDeploymentStage(archiveID, 4, "Failed")
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-production", OK: false, ArchiveID: archiveID,
			Message: "GitHub diperbarui, tetapi sesi verifikasi Vercel gagal dibuat: " + err.Error()})
		return
	}
	updateDeploymentStage(archiveID, 3, "Success")
	updateDeploymentStage(archiveID, 4, "Running")
	util.JSON(w, http.StatusOK, selfUpdateResult{
		Step: "vercel-production", OK: true, Status: "pending", Ticket: ticket, ArchiveID: archiveID,
		Message: "GitHub berhasil diperbarui; menunggu hasil deployment production Vercel...",
	})
}

func handleSelfUpdateProductionStatus(w http.ResponseWriter, r *http.Request, githubToken, signingSecret, owner, repoName, branch string) {
	ticket, err := verifyBuildTicket(signingSecret, r.FormValue("ticket"))
	repo := owner + "/" + repoName
	if err != nil || ticket.Stage != "self-production" || ticket.Repo != repo || ticket.Branch != branch ||
		ticket.CommitSHA == "" || ticket.ArchiveID == "" || ticket.CreatedAt == 0 {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("sesi verifikasi deployment production tidak valid atau kedaluwarsa"))
		return
	}
	store, storeErr := archive.New()
	if storeErr != nil {
		util.Error(w, http.StatusPreconditionFailed, storeErr)
		return
	}
	record, recordErr := store.Get(ticket.ArchiveID)
	if recordErr != nil || record.Scope != "self" || record.Target != repo+"@"+branch || record.Status != "current" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip update tidak cocok dengan commit production"))
		return
	}
	if err := requireActiveDeployment(ticket.ArchiveID); err != nil {
		rows, queryErr := d1.Query(`SELECT status FROM deployment_jobs WHERE id = ? LIMIT 1`, ticket.ArchiveID)
		if queryErr == nil && len(rows) > 0 && rows[0]["status"] == "Success" {
			util.JSON(w, http.StatusOK, selfUpdateResult{Step: "done", OK: true, Status: "ready", ArchiveID: ticket.ArchiveID,
				Message: "GitHub berhasil diperbarui dan deployment production Vercel sudah siap"})
			return
		}
		util.Error(w, http.StatusConflict, err); return
	}

	state, detail, detailsURL, checkErr := githubVercelCheckStatus(githubToken, owner, repoName, ticket.CommitSHA)
	if checkErr != nil {
		util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal membaca check production Vercel dari GitHub: %w", checkErr))
		return
	}
	if state == "missing" && time.Since(time.Unix(ticket.CreatedAt, 0)) < 5*time.Minute {
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-production", OK: true, Status: "pending", Ticket: r.FormValue("ticket"), ArchiveID: ticket.ArchiveID,
			Message: "GitHub sudah diperbarui; menunggu Vercel membuat check deployment production..."})
		return
	}
	if state == "pending" {
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-production", OK: true, Status: "pending", Ticket: r.FormValue("ticket"), ArchiveID: ticket.ArchiveID,
			Message: "Deployment production Vercel masih berjalan; status akan diperiksa lagi...", PreviewURL: detailsURL})
		return
	}
	if state != "success" {
		if detail == "" {
			detail = "Check Vercel tidak muncul dalam 5 menit. Pastikan integrasi GitHub–Vercel aktif untuk repo dan branch ini."
		}
		updateDeploymentStage(ticket.ArchiveID, 4, "Failed")
		msg := "[Production Vercel] GitHub sudah diperbarui, tetapi deployment production gagal: " + detail
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-production", OK: false, Message: msg, BuildLog: detail,
			PreviewURL: detailsURL, ArchiveID: ticket.ArchiveID})
		return
	}

	updateDeploymentStage(ticket.ArchiveID, 4, "Success")
	logActivity("Self-update berhasil", fmt.Sprintf("%s@%s berhasil dideploy oleh Vercel", repo, branch), "check")
	logLiveLog("INFO", fmt.Sprintf("Self-update selesai: %s@%s berhasil online melalui Vercel", repo, branch))
	util.JSON(w, http.StatusOK, selfUpdateResult{Step: "done", OK: true, Status: "ready", ArchiveID: ticket.ArchiveID,
		Message: "GitHub berhasil diperbarui dan deployment production Vercel sudah siap", PreviewURL: detailsURL})
}

func splitRepo(repo string) (owner, name string, ok bool) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// extractZip reads every regular file out of a zip archive held in memory.
// Paths are cleaned and any entry that tries to escape the extraction root
// (a "zip-slip" attempt) is skipped.
func extractZip(data []byte) ([]selfUpdateFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	var out []selfUpdateFile
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		cleaned := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
			continue
		}
		// __MACOSX/.DS_Store must go first: an extra top-level __MACOSX folder
		// would otherwise stop stripCommonRootDir from finding the project root.
		if reposync.IsJunk(cleaned) { continue }
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, selfUpdateFile{path: cleaned, data: content})
	}
	stripCommonRootDir(out)
	// Output of npm install / builds (node_modules, .next, …) is never
	// deployed or committed; Vercel installs and builds it itself.
	kept := out[:0]
	for _, file := range out {
		if !reposync.IsGenerated(file.path) { kept = append(kept, file) }
	}
	return kept, nil
}

// stripCommonRootDir removes a single shared top-level folder from every
// entry's path, in place. Zips made by compressing a project folder (or
// GitHub's own "Download ZIP") wrap every file in one outer folder, e.g.
// "devcontrol/api/gateway.go" instead of "api/gateway.go". Pushed verbatim,
// that outer folder name becomes a *new*, wrong path in the target repo —
// creating a stray nested copy — instead of updating the real file at the
// repo root, so it silently looks like the update never applied. If every
// entry shares exactly one top-level segment, it's dropped; anything more
// ambiguous is left untouched.
func stripCommonRootDir(files []selfUpdateFile) {
	if len(files) == 0 {
		return
	}
	first := strings.SplitN(files[0].path, "/", 2)
	if len(first) != 2 {
		return // first file already sits at the root; nothing to strip
	}
	root := first[0]
	for _, f := range files {
		parts := strings.SplitN(f.path, "/", 2)
		if len(parts) != 2 || parts[0] != root {
			return // not every entry shares the same top-level folder
		}
	}
	for i := range files {
		files[i].path = strings.SplitN(files[i].path, "/", 2)[1]
	}
}

type vercelDeployResponse struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func toVercelFiles(files []selfUpdateFile) []vercelapp.File {
	out := make([]vercelapp.File, 0, len(files))
	for _, file := range files { out = append(out, vercelapp.File{Path: file.path, Data: file.data}) }
	return out
}

// createVercelTestDeployment uploads the extracted files inline (base64)
// and asks Vercel to build a preview deployment from them under a
// brand-new, uniquely-named *temporary* project — purely to validate the
// zip. It is never linked to GitHub and never reuses an existing/real
// Vercel project (VERCEL_PROJECT_ID is intentionally not consulted here),
// so a self-update test run can never show up in a real project's
// deployment history. The returned tempProject name is what the caller
// must delete afterwards via deleteVercelProject once the test is done.
// framework is the preset the build really used, for the production step.
func createVercelTestDeployment(token, repoName string, files []selfUpdateFile) (id string, previewURL string, tempProject string, framework string, rootDirectory string, err error) {
	tempProject = temporaryVercelProjectName(repoName)
	rootDirectory, err = vercelapp.WebRoot(toVercelFiles(files))
	if err != nil { return "", "", tempProject, "", "", err }
	framework = vercelapp.DetectFramework(vercelapp.FilesAtRoot(toVercelFiles(files), rootDirectory))
	id, previewURL, framework, err = createVercelDeployment(token, tempProject, "", files, framework, rootDirectory)
	if err != nil { return "", "", tempProject, "", rootDirectory, err }
	return id, previewURL, tempProject, framework, rootDirectory, nil
}

// createVercelDeployment starts either an isolated test or a real production
// deployment from the ZIP files. The framework preset is always explicit
// (package.json, or the one Vercel itself detects): skipping detection left
// Vite/CRA apps in a project without a framework, which served the source
// folder instead of dist/ and showed Vercel's 404 NOT_FOUND to visitors.
func createVercelDeployment(token, project, target string, files []selfUpdateFile, framework, rootDirectory string, projectID ...string) (id string, deploymentURL string, usedFramework string, err error) {
	deployment, used, err := vercelapp.New(token).CreateFileDeployment(project, target, toVercelFiles(files), framework, rootDirectory, projectID...)
	if err != nil { return "", "", "", err }
	return deployment.ID, deployment.URL, used, nil
}

// deleteVercelProject removes a Vercel project (and every deployment under
// it) by name. Used to tear down the throwaway project createVercelTestDeployment
// creates for each self-update test run, so test runs never accumulate as
// clutter in the Vercel account. A 404 (already gone) is treated as success.
func deleteVercelProject(token, nameOrID string) error {
	endpoint := "https://api.vercel.com/v9/projects/" + nameOrID
	if team := os.Getenv("VERCEL_TEAM_ID"); team != "" {
		endpoint += "?teamId=" + team
	}

	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		var out vercelDeployResponse
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if out.Error != nil && out.Error.Message != "" {
			return fmt.Errorf("%s", out.Error.Message)
		}
		return fmt.Errorf("HTTP %d dari Vercel", resp.StatusCode)
	}
	return nil
}

func sanitizeVercelName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "devcontrol-self-update"
	}
	return out
}

// Keep production project names stable, short, and distinct for long repo names.
func vercelAppProjectName(owner, repo string) string {
	name := sanitizeVercelName("devcontrol-" + owner + "-" + repo)
	if len(name) <= 80 { return name }
	digest := sha256.Sum256([]byte(owner + "/" + repo))
	return strings.Trim(name[:70], "-") + fmt.Sprintf("-%x", digest[:4])
}

// temporaryVercelProjectName builds a name that's unique per test build
// (repo name + a nanosecond-based suffix) so the test deployment always
// lands in its own disposable project instead of an existing/real one, no
// matter how many "Aplikasi Baru"/"Update Aplikasi"/"Update Diri" runs go
// back to back. Vercel project names are capped at 100 chars and limited to
// lowercase alphanumerics + hyphens; this stays comfortably under that.
func temporaryVercelProjectName(repoName string) string {
	base := sanitizeVercelName(repoName)
	suffix := fmt.Sprintf("test-%d", time.Now().UnixNano())
	const maxLen = 80
	if room := maxLen - len(suffix) - 1; len(base) > room {
		if room < 1 {
			base = "app"
		} else {
			base = strings.Trim(base[:room], "-")
		}
	}
	return base + "-" + suffix
}

type vercelStatusResponse struct {
	ReadyState string `json:"readyState"`
	URL        string `json:"url"`
	Alias      []string `json:"alias"`
	AliasAssigned bool `json:"aliasAssigned"`
	AliasError *struct { Message string `json:"message"` } `json:"aliasError"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// getVercelBuildStatus returns candidates for the public link, with the
// stable Vercel alias first and the immutable deployment URL last. READY
// alone cannot establish that any of those links actually serves the app.
func getVercelBuildStatus(token, id string) (state string, urls []string, detail string, err error) {
	endpoint := "https://api.vercel.com/v13/deployments/" + url.PathEscape(id)
	if team := os.Getenv("VERCEL_TEAM_ID"); team != "" { endpoint += "?teamId=" + url.QueryEscape(team) }
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil { return "", nil, "", err }
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return "", nil, "", err }
	defer resp.Body.Close()
	var out vercelStatusResponse
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil { return "", nil, "", err }
	if resp.StatusCode >= 300 {
		if out.Error != nil && out.Error.Message != "" { return "", nil, "", fmt.Errorf("%s", out.Error.Message) }
		return "", nil, "", fmt.Errorf("HTTP %d dari Vercel", resp.StatusCode)
	}
	if out.ReadyState == "" { return "", nil, "", fmt.Errorf("Vercel belum mengembalikan status deployment") }
	if out.AliasAssigned {
		// Probe at most two aliases to keep this status request within the
		// function timeout. Prefer the stable .vercel.app project domain.
		for _, alias := range out.Alias {
			if strings.HasSuffix(strings.ToLower(alias), ".vercel.app") {
				urls = append(urls, "https://"+alias)
				break
			}
		}
		for _, alias := range out.Alias {
			candidate := "https://" + alias
			if alias != "" && (len(urls) == 0 || urls[0] != candidate) {
				urls = append(urls, candidate)
				break
			}
		}
	}
	if out.URL != "" {
		candidate := "https://" + out.URL
		found := false
		for _, existing := range urls { if existing == candidate { found = true; break } }
		if !found { urls = append(urls, candidate) }
	}
	if out.Error != nil { detail = out.Error.Message }
	if out.AliasError != nil && detail == "" { detail = out.AliasError.Message }
	return out.ReadyState, urls, detail, nil
}

// Vercel's deployment status often contains only ERROR with no error.message.
// Read the finished build's events before deleting the temporary project so
// the admin can see the compiler or dependency error that actually occurred.
func getVercelBuildLogOnce(token, id string) (string, error) {
	query := url.Values{"direction": {"backward"}, "follow": {"0"}, "limit": {"100"}}
	if team := os.Getenv("VERCEL_TEAM_ID"); team != "" { query.Set("teamId", team) }
	endpoint := "https://api.vercel.com/v3/deployments/" + url.PathEscape(id) + "/events?" + query.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil { return "", err }
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return "", err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return "", fmt.Errorf("HTTP %d", resp.StatusCode) }
	var events []struct {
		Type string `json:"type"`
		Payload struct { Text string `json:"text"` } `json:"payload"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&events); err != nil { return "", err }
	var lines []string
	for i := len(events)-1; i >= 0; i-- {
		event := events[i]
		if event.Type != "stdout" && event.Type != "stderr" && event.Type != "fatal" && event.Type != "command" && event.Type != "exit" { continue }
		for _, line := range strings.Split(strings.ReplaceAll(event.Payload.Text, "\r", ""), "\n") {
			line = strings.TrimSpace(line)
			if line != "" { lines = append(lines, line) }
		}
	}
	if len(lines) == 0 { return "", fmt.Errorf("log build kosong") }
	// The final lines contain the error and its context; bound the response so
	// build output cannot make a status poll too large for a serverless request.
	if len(lines) > 30 { lines = lines[len(lines)-30:] }
	log := strings.Join(lines, "\n")
	if len(log) > 4000 { log = log[len(log)-4000:] }
	return log, nil
}

// getVercelBuildLog retries getVercelBuildLogOnce: Vercel's events endpoint
// can briefly lag behind a deployment's readyState flipping to ERROR/CANCELED,
// which otherwise showed up as a false "log build kosong" right as the build
// actually finished. Total worst case ~7s, well inside the request's budget.
func getVercelBuildLog(token, id string) (string, error) {
	var log string
	var err error
	for attempt, delay := range []time.Duration{0, 1500 * time.Millisecond, 3 * time.Second} {
		if attempt > 0 { time.Sleep(delay) }
		if log, err = getVercelBuildLogOnce(token, id); err == nil { return log, nil }
	}
	return "", err
}

// recordOrphanTestProject remembers a throwaway test project whose build log
// still could not be read after retries, so sweepOrphanTestProjects can
// clean it up later instead of it sitting in Vercel forever. Cleanup is
// deliberately not immediate: the admin can still open the project in Vercel
// for a while first (see getVercelBuildLog's comment on why it was kept).
func recordOrphanTestProject(project string) {
	if project == "" { return }
	_, _ = d1.Query(`INSERT OR IGNORE INTO vercel_orphan_projects (project) VALUES (?)`, project)
}

// rollbackFailedNewApp removes what a failed "Aplikasi Baru" run created
// itself — its Vercel project and its GitHub repo — so a first deployment
// that never went online leaves nothing behind in Vercel or GitHub. A repo
// or project that existed before the run (for example a manual Vercel
// import) is never touched, and nothing is removed once the app is saved as
// online. keepProjectForLog delays the project removal by 30 minutes when
// its build log could not be read yet, so the log can still be recovered.
// Returns a sentence for the pipeline message ("" when nothing was created).
func rollbackFailedNewApp(name, owner, repoName string, createdRepo, createdProject bool, project string, keepProjectForLog bool) string {
	if !createdRepo && !(createdProject && project != "") { return "" }
	if rows, err := d1.Query(`SELECT 1 AS found FROM services WHERE name = ? LIMIT 1`, name); err != nil || len(rows) > 0 { return "" }
	notes := []string{}
	if createdProject && project != "" {
		if keepProjectForLog {
			recordOrphanTestProject(project)
			notes = append(notes, "project Vercel "+project+" dihapus otomatis 30 menit lagi (log build masih dibaca ulang)")
		} else if err := deleteVercelProject(os.Getenv("VERCEL_TOKEN"), project); err != nil {
			recordOrphanTestProject(project)
			notes = append(notes, "project Vercel "+project+" belum dapat dihapus ("+err.Error()+"), dicoba lagi otomatis")
		} else {
			notes = append(notes, "project Vercel "+project+" dihapus")
		}
	}
	if createdRepo && owner != "" && repoName != "" {
		if err := projectdelete.DeleteGithubRepo(os.Getenv("GITHUB_TOKEN"), owner, repoName); err != nil {
			notes = append(notes, "repo GitHub "+owner+"/"+repoName+" belum dapat dihapus ("+err.Error()+"); hapus lewat Projects → menu kartu → Hapus aplikasi")
		} else {
			notes = append(notes, "repo GitHub "+owner+"/"+repoName+" dihapus")
		}
	}
	if len(notes) == 0 { return "" }
	logLiveLog("INFO", "Aplikasi baru "+name+" gagal sebelum online; dibersihkan: "+strings.Join(notes, "; "))
	return " Aplikasi baru ini belum pernah online, jadi yang dibuat oleh proses ini dibersihkan: " + strings.Join(notes, "; ") + ". Perbaiki penyebabnya lalu jalankan Aplikasi Baru lagi dengan nama yang sama."
}

// sweepOrphanTestProjects deletes recorded test projects older than the grace
// window. Called from the same once-a-minute Cloudflare runner as other
// periodic cleanup, so orphans left by an unreadable build log never
// accumulate even if nobody opens DevControl again.
func sweepOrphanTestProjects() {
	token := os.Getenv("VERCEL_TOKEN")
	if token == "" { return }
	rows, err := d1.Query(`SELECT project FROM vercel_orphan_projects WHERE created_at <= datetime('now', '-30 minutes') LIMIT 20`)
	if err != nil { return }
	for _, row := range rows {
		project := rowText(row, "project")
		if project == "" { continue }
		if err := deleteVercelProject(token, project); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
			logLiveLog("WARN", "Gagal membersihkan project uji Vercel "+project+": "+err.Error())
			continue
		}
		_, _ = d1.Query(`DELETE FROM vercel_orphan_projects WHERE project = ?`, project)
	}
}

func vercelFailureSummary(log string) string {
	lines := strings.Split(log, "\n")
	for i := len(lines)-1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		lower := strings.ToLower(line)
		if strings.Contains(lower, "command") && strings.Contains(lower, "exited") { continue }
		if lower == "failed to compile." || lower == "error" { continue }
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") ||
			strings.Contains(lower, "undefined:") || strings.Contains(lower, "cannot find") || strings.Contains(lower, "not found") {
			if len(line) > 300 { line = line[:300] + "…" }
			return line
		}
	}
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > 300 { line = line[:300] + "…" }
	return line
}

// The ticket carries deployment context between independent serverless requests.
// Its HMAC binds the build result to the original ZIP and target repository.
type buildTicket struct {
	Stage string `json:"stage"`
	Mode string `json:"mode,omitempty"`
	Name string `json:"name,omitempty"`
	Repo string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	CommitSHA string `json:"commit_sha,omitempty"`
	// Set on a first deployment ("Aplikasi Baru") for what this run created
	// itself; if the app never goes online, exactly these are removed again.
	CreatedRepo bool `json:"created_repo,omitempty"`
	CreatedProject bool `json:"created_project,omitempty"`
	ZipSHA string `json:"zip_sha,omitempty"`
	ArchiveID string `json:"archive_id,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Project string `json:"project,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	OldProjectID string `json:"old_project_id,omitempty"`
	URL string `json:"url,omitempty"`
	Environment string `json:"environment,omitempty"`
	CreatedAt int64 `json:"created_at,omitempty"`
	Expires int64 `json:"expires"`
	// App deployments: framework preset proven by the test build, whether the
	// home page must be checked, and whether production is built from the
	// GitHub commit (project connected to the repo) instead of the ZIP upload.
	Framework string `json:"framework,omitempty"`
	RootDirectory string `json:"root_directory,omitempty"`
	Probe bool `json:"probe,omitempty"`
	Linked bool `json:"linked,omitempty"`
}

func zipDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func signBuildTicket(secret string, ticket buildTicket) (string, error) {
	if ticket.CreatedAt == 0 { ticket.CreatedAt = time.Now().Unix() }
	ticket.Expires = time.Now().Add(3 * time.Hour).Unix()
	data, err := json.Marshal(ticket)
	if err != nil { return "", err }
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyBuildTicket(secret, raw string) (buildTicket, error) {
	var ticket buildTicket
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(raw) > 4096 || secret == "" { return ticket, fmt.Errorf("sesi tidak valid") }
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil { return ticket, fmt.Errorf("sesi tidak valid") }
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(provided, mac.Sum(nil)) { return ticket, fmt.Errorf("sesi tidak valid") }
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(data, &ticket) != nil || ticket.Expires < time.Now().Unix() {
		return buildTicket{}, fmt.Errorf("sesi tidak valid atau kedaluwarsa")
	}
	return ticket, nil
}

func cleanupTestProject(token, project string) {
	if project == "" { return }
	if err := deleteVercelProject(token, project); err != nil {
		logLiveLog("WARN", "Gagal membersihkan project uji Vercel: "+err.Error())
	}
}

type githubRefResponse struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type githubSHAResponse struct {
	SHA string `json:"sha"`
}

type githubTreeEntry struct {
	Path    string  `json:"path"`
	Mode    string  `json:"mode"`
	Type    string  `json:"type"`
	SHA     string  `json:"sha,omitempty"`
	Content *string `json:"content,omitempty"`
}

type githubCheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
	Output struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
	} `json:"output"`
	App struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"app"`
}

type githubCheckRunsResponse struct {
	CheckRuns []githubCheckRun `json:"check_runs"`
}

type githubCommitStatus struct {
	State       string `json:"state"`
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
	Context     string `json:"context"`
	Creator struct {
		Login string `json:"login"`
	} `json:"creator"`
}

type githubCombinedStatusResponse struct {
	Statuses []githubCommitStatus `json:"statuses"`
}

// githubPushOptions controls how the ZIP replaces the repo contents.
type githubPushOptions struct {
	DeleteStale     bool   // remove repo files the ZIP no longer has (reposync rules)
	Message         string // commit message
	SkipIfUnchanged bool   // no commit when the resulting tree is identical
}

// pushFilesToGitHub publishes the ZIP as one atomic Git commit (a single push
// event, so Vercel starts at most one deployment). It first reads the repo's
// current file list and asks reposync which missing files are really unused:
// lockfiles, CI/Git metadata and licences are carried over, npm/build output
// committed by mistake is dropped, and a ZIP of the wrong folder cannot wipe
// the repo (mass-deletion guard). Real .env files are never published.
func pushFilesToGitHub(token, owner, repo, branch string, files []selfUpdateFile, options githubPushOptions) (string, reposync.Plan, error) {
	plan := reposync.Plan{SkippedSecrets: []string{}}
	publish := make([]selfUpdateFile, 0, len(files))
	for _, file := range files {
		if reposync.IsSecretEnv(file.path) { plan.SkippedSecrets = append(plan.SkippedSecrets, file.path); continue }
		publish = append(publish, file)
	}
	if len(publish) == 0 {
		return "", plan, fmt.Errorf("tidak ada file untuk didorong ke GitHub")
	}
	if options.Message == "" { options.Message = "devcontrol: sinkronkan paket ZIP" }

	client := &http.Client{Timeout: 25 * time.Second}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	parentSHA, exists, err := githubBranchHead(client, token, baseURL, branch)
	if err != nil {
		return "", plan, err
	}
	if !exists {
		// GitHub's Git Database API cannot create the first ref in an empty
		// repository. Bootstrap it with one Contents API commit, then perform
		// the normal atomic tree replacement below.
		if err := initializeGithubBranch(client, token, baseURL, branch, publish[0]); err != nil {
			return "", plan, fmt.Errorf("gagal menyiapkan branch %s: %w", branch, err)
		}
		if len(publish) == 1 {
			sha, found, headErr := waitGithubBranchHead(client, token, baseURL, branch)
			if headErr != nil { return "", plan, headErr }
			if !found { return "", plan, fmt.Errorf("GitHub belum membuat branch %s setelah inisialisasi", branch) }
			return sha, plan, nil
		}
		// The Contents API commit is accepted before the new ref is readable
		// through the Git Database API (replication lag on a brand-new repo),
		// so poll instead of reading once.
		parentSHA, exists, err = waitGithubBranchHead(client, token, baseURL, branch)
		if err != nil {
			return "", plan, err
		}
		if !exists {
			return "", plan, fmt.Errorf("GitHub belum membuat branch %s setelah inisialisasi", branch)
		}
	}

	entries := make([]githubTreeEntry, 0, len(publish))
	inZip := make(map[string]bool, len(publish))
	zipPaths := make([]string, 0, len(publish))
	for _, file := range publish {
		inZip[file.path] = true
		zipPaths = append(zipPaths, file.path)
		entry := githubTreeEntry{Path: file.path, Mode: "100644", Type: "blob"}
		if utf8.Valid(file.data) {
			entry.Content = new(string)
			*entry.Content = string(file.data)
		} else {
			blobSHA, err := createGithubBlob(client, token, baseURL, file.data)
			if err != nil {
				return "", plan, fmt.Errorf("gagal mengunggah blob %s: %w", file.path, err)
			}
			entry.SHA = blobSHA
		}
		entries = append(entries, entry)
	}

	// Compare with what GitHub has now and decide per file.
	parentTree, existing, truncated, listErr := githubCommitTree(client, token, baseURL, parentSHA)
	if listErr != nil {
		return "", plan, fmt.Errorf("gagal membaca daftar file GitHub (tidak ada yang diubah): %w", listErr)
	}
	request := map[string]interface{}{}
	if truncated {
		// GitHub could not list the whole repo: add/replace only, never delete.
		request["base_tree"] = parentTree
		plan.Blocked, plan.Reason = true, "repo terlalu besar untuk dibandingkan; file lama tidak dihapus"
	} else {
		repoPaths := make([]string, 0, len(existing))
		for _, item := range existing { if item.Type == "blob" { repoPaths = append(repoPaths, item.Path) } }
		skipped := plan.SkippedSecrets
		plan = reposync.PlanSync(zipPaths, repoPaths)
		plan.SkippedSecrets = skipped
		remove := map[string]bool{}
		if options.DeleteStale { for _, file := range plan.Removed() { remove[file] = true } }
		for _, item := range existing {
			if inZip[item.Path] || remove[item.Path] { continue }
			entries = append(entries, item) // carried over unchanged (same blob SHA)
		}
	}
	request["tree"] = entries

	var tree githubSHAResponse
	if _, err := githubJSONRequest(client, token, http.MethodPost, baseURL+"/git/trees", request, &tree); err != nil {
		return "", plan, fmt.Errorf("gagal membuat tree GitHub: %w", err)
	}
	if tree.SHA == "" {
		return "", plan, fmt.Errorf("GitHub tidak mengembalikan SHA tree")
	}
	if options.SkipIfUnchanged && tree.SHA == parentTree {
		return "", plan, nil
	}

	var commit githubSHAResponse
	if _, err := githubJSONRequest(client, token, http.MethodPost, baseURL+"/git/commits", map[string]interface{}{
		"message": options.Message,
		"tree":    tree.SHA,
		"parents": []string{parentSHA},
	}, &commit); err != nil {
		return "", plan, fmt.Errorf("gagal membuat commit GitHub: %w", err)
	}
	if commit.SHA == "" {
		return "", plan, fmt.Errorf("GitHub tidak mengembalikan SHA commit")
	}

	refURL := baseURL + "/git/refs/heads/" + url.PathEscape(branch)
	if _, err := githubJSONRequest(client, token, http.MethodPatch, refURL, map[string]interface{}{
		"sha": commit.SHA, "force": false,
	}, &githubRefResponse{}); err != nil {
		return "", plan, fmt.Errorf("gagal memperbarui branch %s: %w", branch, err)
	}
	return commit.SHA, plan, nil
}

// githubCommitTree lists every file of a commit (recursive). Entries keep
// their mode and blob SHA so they can be carried into a new tree as-is.
func githubCommitTree(client *http.Client, token, baseURL, commitSHA string) (string, []githubTreeEntry, bool, error) {
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := githubJSONRequest(client, token, http.MethodGet, baseURL+"/git/commits/"+url.PathEscape(commitSHA), nil, &commit); err != nil {
		return "", nil, false, err
	}
	if commit.Tree.SHA == "" { return "", nil, false, fmt.Errorf("commit %s tanpa tree", commitSHA) }
	var listing struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := githubJSONRequest(client, token, http.MethodGet, baseURL+"/git/trees/"+url.PathEscape(commit.Tree.SHA)+"?recursive=1", nil, &listing); err != nil {
		return commit.Tree.SHA, nil, false, err
	}
	entries := make([]githubTreeEntry, 0, len(listing.Tree))
	for _, item := range listing.Tree {
		// Folders are implied by paths; submodules ("commit") and symlinks are kept.
		if (item.Type == "blob" || item.Type == "commit") && item.SHA != "" {
			entries = append(entries, githubTreeEntry{Path: item.Path, Mode: item.Mode, Type: item.Type, SHA: item.SHA})
		}
	}
	return commit.Tree.SHA, entries, listing.Truncated, nil
}

// setSyncNote appends a GitHub-sync note to a pipeline; it is copied into the
// update history when the deployment succeeds.
func setSyncNote(id, note string) {
	if id == "" || note == "" { return }
	_, _ = d1.Query(`UPDATE deployment_jobs SET sync_note = CASE WHEN sync_note = '' THEN ? ELSE sync_note || ' · ' || ? END WHERE id = ?`, note, note, id)
}

func githubBranchHead(client *http.Client, token, baseURL, branch string) (string, bool, error) {
	var ref githubRefResponse
	status, err := githubJSONRequest(client, token, http.MethodGet, baseURL+"/git/ref/heads/"+url.PathEscape(branch), nil, &ref)
	if status == http.StatusNotFound || status == http.StatusConflict {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("gagal membaca branch %s: %w", branch, err)
	}
	if ref.Object.SHA == "" {
		return "", false, fmt.Errorf("GitHub tidak mengembalikan SHA branch %s", branch)
	}
	return ref.Object.SHA, true, nil
}

// waitGithubBranchHead polls the branch head for a short while. Right after
// the first Contents API commit in a new repository, GET /git/ref/heads/<branch>
// can still answer 404/409 for a few seconds even though the commit succeeded.
// Total wait stays under ~14s so the 60s function limit is never at risk.
func waitGithubBranchHead(client *http.Client, token, baseURL, branch string) (string, bool, error) {
	delays := []time.Duration{0, time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second, 5 * time.Second}
	for _, delay := range delays {
		if delay > 0 { time.Sleep(delay) }
		sha, found, err := githubBranchHead(client, token, baseURL, branch)
		if err != nil { return "", false, err }
		if found { return sha, true, nil }
	}
	return "", false, nil
}

func currentGithubBranchSHA(token, owner, repo, branch string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	sha, exists, err := githubBranchHead(client, token, baseURL, branch)
	if err != nil { return "", err }
	if !exists { return "", fmt.Errorf("branch %s tidak ditemukan", branch) }
	return sha, nil
}

// githubVercelCheckStatus reads the checks for the exact commit just pushed by
// self-update. Vercel reports both successful deployments and pre-build
// rejections (including daily deployment rate limits) through this check.
func githubVercelCheckStatus(token, owner, repo, commitSHA string) (string, string, string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	endpoint := baseURL + "/commits/" + url.PathEscape(commitSHA) + "/check-runs?filter=latest&per_page=100"
	var response githubCheckRunsResponse
	_, checkRunsErr := githubJSONRequest(client, token, http.MethodGet, endpoint, nil, &response)
	if checkRunsErr != nil {
		return "", "", "", checkRunsErr
	}

	var found, pending, succeeded bool
	detailsURL := ""
	for _, run := range response.CheckRuns {
		slug := strings.ToLower(strings.TrimSpace(run.App.Slug))
		appName := strings.ToLower(strings.TrimSpace(run.App.Name))
		checkName := strings.ToLower(strings.TrimSpace(run.Name))
		checkURL := strings.ToLower(strings.TrimSpace(run.DetailsURL))
		isVercel := slug == "vercel" || strings.Contains(slug, "vercel") || appName == "vercel" ||
			checkName == "vercel" || strings.HasPrefix(checkName, "vercel ") || strings.Contains(checkURL, "vercel.com")
		if !isVercel { continue }

		found = true
		if detailsURL == "" { detailsURL = run.DetailsURL }
		if run.Status != "completed" {
			pending = true
			continue
		}
		if run.Conclusion != "success" {
			return "failure", githubCheckRunDetail(run), run.DetailsURL, nil
		}
		succeeded = true
	}
	if found {
		if pending { return "pending", "", detailsURL, nil }
		if succeeded { return "success", "", detailsURL, nil }
		return "failure", "Vercel menyelesaikan check tanpa status sukses", detailsURL, nil
	}

	// Some Vercel Git integrations publish a classic commit status instead of
	// a Check Run. Read that API as a fallback so both GitHub representations
	// are covered.
	var combined githubCombinedStatusResponse
	statusEndpoint := baseURL + "/commits/" + url.PathEscape(commitSHA) + "/status"
	_, statusErr := githubJSONRequest(client, token, http.MethodGet, statusEndpoint, nil, &combined)
	if statusErr != nil {
		return "", "", "", statusErr
	}
	var statusFound, statusPending, statusSucceeded bool
	for _, status := range combined.Statuses {
		context := strings.ToLower(strings.TrimSpace(status.Context))
		targetURL := strings.ToLower(strings.TrimSpace(status.TargetURL))
		creator := strings.ToLower(strings.TrimSpace(status.Creator.Login))
		isVercel := context == "vercel" || strings.HasPrefix(context, "vercel ") || strings.Contains(targetURL, "vercel.com") || strings.Contains(creator, "vercel")
		if !isVercel { continue }
		statusFound = true
		if detailsURL == "" { detailsURL = status.TargetURL }
		switch strings.ToLower(status.State) {
		case "success":
			statusSucceeded = true
		case "pending":
			statusPending = true
		default:
			detail := strings.TrimSpace(status.Description)
			if detail == "" { detail = "Status Vercel selesai dengan hasil " + orDefault(status.State, "tidak diketahui") }
			return "failure", detail, status.TargetURL, nil
		}
	}
	if !statusFound { return "missing", "", "", nil }
	if statusPending { return "pending", "", detailsURL, nil }
	if statusSucceeded { return "success", "", detailsURL, nil }
	return "failure", "Vercel menyelesaikan status tanpa hasil sukses", detailsURL, nil
}

func githubCheckRunDetail(run githubCheckRun) string {
	parts := make([]string, 0, 4)
	for _, value := range []string{run.Output.Title, run.Output.Summary, run.Output.Text} {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
		if value == "" { continue }
		duplicate := false
		for _, existing := range parts {
			if existing == value { duplicate = true; break }
		}
		if !duplicate { parts = append(parts, value) }
	}
	detail := strings.Join(parts, "\n")
	if detail == "" {
		detail = fmt.Sprintf("Check %s selesai dengan status %s", orDefault(run.Name, "Vercel"), orDefault(run.Conclusion, "tidak diketahui"))
	}
	runes := []rune(detail)
	if len(runes) > 4000 { detail = string(runes[:4000]) + "…" }
	return detail
}

func initializeGithubBranch(client *http.Client, token, baseURL, branch string, file selfUpdateFile) error {
	parts := strings.Split(file.path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	endpoint := baseURL + "/contents/" + strings.Join(parts, "/")
	_, err := githubJSONRequest(client, token, http.MethodPut, endpoint, map[string]interface{}{
		"message": "devcontrol: inisialisasi paket ZIP",
		"content": base64.StdEncoding.EncodeToString(file.data),
		"branch":  branch,
	}, nil)
	return err
}

func createGithubBlob(client *http.Client, token, baseURL string, data []byte) (string, error) {
	var blob githubSHAResponse
	_, err := githubJSONRequest(client, token, http.MethodPost, baseURL+"/git/blobs", map[string]interface{}{
		"content": base64.StdEncoding.EncodeToString(data),
		"encoding": "base64",
	}, &blob)
	if err != nil {
		return "", err
	}
	if blob.SHA == "" {
		return "", fmt.Errorf("GitHub tidak mengembalikan SHA blob")
	}
	return blob.SHA, nil
}

func githubJSONRequest(client *http.Client, token, method, endpoint string, payload, out interface{}) (int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		if detail.Message == "" {
			detail.Message = http.StatusText(resp.StatusCode)
		}
		return resp.StatusCode, fmt.Errorf("GitHub HTTP %d: %s", resp.StatusCode, detail.Message)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}


// ---------- error diagnosis ----------

var sweepState struct {
	sync.Mutex
	last time.Time
}

// sweepArchives enforces "only the newest successful ZIP" at most once a
// minute per instance; failures here never block the caller.
func sweepArchives() {
	sweepState.Lock()
	if time.Since(sweepState.last) < time.Minute { sweepState.Unlock(); return }
	sweepState.last = time.Now()
	sweepState.Unlock()
	if store, err := archive.New(); err == nil { _ = store.Sweep() }
}

func diagnosisStage(kind string, position int) string {
	names := [4]string{"Simpan & ekstrak ZIP", "Uji build Vercel", "Dorong ke GitHub", "Onlinekan di Vercel"}
	if kind == "self_update" { names[2], names[3] = "Perbarui GitHub", "Deployment production Vercel" }
	if position < 1 || position > 4 { return "Tidak diketahui" }
	return names[position-1]
}

// enrichDiagnosis adds the source to a stored diagnosis written by an older
// version, so the Pipeline never shows "sumber belum bisa dipastikan" merely
// because the record predates source classification. No network calls.
func enrichDiagnosis(raw string) json.RawMessage {
	var stored diagnose.Diagnosis
	if json.Unmarshal([]byte(raw), &stored) != nil { return json.RawMessage(raw) }
	diagnose.Reclassify(&stored)
	encoded, err := json.Marshal(stored)
	if err != nil { return json.RawMessage(raw) }
	return encoded
}

// rereadBuildLog retries reading the Vercel build log of a run whose log was
// still empty when it failed. Minutes later Vercel usually has it; the
// diagnosis is then rebuilt with the real error, its source and location,
// and the leftover test project is removed right away.
func rereadBuildLog(id string, row map[string]interface{}) (diagnose.Diagnosis, bool) {
	token := os.Getenv("VERCEL_TOKEN")
	raw := rowText(row, "ticket")
	if token == "" || raw == "" { return diagnose.Diagnosis{}, false }
	ticket, err := verifyBuildTicket(token, raw)
	if err != nil || ticket.DeploymentID == "" { return diagnose.Diagnosis{}, false }
	buildLog, err := getVercelBuildLogOnce(token, ticket.DeploymentID)
	if err != nil || buildLog == "" { return diagnose.Diagnosis{}, false }
	position := 2
	var stages []deploymentStage
	if json.Unmarshal([]byte(rowText(row, "stages")), &stages) == nil {
		for _, stage := range stages { if stage.Status == "Failed" { position = stage.Position } }
	}
	message := rowText(row, "message")
	if cut := strings.Index(message, ". Log belum dapat dibaca"); cut > 0 { message = message[:cut] }
	message += ": " + vercelFailureSummary(buildLog)
	fresh := recordDiagnosis(id, rowText(row, "kind"), rowText(row, "target"), position, message, buildLog)
	// Only a test-build ticket names a throwaway test project. A production
	// ticket names the app's real project, which must never be removed here
	// (a real app may itself be called e.g. "my-test-app").
	if ticket.Project != "" && (ticket.Stage == "app-test" || ticket.Stage == "self-test") {
		cleanupTestProject(token, ticket.Project)
		_, _ = d1.Query(`DELETE FROM vercel_orphan_projects WHERE project = ?`, ticket.Project)
	}
	return fresh, true
}

// recordDiagnosis stores where the run failed and how to fix it, including
// the offending code read from the uploaded ZIP while it still exists.
func recordDiagnosis(id, kind, target string, position int, message, buildLog string) diagnose.Diagnosis {
	var zipBytes []byte
	if store, err := archive.New(); err == nil {
		if record, err := store.Get(id); err == nil { zipBytes, _ = store.Download(record) }
	}
	result := diagnose.Analyze(diagnose.Input{Kind: kind, Target: target, Stage: diagnosisStage(kind, position),
		Message: message, BuildLog: buildLog, Zip: zipBytes})
	encoded, err := json.Marshal(result)
	if err != nil { return result }
	_, _ = d1.Query(`UPDATE deployment_jobs SET diagnosis = ? WHERE id = ?`, string(encoded), id)
	return result
}

// POST /api/diagnose -> {job_id} returns the stored diagnosis of a pipeline;
// {kind, target, stage, message, build_log} analyses an error shown directly
// in the browser (e.g. an upload rejected before the runner started).
func handleDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan POST")); return }
	var input struct {
		JobID    string `json:"job_id"`
		Kind     string `json:"kind"`
		Target   string `json:"target"`
		Stage    string `json:"stage"`
		Message  string `json:"message"`
		BuildLog string `json:"build_log"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan diagnosis tidak valid")); return
	}
	if input.JobID != "" {
		if len(input.JobID) != 32 || strings.Trim(input.JobID, "0123456789abcdef") != "" {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("ID pipeline tidak valid")); return
		}
		if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		rows, err := d1.Query(`SELECT j.kind, j.target, j.stages, j.diagnosis, COALESCE(r.message, '') AS message,
			COALESCE(r.ticket, '') AS ticket FROM deployment_jobs j LEFT JOIN deployment_runner r ON r.id = j.id WHERE j.id = ? LIMIT 1`, input.JobID)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(rows) == 0 { util.Error(w, http.StatusNotFound, fmt.Errorf("pipeline sudah tidak tersedia")); return }
		if raw := rowText(rows[0], "diagnosis"); raw != "" && json.Valid([]byte(raw)) {
			var stored diagnose.Diagnosis
			if json.Unmarshal([]byte(raw), &stored) == nil {
				diagnose.Reclassify(&stored)
				if stored.LogMissing {
					if fresh, ok := rereadBuildLog(input.JobID, rows[0]); ok { stored = fresh }
				}
				util.JSON(w, http.StatusOK, stored)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(raw))
			return
		}
		input.Kind, input.Target = rowText(rows[0], "kind"), rowText(rows[0], "target")
		if input.Message == "" { input.Message = rowText(rows[0], "message") }
		var stages []deploymentStage
		if json.Unmarshal([]byte(rowText(rows[0], "stages")), &stages) == nil {
			for _, stage := range stages { if stage.Status == "Failed" { input.Stage = stage.Stage } }
		}
	}
	if strings.TrimSpace(input.Message) == "" && strings.TrimSpace(input.BuildLog) == "" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("pesan error kosong")); return
	}
	util.JSON(w, http.StatusOK, diagnose.Analyze(diagnose.Input{Kind: input.Kind, Target: input.Target, Stage: input.Stage,
		Message: input.Message, BuildLog: input.BuildLog}))
}


// ---------- update history ----------

// recordHistory keeps one row per finished deployment with a file-level
// summary of what changed versus the last successful ZIP. It must run while
// both ZIPs still exist (before Finalize/Discard delete the loser).
func recordHistory(id, status, message string) {
	rows, err := d1.Query(`SELECT kind, target, lock_key, sync_note FROM deployment_jobs WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 { return }
	kind, target, repo := rowText(rows[0], "kind"), rowText(rows[0], "target"), strings.ToLower(rowText(rows[0], "lock_key"))
	// Successful runs carry the GitHub sync summary (files removed/kept).
	if message == "" { message = rowText(rows[0], "sync_note") }
	if repo == "" { return }
	var record archive.Record
	var newZip, oldZip []byte
	if store, err := archive.New(); err == nil {
		if found, err := store.Get(id); err == nil {
			record = found
			newZip, _ = store.Download(found)
			if baseline, err := store.Baseline(found.Scope, found.Target, id); err == nil && baseline != nil {
				oldZip, _ = store.Download(*baseline)
			}
		}
	}
	changes, err := json.Marshal(history.Diff(newZip, oldZip))
	if err != nil { changes = []byte("{}") }
	if len(message) > 500 { message = message[:500] }
	_, _ = d1.Query(`INSERT OR REPLACE INTO deployment_history (id, repo, kind, target, status, file_name, size_bytes, sha256, changes, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, repo, kind, target, status, record.Filename, record.SizeBytes, record.SHA256, string(changes), message)
	_, _ = d1.Query(`DELETE FROM deployment_history WHERE repo = ?1 AND id NOT IN
		(SELECT id FROM deployment_history WHERE repo = ?1 ORDER BY created_at DESC LIMIT 100)`, repo)
}

// GET /api/deploy-history?repo=owner/name
func handleDeployHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET")); return }
	repo := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("repo")))
	if parts := strings.Split(repo, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(repo) > 200 {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("format repo harus owner/nama")); return
	}
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	rows, err := d1.Query(`SELECT h.id, h.kind, h.target, h.status, h.file_name, h.size_bytes, h.sha256, h.changes, h.message, h.created_at,
		CASE WHEN a.status = 'current' THEN 1 ELSE 0 END AS is_current
		FROM deployment_history h LEFT JOIN zip_archives a ON a.id = h.id
		WHERE h.repo = ? ORDER BY h.created_at DESC LIMIT 100`, repo)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	entries := make([]map[string]interface{}, 0, len(rows))
	successes, updates := 0, 0
	for _, row := range rows {
		entry := map[string]interface{}{
			"id": rowText(row, "id"), "kind": rowText(row, "kind"), "target": rowText(row, "target"), "status": rowText(row, "status"),
			"file_name": rowText(row, "file_name"), "size_bytes": row["size_bytes"], "sha256": rowText(row, "sha256"),
			"message": rowText(row, "message"), "created_at": rowText(row, "created_at"), "is_current": fmt.Sprint(row["is_current"]) == "1",
		}
		if raw := rowText(row, "changes"); raw != "" && json.Valid([]byte(raw)) { entry["changes"] = json.RawMessage(raw) }
		if entry["status"] == "Success" {
			successes++
			if entry["kind"] != "new_app" { updates++ }
		}
		entries = append(entries, entry)
	}
	util.JSON(w, http.StatusOK, map[string]interface{}{"entries": entries, "success_count": successes, "update_count": updates, "total": len(entries)})
}


// DELETE /api/deployments?id=<job>: remove a failed/interrupted pipeline
// from the list. Its ZIP is discarded (the last successful ZIP stays
// current); the update history entry is kept.
func dismissDeployment(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" { util.Error(w, http.StatusBadRequest, fmt.Errorf("ID pipeline tidak valid")); return }
	rows, err := d1.Query(`SELECT status FROM deployment_jobs WHERE id = ? LIMIT 1`, id)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	if len(rows) == 0 { util.JSON(w, http.StatusOK, map[string]bool{"ok": true}); return }
	status := rowText(rows[0], "status")
	if status == "Running" { util.Error(w, http.StatusConflict, fmt.Errorf("proses masih berjalan; tunggu sampai selesai atau gagal")); return }
	if status != "Failed" && status != "Interrupted" { util.Error(w, http.StatusConflict, fmt.Errorf("hanya proses yang gagal yang bisa ditutup")); return }
	if store, err := archive.New(); err == nil { _ = store.Discard(id) }
	if _, err := d1.Query(`DELETE FROM deployment_runner WHERE id = ?`, id); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	if _, err := d1.Query(`DELETE FROM deployment_jobs WHERE id = ? AND status IN ('Failed', 'Interrupted')`, id); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	_, _ = d1.Query(`INSERT INTO admin_audit_log (action, target) VALUES ('dismiss_deployment', ?)`, id)
	util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

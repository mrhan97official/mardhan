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
	"devcontrol/pkg/autoconfig"
	"devcontrol/pkg/archive"
	"devcontrol/pkg/branding"
	"devcontrol/pkg/environmentstatus"
	"devcontrol/pkg/projectdelete"
	"devcontrol/pkg/projectthumbnail"
	"devcontrol/pkg/trafficmetrics"
	"devcontrol/pkg/zonemanagement"
	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/deploymentrunner"
	"devcontrol/pkg/diagnose"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
)

// Handler is the sole entrypoint for the whole API surface.
func Handler(w http.ResponseWriter, r *http.Request) {
	if util.HandleCORSPreflight(w, r) {
		return
	}
	// Fill in every setting derivable from the core variables before any
	// handler (including the Cloudflare runner) reads os.Getenv.
	autoconfig.Apply(r.Context())
	resource := r.URL.Query().Get("resource")
	if resource == "deployment-runner" {
		if r.Method != http.MethodPost || !deploymentrunner.Authorized(r.Header.Get("Authorization")) {
			util.Error(w, http.StatusUnauthorized, fmt.Errorf("runner tidak diizinkan")); return
		}
		handleDeploymentRunner(w, r)
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
	if resource == "session" { auth.HandleSession(w, r); return }
	if resource == "auto-setup" { autoconfig.Handle(w, r); return }
	if resource != "zip-archives" {
		if !auth.Configured() { util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("isi DEVCONTROL_ADMIN_PASSWORD (minimal 16 karakter) di Vercel untuk mengaktifkan panel admin; rahasia sesi dibuat otomatis")); return }
		if !auth.Allowed(r, resource) { util.Error(w, http.StatusUnauthorized, fmt.Errorf("login admin atau API key dengan hak baca diperlukan")); return }
	}
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
	case "project":
		projectdelete.Handle(w, r, vercelAppProjectName)
	case "project-thumbnails":
		projectthumbnail.Handle(w, r)
	case "app-promo":
		handleAppPromo(w, r)
	case "branding":
		branding.Handle(w, r)
	case "github-branches":
		handleGithubBranches(w, r)
	case "zip-archives":
		handleZipArchives(w, r)
	case "diagnose":
		handleDiagnose(w, r)
	case "databases":
		handleDatabases(w, r)
	case "api-management":
		apimanagement.Handle(w, r)
	default:
		util.Error(w, http.StatusNotFound, fmt.Errorf("unknown or missing ?resource="))
	}
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
	_, err := d1.Query(`UPDATE deployment_jobs SET status = 'Interrupted', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'Running' AND lease_until <= CURRENT_TIMESTAMP`)
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
		if raw := value("diagnosis"); raw != "" && json.Valid([]byte(raw)) { job.Diagnosis = json.RawMessage(raw) }
		jobs = append(jobs, job)
	}
	util.JSON(w, http.StatusOK, jobs)
}

func startDeploymentPipeline(id, kind, target, lockKey string, names [4]string) error {
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
	_, err = d1.Query(`INSERT INTO deployment_jobs (id, kind, target, lock_key, status, stages, lease_until)
		VALUES (?, ?, ?, ?, 'Running', ?, datetime('now', '+10 minutes'))`, id, kind, target, lockKey, string(encoded))
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
		if store, err := archive.New(); err == nil { _ = store.Finalize(id) }
	}
}

// A Go function stops when its HTTP request ends. Cloudflare's scheduled
// Worker starts fresh requests; R2 holds the ZIP and D1 holds the build ticket.
func prepareDeploymentRunner(r *http.Request) error {
	host := os.Getenv("VERCEL_PROJECT_PRODUCTION_URL")
	if host == "" { host = r.Host }
	if os.Getenv("VERCEL_ENV") == "preview" {
		return fmt.Errorf("jalankan deployment dari domain production agar runner tetap tersedia setelah update diri")
	}
	return deploymentrunner.Ensure(host)
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
	recordDiagnosis(job.id, job.kind, job.target, runnerStage(job), message, buildLog)
	if len(message) > 700 { message = message[:700] }
	status := "Failed"
	if uncertain { status = "Interrupted" }
	if !uncertain { updateDeploymentStage(job.id, runnerStage(job), "Failed") } else {
		_, _ = d1.Query(`UPDATE deployment_jobs SET status = 'Interrupted', updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = 'Running'`, job.id)
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
	rows, err := d1.Query(`
		SELECT id, name, status, uptime, version, repo, branch, app_url
		FROM services
		ORDER BY id ASC
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}
	util.JSON(w, http.StatusOK, rows)
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
	branchInput := strings.TrimSpace(r.FormValue("branch"))
	environment := orDefault(r.FormValue("environment"), "Production")
	isUpdate := deployType == "update_app"

	if name == "" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("nama aplikasi wajib diisi"))
		return
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
		if err := startDeploymentPipeline(archiveID, deployType, name, lockKey,
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
		id, previewURL, tempProject, testErr := createVercelTestDeployment(vercelToken, name, files)
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
		})
		if ticketErr != nil {
			cleanupTestProject(vercelToken, tempProject)
			util.Error(w, http.StatusInternalServerError, ticketErr)
			return
		}
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: true, Status: "pending", Ticket: ticket, ArchiveID: archiveID, Message: "Build uji Vercel dimulai; menunggu hasil build..."})
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
		if phase == "github" { testProjectToCleanup = ticket.Project }
	}
	if testProjectToCleanup != "" { defer cleanupTestProject(vercelToken, testProjectToCleanup) }

	if phase == "vercel-live" {
		updateDeploymentStage(archiveID, 4, "Running")
		// Use a stable, account-scoped Vercel project for every update.
		project := vercelAppProjectName(owner, repoName)
		id, deploymentURL, deployErr := createVercelDeployment(vercelToken, project, "production", files)
		if deployErr != nil {
			_ = store.Fail(archiveID)
			msg := "[Onlinekan di Vercel] " + deployErr.Error()
			updateDeploymentStage(archiveID, 4, "Failed")
			logLiveLog("ERROR", label+": "+msg)
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, Repo: repoFullName})
			return
		}
		ticket, ticketErr := signBuildTicket(vercelToken, buildTicket{
			Stage: "app-live", Mode: deployType, Name: name, DeploymentID: id,
			URL: deploymentURL, Repo: repoFullName, Branch: branch, Environment: environment,
			ArchiveID: archiveID, ZipSHA: zipDigest(zipBytes),
		})
		if ticketErr != nil {
			util.Error(w, http.StatusInternalServerError, ticketErr)
			return
		}
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: true, Status: "pending", Ticket: ticket, Repo: repoFullName, Message: "Deployment production dimulai; menunggu aplikasi online..."})
		return
	}

	updateDeploymentStage(archiveID, 3, "Running")
	if !isUpdate {
		if err := ensureGithubRepo(githubToken, repoName); err != nil {
			_ = store.Fail(archiveID)
			msg := "[GitHub] Gagal membuat repo: " + err.Error()
			logLiveLog("ERROR", label+": "+msg)
			updateDeploymentStage(archiveID, 3, "Failed")
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
			return
		}
	}

	if _, err := pushFilesToGitHub(githubToken, owner, repoName, branch, files); err != nil {
		_ = store.Fail(archiveID)
		msg := "[GitHub] Gagal mendorong file: " + err.Error()
		logLiveLog("ERROR", label+": "+msg)
		updateDeploymentStage(archiveID, 3, "Failed")
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, Repo: repoFullName})
		return
	}

	updateDeploymentStage(archiveID, 3, "Success")
	logLiveLog("INFO", fmt.Sprintf("%s: %q didorong ke %s@%s", label, name, repoFullName, branch))
	nextTicket, ticketErr := signBuildTicket(vercelToken, buildTicket{
		Stage: "app-live-start", Mode: deployType, Name: name, ZipSHA: zipDigest(zipBytes),
		Repo: repoFullName, Branch: branch, ArchiveID: archiveID,
	})
	if ticketErr != nil {
		util.Error(w, http.StatusInternalServerError, ticketErr)
		return
	}

	util.JSON(w, http.StatusOK, deployResult{
		Step: phase, OK: true,
		Message: fmt.Sprintf("Berhasil didorong ke %s@%s; menyiapkan Vercel.", repoFullName, branch),
		Repo:    repoFullName, Ticket: nextTicket, ArchiveID: archiveID,
	})
}

// Each status request performs one Vercel API call, keeping the Go function
// short even when the build stays queued for several minutes.
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
		util.JSON(w, http.StatusOK, deployResult{Step: "done", OK: true, Status: "ready", ArchiveID: record.ID,
			Message: "Aplikasi sudah online; ZIP aktif tersimpan", Repo: ticket.Repo, AppURL: ticket.URL})
		return
	}
	if record.Status != "pending" { util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip sesi deployment tidak lagi menunggu proses")); return }
	if err := requireActiveDeployment(ticket.ArchiveID); err != nil { util.Error(w, http.StatusConflict, err); return }
	state, liveURL, detail, checkErr := getVercelBuildStatus(token, ticket.DeploymentID)
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
				msg += ". Log belum dapat dibaca (" + logErr.Error() + "); periksa project uji " + ticket.Project + " di Vercel."
			}
		}
		updateDeploymentStage(ticket.ArchiveID, position, "Failed")
		logLiveLog("ERROR", msg)
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: msg, Repo: ticket.Repo, BuildLog: buildLog})
		return
	}
	if step == "vercel-test" {
		next, signErr := signBuildTicket(token, buildTicket{
			Stage: "app-push", Mode: mode, Name: name, ZipSHA: ticket.ZipSHA, Project: ticket.Project, ArchiveID: ticket.ArchiveID,
		})
		if signErr != nil { util.Error(w, http.StatusInternalServerError, signErr); return }
		updateDeploymentStage(ticket.ArchiveID, 2, "Success")
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: true, Status: "ready", Ticket: next, Message: "Build uji Vercel berhasil"})
		return
	}
	if liveURL == "" { liveURL = ticket.URL }
	if mode == "update_app" {
		_, err = d1.Query(`UPDATE services SET repo = ?, branch = ?, app_url = ?, status = 'Healthy' WHERE name = ?`, ticket.Repo, ticket.Branch, liveURL, name)
	} else {
		// A repeated status request must not insert a second service row.
		_, err = d1.Query(`INSERT INTO services (name, status, uptime, version, repo, branch, app_url)
			SELECT ?, 'Healthy', 100, 'v1.0.0', ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM services WHERE name = ?)`,
			name, ticket.Repo, ticket.Branch, liveURL, name)
	}
	if err != nil {
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: "[Simpan tautan aplikasi] URL gagal disimpan: " + err.Error(), Repo: ticket.Repo, AppURL: liveURL})
		return
	}
	if err := store.Promote(ticket.ArchiveID, "app", name); err != nil {
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: "Aplikasi online, tetapi gagal mencatat versi ZIP aktif: " + err.Error(), Repo: ticket.Repo, AppURL: liveURL})
		return
	}
	updateDeploymentStage(ticket.ArchiveID, 4, "Success")
	label, icon := "aplikasi baru", "box"
	if mode == "update_app" { label, icon = "update aplikasi", "check" }
	logActivity(fmt.Sprintf("Deployment %s berhasil", label), fmt.Sprintf("%s (%s@%s) → %s", name, ticket.Repo, ticket.Branch, ticket.Environment), icon)
	logLiveLog("INFO", fmt.Sprintf("%s: %q sudah online di %s", label, name, liveURL))
	util.JSON(w, http.StatusOK, deployResult{Step: "done", OK: true, Status: "ready", Message: "Aplikasi sudah online di Vercel", Repo: ticket.Repo, AppURL: liveURL})
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
	secret := os.Getenv("ZIP_ARCHIVE_ACCESS_TOKEN")
	if len(secret) < 16 {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("isi ZIP_ARCHIVE_ACCESS_TOKEN (minimal 16 karakter) di Vercel untuk membuka arsip")); return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
		util.Error(w, http.StatusUnauthorized, fmt.Errorf("kunci arsip salah atau belum diisi")); return
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
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, record.Filename))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" { offset, err = 0, nil }
	if err != nil || offset < 0 || offset > 100000 { util.Error(w, http.StatusBadRequest, fmt.Errorf("offset arsip tidak valid")); return }
	items, more, err := store.List(offset)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	util.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "has_more": more})
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
func ensureGithubRepo(token, repoName string) error {
	payload := map[string]interface{}{
		"name":      repoName,
		"private":   true,
		"auto_init": false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.github.com/user/repos", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		return nil
	}
	var out createRepoResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(out.Message), "already exists") {
		return nil
	}
	if out.Message != "" {
		return fmt.Errorf("%s", out.Message)
	}
	return fmt.Errorf("HTTP %d dari GitHub saat membuat repo", resp.StatusCode)
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
	if !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("sesi admin diperlukan")); return }
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
		if err := startDeploymentPipeline(archiveID, "self_update", target, strings.ToLower(repo),
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

	deploymentID, previewURL, tempProject, err := createVercelTestDeployment(vercelToken, repoName, files)
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
			msg += ". Log belum dapat dibaca (" + logErr.Error() + "); periksa project uji " + ticket.Project + " di Vercel."
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
	util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: true, Status: "ready", Ticket: next, ArchiveID: ticket.ArchiveID,
		Message: "Build uji Vercel berhasil; memperbarui GitHub...", PreviewURL: ticket.URL})
}

func finishSelfUpdate(w http.ResponseWriter, githubToken, vercelToken, owner, repoName, branch string, files []selfUpdateFile, previewURL, tempProject string, store *archive.Store, archiveID string) {
	defer cleanupTestProject(vercelToken, tempProject)

	logLiveLog("INFO", "Self-update: build Vercel lulus, memperbarui GitHub...")
	commitSHA, err := pushFilesToGitHub(githubToken, owner, repoName, branch, files)
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
	return out, nil
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

type vercelDeployFileInput struct {
	File     string `json:"file"`
	Data     string `json:"data"`
	Encoding string `json:"encoding"`
}

type vercelDeployRequest struct {
	Name            string                  `json:"name"`
	Files           []vercelDeployFileInput `json:"files"`
	Target          string                  `json:"target,omitempty"`
	ProjectSettings map[string]interface{}  `json:"projectSettings,omitempty"`
	// An empty target omits the field and creates a preview build for testing;
	// production deployments explicitly set target to "production".
}

// A new Vercel project needs an actual framework setting. An empty object
// does not count and returns missing_project_settings. For other source ZIPs,
// let Vercel detect the framework with its documented confirmation flag.
func vercelProjectSettings(files []selfUpdateFile) (map[string]interface{}, bool) {
	for _, file := range files {
		if file.path != "package.json" {
			continue
		}
		var pkg struct {
			Dependencies    map[string]json.RawMessage `json:"dependencies"`
			DevDependencies map[string]json.RawMessage `json:"devDependencies"`
		}
		if json.Unmarshal(file.data, &pkg) == nil {
			if _, found := pkg.Dependencies["next"]; found {
				return map[string]interface{}{"framework": "nextjs"}, false
			}
			if _, found := pkg.DevDependencies["next"]; found {
				return map[string]interface{}{"framework": "nextjs"}, false
			}
		}
		break
	}
	return nil, true
}

type vercelDeployResponse struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// createVercelTestDeployment uploads the extracted files inline (base64)
// and asks Vercel to build a preview deployment from them under a
// brand-new, uniquely-named *temporary* project — purely to validate the
// zip. It is never linked to GitHub and never reuses an existing/real
// Vercel project (VERCEL_PROJECT_ID is intentionally not consulted here),
// so a self-update test run can never show up in a real project's
// deployment history. The returned tempProject name is what the caller
// must delete afterwards via deleteVercelProject once the test is done.
func createVercelTestDeployment(token, repoName string, files []selfUpdateFile) (id string, previewURL string, tempProject string, err error) {
	tempProject = temporaryVercelProjectName(repoName)
	id, previewURL, err = createVercelDeployment(token, tempProject, "", files)
	if err != nil { return "", "", tempProject, err }
	return id, previewURL, tempProject, nil
}

// createVercelDeployment starts either an isolated test or a real production deployment.
func createVercelDeployment(token, project, target string, files []selfUpdateFile) (id string, deploymentURL string, err error) {
	reqFiles := make([]vercelDeployFileInput, 0, len(files))
	for _, f := range files {
		reqFiles = append(reqFiles, vercelDeployFileInput{
			File: f.path,
			Data: base64.StdEncoding.EncodeToString(f.data),
			Encoding: "base64",
		})
	}

	settings, autoDetect := vercelProjectSettings(files)
	payload := vercelDeployRequest{
		Name: project, Files: reqFiles, Target: target,
		ProjectSettings: settings,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}

	endpoint := "https://api.vercel.com/v13/deployments"
	query := url.Values{}
	if team := os.Getenv("VERCEL_TEAM_ID"); team != "" {
		query.Set("teamId", team)
	}
	if autoDetect {
		query.Set("skipAutoDetectionConfirmation", "1")
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var out vercelDeployResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if resp.StatusCode >= 300 {
		if out.Error != nil && out.Error.Message != "" {
			return "", "", fmt.Errorf("%s", out.Error.Message)
		}
		return "", "", fmt.Errorf("HTTP %d dari Vercel", resp.StatusCode)
	}
	if out.ID == "" || out.URL == "" { return "", "", fmt.Errorf("Vercel tidak mengembalikan ID atau URL deployment") }
	return out.ID, "https://" + out.URL, nil
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

// temporaryVercelProjectName builds a name that's unique per self-update run
// (repo name + a nanosecond-based suffix) so the test deployment always
// lands in its own disposable project instead of an existing/real one, no
// matter how many self-updates run back to back. Vercel project names are
// capped at 100 chars and limited to lowercase alphanumerics + hyphens;
// this stays comfortably under that.
func temporaryVercelProjectName(repoName string) string {
	base := sanitizeVercelName(repoName)
	suffix := fmt.Sprintf("selfupdate-test-%d", time.Now().UnixNano())
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

// getVercelBuildStatus performs exactly one bounded check. QUEUED and
// BUILDING remain pending instead of becoming a false build failure.
func getVercelBuildStatus(token, id string) (state, liveURL, detail string, err error) {
	endpoint := "https://api.vercel.com/v13/deployments/" + url.PathEscape(id)
	if team := os.Getenv("VERCEL_TEAM_ID"); team != "" { endpoint += "?teamId=" + url.QueryEscape(team) }
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil { return "", "", "", err }
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return "", "", "", err }
	defer resp.Body.Close()
	var out vercelStatusResponse
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil { return "", "", "", err }
	if resp.StatusCode >= 300 {
		if out.Error != nil && out.Error.Message != "" { return "", "", "", fmt.Errorf("%s", out.Error.Message) }
		return "", "", "", fmt.Errorf("HTTP %d dari Vercel", resp.StatusCode)
	}
	if out.ReadyState == "" { return "", "", "", fmt.Errorf("Vercel belum mengembalikan status deployment") }
	// A custom alias can fail while the deployment itself is READY. The
	// deployment URL remains usable and the build must still count as ready.
	if out.AliasAssigned && len(out.Alias) > 0 { liveURL = "https://" + out.Alias[0] } else if out.URL != "" { liveURL = "https://" + out.URL }
	if out.Error != nil { detail = out.Error.Message }
	return out.ReadyState, liveURL, detail, nil
}

// Vercel's deployment status often contains only ERROR with no error.message.
// Read the finished build's events before deleting the temporary project so
// the admin can see the compiler or dependency error that actually occurred.
func getVercelBuildLog(token, id string) (string, error) {
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
	ZipSHA string `json:"zip_sha,omitempty"`
	ArchiveID string `json:"archive_id,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Project string `json:"project,omitempty"`
	URL string `json:"url,omitempty"`
	Environment string `json:"environment,omitempty"`
	CreatedAt int64 `json:"created_at,omitempty"`
	Expires int64 `json:"expires"`
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

// pushFilesToGitHub publishes the whole ZIP as one atomic Git commit. The old
// Contents API loop made one commit per file, so one update could emit dozens
// of push events and make Vercel rate-limit the resulting deployments. A tree
// created without base_tree is also a full replacement: paths absent from the
// ZIP are deleted by the same commit instead of by more per-file commits.
func pushFilesToGitHub(token, owner, repo, branch string, files []selfUpdateFile) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("tidak ada file untuk didorong ke GitHub")
	}

	client := &http.Client{Timeout: 25 * time.Second}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	parentSHA, exists, err := githubBranchHead(client, token, baseURL, branch)
	if err != nil {
		return "", err
	}
	if !exists {
		// GitHub's Git Database API cannot create the first ref in an empty
		// repository. Bootstrap it with one Contents API commit, then perform
		// the normal atomic tree replacement below.
		if err := initializeGithubBranch(client, token, baseURL, branch, files[0]); err != nil {
			return "", fmt.Errorf("gagal menyiapkan branch %s: %w", branch, err)
		}
		if len(files) == 1 {
			sha, found, headErr := githubBranchHead(client, token, baseURL, branch)
			if headErr != nil { return "", headErr }
			if !found { return "", fmt.Errorf("GitHub belum membuat branch %s setelah inisialisasi", branch) }
			return sha, nil
		}
		parentSHA, exists, err = githubBranchHead(client, token, baseURL, branch)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("GitHub belum membuat branch %s setelah inisialisasi", branch)
		}
	}

	entries := make([]githubTreeEntry, 0, len(files))
	for _, file := range files {
		entry := githubTreeEntry{Path: file.path, Mode: "100644", Type: "blob"}
		if utf8.Valid(file.data) {
			entry.Content = new(string)
			*entry.Content = string(file.data)
		} else {
			blobSHA, err := createGithubBlob(client, token, baseURL, file.data)
			if err != nil {
				return "", fmt.Errorf("gagal mengunggah blob %s: %w", file.path, err)
			}
			entry.SHA = blobSHA
		}
		entries = append(entries, entry)
	}

	var tree githubSHAResponse
	if _, err := githubJSONRequest(client, token, http.MethodPost, baseURL+"/git/trees", map[string]interface{}{
		"tree": entries,
	}, &tree); err != nil {
		return "", fmt.Errorf("gagal membuat tree GitHub: %w", err)
	}
	if tree.SHA == "" {
		return "", fmt.Errorf("GitHub tidak mengembalikan SHA tree")
	}

	var commit githubSHAResponse
	if _, err := githubJSONRequest(client, token, http.MethodPost, baseURL+"/git/commits", map[string]interface{}{
		"message": "devcontrol: sinkronkan paket ZIP",
		"tree":    tree.SHA,
		"parents": []string{parentSHA},
	}, &commit); err != nil {
		return "", fmt.Errorf("gagal membuat commit GitHub: %w", err)
	}
	if commit.SHA == "" {
		return "", fmt.Errorf("GitHub tidak mengembalikan SHA commit")
	}

	refURL := baseURL + "/git/refs/heads/" + url.PathEscape(branch)
	if _, err := githubJSONRequest(client, token, http.MethodPatch, refURL, map[string]interface{}{
		"sha": commit.SHA, "force": false,
	}, &githubRefResponse{}); err != nil {
		return "", fmt.Errorf("gagal memperbarui branch %s: %w", branch, err)
	}
	return commit.SHA, nil
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

// recordDiagnosis stores where the run failed and how to fix it, including
// the offending code read from the uploaded ZIP while it still exists.
func recordDiagnosis(id, kind, target string, position int, message, buildLog string) {
	var zipBytes []byte
	if store, err := archive.New(); err == nil {
		if record, err := store.Get(id); err == nil { zipBytes, _ = store.Download(record) }
	}
	result := diagnose.Analyze(diagnose.Input{Kind: kind, Target: target, Stage: diagnosisStage(kind, position),
		Message: message, BuildLog: buildLog, Zip: zipBytes})
	encoded, err := json.Marshal(result)
	if err != nil { return }
	_, _ = d1.Query(`UPDATE deployment_jobs SET diagnosis = ? WHERE id = ?`, string(encoded), id)
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
		rows, err := d1.Query(`SELECT j.kind, j.target, j.stages, j.diagnosis, COALESCE(r.message, '') AS message
			FROM deployment_jobs j LEFT JOIN deployment_runner r ON r.id = j.id WHERE j.id = ? LIMIT 1`, input.JobID)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(rows) == 0 { util.Error(w, http.StatusNotFound, fmt.Errorf("pipeline sudah tidak tersedia")); return }
		if raw := rowText(rows[0], "diagnosis"); raw != "" && json.Valid([]byte(raw)) {
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

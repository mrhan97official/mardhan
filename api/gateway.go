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
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"devcontrol/pkg/apimanagement"
	"devcontrol/pkg/archive"
	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
)

// Handler is the sole entrypoint for the whole API surface.
func Handler(w http.ResponseWriter, r *http.Request) {
	if util.HandleCORSPreflight(w, r) {
		return
	}
	resource := r.URL.Query().Get("resource")
	if !auth.SameOrigin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("permintaan harus berasal dari aplikasi ini")); return }
	if resource == "session" { auth.HandleSession(w, r); return }
	if resource != "zip-archives" {
		if !auth.Configured() { util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("isi DEVCONTROL_ADMIN_PASSWORD dan DEVCONTROL_SESSION_SECRET di Vercel untuk mengaktifkan panel admin")); return }
		if !auth.Allowed(r, resource) { util.Error(w, http.StatusUnauthorized, fmt.Errorf("login admin atau API key dengan hak baca diperlukan")); return }
	}

	switch resource {
	case "overview":
		handleOverview(w, r)
	case "deployments":
		handleDeployments(w, r)
	case "environments":
		handleEnvironments(w, r)
	case "health":
		handleHealth(w, r)
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
	case "github-branches":
		handleGithubBranches(w, r)
	case "zip-archives":
		handleZipArchives(w, r)
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
		util.JSON(w, http.StatusOK, map[string]interface{}{})
		return
	}
	util.JSON(w, http.StatusOK, rows[0])
}

// GET /api/deployments -> deployment pipeline stages.
func handleDeployments(w http.ResponseWriter, r *http.Request) {
	rows, err := d1.Query(`
		SELECT id, stage, duration, status, position
		FROM deployment_pipeline
		WHERE position BETWEEN 1 AND 4
		  AND id = (SELECT MIN(id) FROM deployment_pipeline AS other WHERE other.position = deployment_pipeline.position)
		ORDER BY position ASC
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}
	if len(rows) < 4 {
		if err := ensureDeploymentPipeline(); err != nil {
			util.Error(w, http.StatusInternalServerError, fmt.Errorf("gagal menyiapkan pipeline deployment: %w", err))
			return
		}
		rows, err = d1.Query(`
			SELECT id, stage, duration, status, position
			FROM deployment_pipeline
			WHERE position BETWEEN 1 AND 4
			  AND id = (SELECT MIN(id) FROM deployment_pipeline AS other WHERE other.position = deployment_pipeline.position)
			ORDER BY position ASC
		`)
		if err != nil {
			util.Error(w, http.StatusInternalServerError, err)
			return
		}
	}
	util.JSON(w, http.StatusOK, rows)
}

// Schema creation does not seed demo data. A deployment must have real rows
// before UPDATE can report progress. INSERT ... WHERE NOT EXISTS also repairs
// partially initialized databases without overwriting an in-flight stage.
func ensureDeploymentPipeline() error {
	defaults := []string{"Ekstrak ZIP", "Uji Vercel", "GitHub", "Online Vercel"}
	for index, stage := range defaults {
		position := index + 1
		if _, err := d1.Query(`INSERT INTO deployment_pipeline (stage, duration, status, position)
			SELECT ?, '-', 'Pending', ?
			WHERE NOT EXISTS (SELECT 1 FROM deployment_pipeline WHERE position = ?)`, stage, position, position); err != nil {
			return err
		}
	}
	return nil
}

func startDeploymentPipeline(stages [4]string) error {
	if err := ensureDeploymentPipeline(); err != nil {
		return err
	}
	for index, stage := range stages {
		status := "Pending"
		if index == 0 {
			status = "Running"
		}
		if _, err := d1.Query(`UPDATE deployment_pipeline SET stage = ?, status = ?, duration = '-' WHERE position = ?`, stage, status, index+1); err != nil {
			return err
		}
	}
	return nil
}

// GET /api/environments -> environment status table.
func handleEnvironments(w http.ResponseWriter, r *http.Request) {
	rows, err := d1.Query(`
		SELECT id, name, region, version, status
		FROM environments
		ORDER BY id ASC
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}
	util.JSON(w, http.StatusOK, rows)
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
	Current float64   `json:"current"`
}

// GET /api/health -> infra_metrics grouped into a sparkline series per metric.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	rows, err := d1.Query(`
		SELECT metric, value, recorded_at
		FROM infra_metrics
		ORDER BY recorded_at ASC
	`)
	if err != nil {
		util.Error(w, http.StatusInternalServerError, err)
		return
	}

	grouped := map[string][]float64{}
	order := []string{"cpu", "memory", "network", "requests"}

	for _, row := range rows {
		metric, _ := row["metric"].(string)
		grouped[metric] = append(grouped[metric], toFloat(row["value"]))
	}

	series := make([]infraSeries, 0, len(order))
	for _, m := range order {
		values := grouped[m]
		if len(values) == 0 {
			continue
		}
		if len(values) > 20 {
			values = values[len(values)-20:]
		}
		series = append(series, infraSeries{Metric: m, Values: values, Current: values[len(values)-1]})
	}

	sort.SliceStable(series, func(i, j int) bool {
		return indexOf(order, series[i].Metric) < indexOf(order, series[j].Metric)
	})

	util.JSON(w, http.StatusOK, series)
}

func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return len(list)
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
}

func deployStagePosition(phase string) int {
	switch phase {
	case "extract": return 1
	case "vercel-test": return 2
	case "github": return 3
	default: return 4
	}
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
		(phase != "extract" && phase != "vercel-test" && phase != "vercel-test-status" && phase != "github" && phase != "vercel-live" && phase != "vercel-live-status") {
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
		if err := startDeploymentPipeline([4]string{"Ekstrak ZIP", "Uji Vercel", "GitHub", "Online Vercel"}); err != nil {
			util.Error(w, http.StatusInternalServerError, fmt.Errorf("pipeline deployment belum siap: %w", err))
			return
		}
	}
	store, err := archive.New()
	if err != nil {
		if phase == "extract" { _, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 1`) }
		util.Error(w, http.StatusPreconditionFailed, err); return
	}
	archiveID := strings.TrimSpace(r.FormValue("archive_id"))
	if phase == "extract" {
		record, saveErr := store.Save("app", name, header.Filename, "upload", "pending", zipBytes)
		if saveErr != nil {
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 1`)
			util.Error(w, http.StatusBadGateway, fmt.Errorf("ZIP tidak dapat disimpan, update dibatalkan: %w", saveErr)); return
		}
		archiveID = record.ID
		if isUpdate {
			if backupErr := ensureAppBaseline(store, githubToken, name); backupErr != nil {
				_ = store.Fail(archiveID)
				_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 1`)
				util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, ArchiveID: archiveID,
					Message: "ZIP baru tersimpan, tetapi versi sebelumnya belum dapat diarsipkan; update dibatalkan: " + backupErr.Error()})
				return
			}
		}
	} else if err := store.Verify(archiveID, "app", name, zipBytes); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip ZIP wajib tersimpan sebelum deployment: %w", err)); return
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
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = ?`, deployStagePosition(phase))
		logLiveLog("ERROR", label+": "+msg)
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	if len(files) == 0 {
		_ = store.Fail(archiveID)
		msg := "[Ekstrak ZIP] Zip kosong atau tidak berisi file yang valid"
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = ?`, deployStagePosition(phase))
		logLiveLog("ERROR", label+": "+msg)
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	logLiveLog("INFO", fmt.Sprintf("%s: mengekstrak %d file dari zip untuk %q", label, len(files), name))
	if phase == "extract" {
		// A separate request lets the modal show each stage while it runs.
		_, _ = d1.Query(`UPDATE deployment_pipeline SET stage = 'Ekstrak ZIP', status = 'Success', duration = 'Selesai' WHERE position = 1`)
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: true, Message: fmt.Sprintf("ZIP disimpan; %d file berhasil diekstrak", len(files)), ArchiveID: archiveID})
		return
	}
	if phase == "vercel-test" {
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Running' WHERE position = 2`)
		id, previewURL, tempProject, testErr := createVercelTestDeployment(vercelToken, name, files)
		if testErr != nil {
			_ = store.Fail(archiveID)
			cleanupTestProject(vercelToken, tempProject)
			msg := "[Uji Build Vercel] " + testErr.Error()
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 2`)
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
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = ?`, deployStagePosition(phase))
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
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = ?`, deployStagePosition(phase))
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
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Running' WHERE position = 4`)
		// Use a stable, account-scoped Vercel project for every update.
		project := vercelAppProjectName(owner, repoName)
		id, deploymentURL, deployErr := createVercelDeployment(vercelToken, project, "production", files)
		if deployErr != nil {
			_ = store.Fail(archiveID)
			msg := "[Onlinekan di Vercel] " + deployErr.Error()
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 4`)
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

	_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Running' WHERE position = 3`)
	if !isUpdate {
		if err := ensureGithubRepo(githubToken, repoName); err != nil {
			_ = store.Fail(archiveID)
			msg := "[GitHub] Gagal membuat repo: " + err.Error()
			logLiveLog("ERROR", label+": "+msg)
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 3`)
			util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg})
			return
		}
	}

	if err := pushFilesToGitHub(githubToken, owner, repoName, branch, files); err != nil {
		_ = store.Fail(archiveID)
		msg := "[GitHub] Gagal mendorong file: " + err.Error()
		logLiveLog("ERROR", label+": "+msg)
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 3`)
		util.JSON(w, http.StatusOK, deployResult{Step: phase, OK: false, Message: msg, Repo: repoFullName})
		return
	}

	// Sync: anything already on the branch that isn't part of this upload
	// gets removed too, same as self-update, so stale files never linger.
	if existingPaths, treeErr := listGithubRepoFiles(githubToken, owner, repoName, branch); treeErr == nil {
		wanted := make(map[string]bool, len(files))
		for _, f := range files {
			wanted[f.path] = true
		}
		for _, p := range existingPaths {
			if wanted[p] {
				continue
			}
			if delErr := deleteFileFromGitHub(githubToken, owner, repoName, branch, p); delErr != nil {
				logLiveLog("WARN", fmt.Sprintf("%s: gagal menghapus file lama %s: %s", label, p, delErr.Error()))
			}
		}
	}

	_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Success', duration = 'Selesai' WHERE position = 3`)
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
		if step == "vercel-test" { cleanupTestProject(token, ticket.Project) }
		msg := fmt.Sprintf("[%s] Build Vercel %s", map[string]string{"vercel-test": "Uji Build Vercel", "vercel-live": "Onlinekan di Vercel"}[step], state)
		if detail != "" { msg += ": " + detail }
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = ?`, position)
		logLiveLog("ERROR", msg)
		util.JSON(w, http.StatusOK, deployResult{Step: step, OK: false, Message: msg, Repo: ticket.Repo})
		return
	}
	if step == "vercel-test" {
		next, signErr := signBuildTicket(token, buildTicket{
			Stage: "app-push", Mode: mode, Name: name, ZipSHA: ticket.ZipSHA, Project: ticket.Project, ArchiveID: ticket.ArchiveID,
		})
		if signErr != nil { util.Error(w, http.StatusInternalServerError, signErr); return }
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Success', duration = 'Selesai' WHERE position = 2`)
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
	_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Success', duration = 'Selesai' WHERE position = 4`)
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
// after this will just add/update files in whatever's already there.
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
	Step       string `json:"step"` // "extract" | "vercel-test" | "github-push" | "done"
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
//  4. The browser checks build status with short requests. A failed test is
//     cleaned up immediately; a passing test is cleaned up after GitHub sync
//     so a lost READY response can be polled again.
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
	if phase != "start" && phase != "status" && phase != "commit" {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("tahap update tidak valid"))
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
		if err := startDeploymentPipeline([4]string{"Ekstrak ZIP", "Uji Vercel", "Perbarui GitHub", "Hasil Update Diri"}); err != nil {
			util.Error(w, http.StatusInternalServerError, fmt.Errorf("pipeline update diri belum siap: %w", err))
			return
		}
	}
	store, err := archive.New()
	if err != nil {
		if phase == "start" { _, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 1`) }
		util.Error(w, http.StatusPreconditionFailed, err); return
	}
	target := repo + "@" + branch
	archiveID := strings.TrimSpace(r.FormValue("archive_id"))
	if phase == "start" {
		record, saveErr := store.Save("self", target, header.Filename, "upload", "pending", zipBytes)
		if saveErr != nil {
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 1`)
			util.Error(w, http.StatusBadGateway, fmt.Errorf("ZIP tidak dapat disimpan, update dibatalkan: %w", saveErr)); return
		}
		archiveID = record.ID
		if backupErr := ensureGithubBaseline(store, githubToken, "self", target, repo, branch); backupErr != nil {
			_ = store.Fail(archiveID)
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 1`)
			util.JSON(w, http.StatusOK, selfUpdateResult{Step: "extract", OK: false, ArchiveID: archiveID,
				Message: "ZIP baru tersimpan, tetapi versi sebelumnya belum dapat diarsipkan; update dibatalkan: " + backupErr.Error()})
			return
		}
	} else if phase == "commit" {
		record, checkErr := store.Get(archiveID)
		if checkErr != nil || record.Scope != "self" || record.Target != target || record.SizeBytes != int64(len(zipBytes)) || record.SHA256 != archive.Digest(zipBytes) ||
			(record.Status != "pending" && record.Status != "current") {
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 3`)
			util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip ZIP sesi update diri tidak cocok")); return
		}
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Running' WHERE position = 3`)
	} else if err := store.Verify(archiveID, "self", target, zipBytes); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("arsip ZIP wajib tersimpan sebelum update diri: %w", err)); return
	}

	// From here on the request itself is valid, so failures are reported as
	// part of the step pipeline (HTTP 200 + selfUpdateResult) instead of a
	// bare HTTP error, so the modal can show exactly which step failed.
	files, err := extractZip(zipBytes)
	if err != nil {
		_ = store.Fail(archiveID)
		position := 1
		if phase == "commit" { position = 3 }
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = ?`, position)
		msg := "[Ekstrak ZIP] Gagal mengekstrak file zip: " + err.Error()
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "extract", OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	if len(files) == 0 {
		_ = store.Fail(archiveID)
		position := 1
		if phase == "commit" { position = 3 }
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = ?`, position)
		msg := "[Ekstrak ZIP] Zip kosong atau tidak berisi file yang valid"
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "extract", OK: false, Message: msg, ArchiveID: archiveID})
		return
	}
	if phase == "commit" {
		ticket, ticketErr := verifyBuildTicket(vercelToken, r.FormValue("ticket"))
		if ticketErr != nil || ticket.Stage != "self-push" || ticket.Repo != repo || ticket.Branch != branch || ticket.Project == "" || ticket.ZipSHA != zipDigest(zipBytes) || ticket.ArchiveID != archiveID {
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 3`)
			util.Error(w, http.StatusBadRequest, fmt.Errorf("sesi uji build tidak valid; unggah ZIP yang sama dan mulai ulang"))
			return
		}
		record, checkErr := store.Get(archiveID)
		if checkErr != nil {
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 3`)
			util.Error(w, http.StatusBadGateway, checkErr); return
		}
		if record.Status == "current" {
			_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Success', duration = 'Selesai' WHERE position IN (3, 4)`)
			util.JSON(w, http.StatusOK, selfUpdateResult{Step: "done", OK: true, ArchiveID: archiveID,
				Message: "ZIP sudah tersimpan dan repo GitHub telah diperbarui"})
			return
		}
		finishSelfUpdate(w, githubToken, vercelToken, owner, repoName, branch, files, ticket.URL, ticket.Project, store, archiveID)
		return
	}
	_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Success', duration = 'Selesai' WHERE position = 1`)
	_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Running' WHERE position = 2`)
	logLiveLog("INFO", fmt.Sprintf("Self-update: mengekstrak %d file dari zip untuk %s/%s", len(files), owner, repoName))

	deploymentID, previewURL, tempProject, err := createVercelTestDeployment(vercelToken, repoName, files)
	if err != nil {
		_ = store.Fail(archiveID)
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 2`)
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
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 2`)
		util.Error(w, http.StatusInternalServerError, ticketErr)
		return
	}
	logLiveLog("INFO", fmt.Sprintf("Self-update: build uji dimulai (project %q, deployment %s)", tempProject, deploymentID))
	util.JSON(w, http.StatusOK, selfUpdateResult{
		Step: "vercel-test", OK: true, Status: "pending", Ticket: ticket, PreviewURL: previewURL, ArchiveID: archiveID,
		Message: "ZIP disimpan; build uji Vercel dimulai; menunggu hasil build...",
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
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 2`)
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
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 2`)
		util.Error(w, http.StatusInternalServerError, signErr); return
	}
	_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Success', duration = 'Selesai' WHERE position = 2`)
	util.JSON(w, http.StatusOK, selfUpdateResult{Step: "vercel-test", OK: true, Status: "ready", Ticket: next, ArchiveID: ticket.ArchiveID,
		Message: "Build uji Vercel berhasil; memperbarui GitHub...", PreviewURL: ticket.URL})
}

func finishSelfUpdate(w http.ResponseWriter, githubToken, vercelToken, owner, repoName, branch string, files []selfUpdateFile, previewURL, tempProject string, store *archive.Store, archiveID string) {
	defer cleanupTestProject(vercelToken, tempProject)

	logLiveLog("INFO", "Self-update: build Vercel lulus, memperbarui GitHub...")
	if err := pushFilesToGitHub(githubToken, owner, repoName, branch, files); err != nil {
		_ = store.Fail(archiveID)
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 3`)
		msg := "[Perbarui GitHub] Build lulus tapi gagal memperbarui GitHub: " + err.Error()
		logLiveLog("ERROR", "Self-update: "+msg)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "github-push", OK: false, Message: msg, PreviewURL: previewURL, ArchiveID: archiveID})
		return
	}

	// pushFilesToGitHub above only PUTs what's in the zip — it never deletes.
	// So a file removed or merged away in the new package (like the old
	// api/deploy.go and api/selfupdate.go being folded into api/gateway.go)
	// would otherwise stay behind in the repo forever, still there next to
	// its replacement and breaking the build (duplicate declarations). Sync
	// it properly: anything that exists in the repo at this branch but isn't
	// part of the uploaded zip gets removed too.
	existingPaths, treeErr := listGithubRepoFiles(githubToken, owner, repoName, branch)
	if treeErr != nil {
		logLiveLog("WARN", "Self-update: tidak bisa memeriksa file lama di repo untuk dibersihkan: "+treeErr.Error())
	} else {
		wanted := make(map[string]bool, len(files))
		for _, f := range files {
			wanted[f.path] = true
		}
		for _, p := range existingPaths {
			if wanted[p] {
				continue
			}
			if err := deleteFileFromGitHub(githubToken, owner, repoName, branch, p); err != nil {
				logLiveLog("WARN", fmt.Sprintf("Self-update: gagal menghapus file lama %s: %s", p, err.Error()))
				continue
			}
			logLiveLog("INFO", "Self-update: menghapus file lama yang sudah tidak dipakai: "+p)
		}
	}

	if err := store.Promote(archiveID, "self", owner+"/"+repoName+"@"+branch); err != nil {
		_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Failed' WHERE position = 3`)
		util.JSON(w, http.StatusOK, selfUpdateResult{Step: "github-push", OK: false, Message: "GitHub diperbarui, tetapi arsip ZIP aktif gagal dicatat: " + err.Error(), ArchiveID: archiveID})
		return
	}
	_, _ = d1.Query(`UPDATE deployment_pipeline SET status = 'Success', duration = 'Selesai' WHERE position IN (3, 4)`)
	logActivity("Self-update berhasil", fmt.Sprintf("%s/%s diperbarui dari zip yang diunggah", owner, repoName), "check")
	logLiveLog("INFO", fmt.Sprintf("Self-update selesai: %s/%s@%s diperbarui", owner, repoName, branch))

	util.JSON(w, http.StatusOK, selfUpdateResult{
		Step: "done", OK: true, ArchiveID: archiveID,
		Message:    fmt.Sprintf("Berhasil diuji di Vercel dan didorong ke %s/%s@%s", owner, repoName, branch),
		PreviewURL: previewURL,
	})
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
	ZipSHA string `json:"zip_sha,omitempty"`
	ArchiveID string `json:"archive_id,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Project string `json:"project,omitempty"`
	URL string `json:"url,omitempty"`
	Environment string `json:"environment,omitempty"`
	Expires int64 `json:"expires"`
}

func zipDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func signBuildTicket(secret string, ticket buildTicket) (string, error) {
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

type githubContentResponse struct {
	SHA string `json:"sha"`
}

// pushFilesToGitHub writes each extracted file to the target repo/branch
// via the Contents API — creating it if it's new, updating it (using its
// current sha) if it already exists. This only ever adds/updates; it never
// deletes. handleSelfUpdate pairs it with listGithubRepoFiles +
// deleteFileFromGitHub right after to remove anything the new zip dropped,
// so the two together behave as a full sync rather than a purely additive
// push.
func pushFilesToGitHub(token, owner, repo, branch string, files []selfUpdateFile) error {
	client := &http.Client{Timeout: 15 * time.Second}

	for _, f := range files {
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, f.path)

		var existingSHA string
		getReq, err := http.NewRequest(http.MethodGet, apiURL+"?ref="+branch, nil)
		if err == nil {
			getReq.Header.Set("Authorization", "Bearer "+token)
			getReq.Header.Set("Accept", "application/vnd.github+json")
			if getResp, getErr := client.Do(getReq); getErr == nil {
				if getResp.StatusCode == http.StatusOK {
					var existing githubContentResponse
					_ = json.NewDecoder(getResp.Body).Decode(&existing)
					existingSHA = existing.SHA
				}
				getResp.Body.Close()
			}
		}

		payload := map[string]interface{}{
			"message": "self-update: perbarui " + f.path,
			"content": base64.StdEncoding.EncodeToString(f.data),
			"branch":  branch,
		}
		if existingSHA != "" {
			payload["sha"] = existingSHA
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}

		putReq, err := http.NewRequest(http.MethodPut, apiURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		putReq.Header.Set("Authorization", "Bearer "+token)
		putReq.Header.Set("Accept", "application/vnd.github+json")
		putReq.Header.Set("Content-Type", "application/json")

		putResp, err := client.Do(putReq)
		if err != nil {
			return err
		}
		status := putResp.StatusCode
		putResp.Body.Close()
		if status >= 300 {
			return fmt.Errorf("gagal memperbarui %s (HTTP %d)", f.path, status)
		}
	}
	return nil
}

type githubTreeResponse struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"` // "blob" | "tree"
	} `json:"tree"`
	Truncated bool `json:"truncated"`
}

// listGithubRepoFiles returns every regular file path currently on
// branch's tip, via the Git Trees API (recursive) — used to work out which
// files the new zip no longer has, so they can be deleted from the repo too.
func listGithubRepoFiles(token, owner, repo, branch string) ([]string, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", owner, repo, branch)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// New/empty branch — nothing committed yet, nothing to clean up.
		return nil, nil
	}

	var out githubTreeResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d dari GitHub saat membaca isi repo", resp.StatusCode)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	if out.Truncated {
		return nil, fmt.Errorf("daftar file repo terlalu besar untuk dibaca sekaligus (truncated)")
	}

	paths := make([]string, 0, len(out.Tree))
	for _, entry := range out.Tree {
		if entry.Type == "blob" {
			paths = append(paths, entry.Path)
		}
	}
	return paths, nil
}

// deleteFileFromGitHub removes one file via the Contents API. Deleting
// (like updating) requires the file's current sha, fetched the same way
// pushFilesToGitHub does before a PUT.
func deleteFileFromGitHub(token, owner, repo, branch, filePath string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, filePath)
	client := &http.Client{Timeout: 15 * time.Second}

	getReq, err := http.NewRequest(http.MethodGet, apiURL+"?ref="+branch, nil)
	if err != nil {
		return err
	}
	getReq.Header.Set("Authorization", "Bearer "+token)
	getReq.Header.Set("Accept", "application/vnd.github+json")

	getResp, err := client.Do(getReq)
	if err != nil {
		return err
	}
	var existing githubContentResponse
	decodeErr := json.NewDecoder(getResp.Body).Decode(&existing)
	getStatus := getResp.StatusCode
	getResp.Body.Close()
	if getStatus != http.StatusOK || decodeErr != nil || existing.SHA == "" {
		return fmt.Errorf("tidak bisa membaca sha saat ini (HTTP %d)", getStatus)
	}

	payload := map[string]interface{}{
		"message": "self-update: hapus " + filePath + " (tidak ada lagi di paket update)",
		"sha":     existing.SHA,
		"branch":  branch,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	delReq, err := http.NewRequest(http.MethodDelete, apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	delReq.Header.Set("Authorization", "Bearer "+token)
	delReq.Header.Set("Accept", "application/vnd.github+json")
	delReq.Header.Set("Content-Type", "application/json")

	delResp, err := client.Do(delReq)
	if err != nil {
		return err
	}
	status := delResp.StatusCode
	delResp.Body.Close()
	if status >= 300 {
		return fmt.Errorf("HTTP %d", status)
	}
	return nil
}

// Package environmentstatus reports actual Vercel deployment states for apps
// managed by this installation. Seeded D1 environment rows are never used.
package environmentstatus

import (
	"context"
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

	"devcontrol/pkg/d1"
	"devcontrol/pkg/util"
)

type deployment struct {
	UID string `json:"uid"`
	Target string `json:"target"`
	ReadyState string `json:"readyState"`
	State string `json:"state"`
	URL string `json:"url"`
	Meta struct { Commit string `json:"githubCommitSha"` } `json:"meta"`
	CustomEnvironment struct { Slug string `json:"slug"` } `json:"customEnvironment"`
}

type environment struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Project string `json:"project"`
	Version string `json:"version"`
	Status string `json:"status"`
	URL string `json:"url,omitempty"`
}

type project struct { ID, Label string }

func deploymentEnvironment(item deployment) string {
	if item.CustomEnvironment.Slug != "" { return item.CustomEnvironment.Slug }
	switch strings.ToLower(item.Target) {
	case "production": return "Production"
	case "preview", "": return "Preview"
	default: return item.Target
	}
}

func deploymentStatus(item deployment) string {
	state := item.ReadyState
	if state == "" { state = item.State }
	switch strings.ToUpper(state) {
	case "READY": return "Ready"
	case "BUILDING", "INITIALIZING", "QUEUED": return "Building"
	case "ERROR": return "Failed"
	case "CANCELED": return "Canceled"
	case "BLOCKED": return "Blocked"
	default: return "Unknown"
	}
}

func latestByEnvironment(items []deployment, projectName string) []environment {
	seen := make(map[string]bool)
	out := make([]environment, 0, len(items))
	for _, item := range items {
		name := deploymentEnvironment(item)
		if seen[strings.ToLower(name)] { continue }
		seen[strings.ToLower(name)] = true // Vercel returns newest deployments first.
		version := item.UID
		if item.Meta.Commit != "" { version = item.Meta.Commit }
		if len(version) > 12 { version = version[:12] }
		if version == "" { version = "—" }
		row := environment{ID: projectName + ":" + name, Name: name, Project: projectName,
			Version: version, Status: deploymentStatus(item)}
		if host := strings.ToLower(item.URL); strings.HasSuffix(host, ".vercel.app") &&
			!strings.ContainsAny(host, "/?#@ :\\") { row.URL = "https://" + host }
		out = append(out, row)
	}
	return out
}

func fetchDeployments(ctx context.Context, client *http.Client, token, projectID, target string) ([]deployment, error) {
	query := url.Values{"projectId": {projectID}, "limit": {"100"}}
	if team := os.Getenv("VERCEL_TEAM_ID"); team != "" { query.Set("teamId", team) }
	if target != "" { query.Set("target", target); query.Set("limit", "1") }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.vercel.com/v7/deployments?"+query.Encode(), nil)
	if err != nil { return nil, err }
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound { return nil, fmt.Errorf("project Vercel tidak terlihat oleh token (HTTP 404)") }
	if resp.StatusCode != http.StatusOK { return nil, fmt.Errorf("Vercel mengembalikan HTTP %d", resp.StatusCode) }
	var result struct { Deployments []deployment `json:"deployments"` }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil { return nil, err }
	if result.Deployments == nil { return nil, fmt.Errorf("daftar deployment Vercel tidak valid") }
	return result.Deployments, nil
}

func projectDeployments(ctx context.Context, client *http.Client, token string, p project) ([]environment, error) {
	items, err := fetchDeployments(ctx, client, token, p.ID, "")
	if err != nil { return nil, fmt.Errorf("%s: %w", p.Label, err) }
	foundProduction := false
	for _, item := range items {
		if strings.EqualFold(deploymentEnvironment(item), "Production") { foundProduction = true; break }
	}
	// Many preview builds can push an older active production deployment off
	// the first page. Ask Vercel for that target explicitly in that case.
	if !foundProduction {
		production, err := fetchDeployments(ctx, client, token, p.ID, "production")
		if err != nil { return nil, fmt.Errorf("%s: %w", p.Label, err) }
		items = append(items, production...)
	}
	rows := latestByEnvironment(items, p.Label)
	return rows, nil
}

// Handle uses the Vercel token already configured for deployment. A failed
// provider request returns an error instead of displaying old demo statuses.
func Handle(w http.ResponseWriter, r *http.Request, stableProjectName func(string, string) string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET")); return }
	token := os.Getenv("VERCEL_TOKEN")
	if token == "" { util.Error(w, http.StatusServiceUnavailable, fmt.Errorf("isi VERCEL_TOKEN untuk melihat status deployment yang sebenarnya")); return }
	rows, err := d1.Query(`SELECT name, repo FROM services WHERE repo IS NOT NULL AND trim(repo) != '' ORDER BY id DESC`)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	projects := make([]project, 0, len(rows)+1)
	seen := make(map[string]bool)
	for _, row := range rows {
		repo, _ := row["repo"].(string)
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" { continue }
		id := stableProjectName(parts[0], parts[1])
		if seen[id] { continue }
		seen[id] = true
		label, _ := row["name"].(string)
		if label == "" { label = parts[1] }
		projects = append(projects, project{ID: id, Label: label})
	}
	if self := os.Getenv("VERCEL_PROJECT_ID"); self != "" && !seen[self] {
		projects = append(projects, project{ID: self, Label: "DevControl"})
	}
	if len(projects) == 0 { util.JSON(w, http.StatusOK, []environment{}); return }

	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 12 * time.Second}
	results := make([][]environment, len(projects))
	errors := make([]error, len(projects))
	semaphore := make(chan struct{}, 4)
	var jobs sync.WaitGroup
	for i, p := range projects {
		jobs.Add(1)
		go func(i int, p project) {
			defer jobs.Done()
			select {
			case semaphore <- struct{}{}: defer func() { <-semaphore }()
			case <-ctx.Done(): errors[i] = ctx.Err(); return
			}
			results[i], errors[i] = projectDeployments(ctx, client, token, p)
		}(i, p)
	}
	jobs.Wait()
	for _, err := range errors {
		if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("status deployment belum dapat diverifikasi: %w", err)); return }
	}
	result := make([]environment, 0)
	for _, item := range results { result = append(result, item...) }
	rank := func(row environment) int {
		if row.Status == "Failed" || row.Status == "Blocked" { return 0 }
		switch strings.ToLower(row.Name) {
		case "production": return 1
		case "staging": return 2
		default: return 3
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if rank(result[i]) != rank(result[j]) { return rank(result[i]) < rank(result[j]) }
		if result[i].Project == result[j].Project { return result[i].Name < result[j].Name }
		return result[i].Project < result[j].Project
	})
	util.JSON(w, http.StatusOK, result)
}

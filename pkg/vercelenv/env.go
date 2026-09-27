// Package vercelenv lets the admin view and edit a Vercel project's
// environment variables from the Environments page. Values are never sent in
// the list; each one is fetched on explicit request ("reveal"). Every change
// is written to admin_audit_log without its value.
package vercelenv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"devcontrol/pkg/d1"
	"devcontrol/pkg/util"
)

var (
	client     = &http.Client{Timeout: 20 * time.Second}
	keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)
	validTargets = map[string]bool{"production": true, "preview": true, "development": true}
)

type project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Framework string `json:"framework,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
	IsSelf    bool   `json:"is_self"`
}

type envVar struct {
	ID        string   `json:"id"`
	Key       string   `json:"key"`
	Type      string   `json:"type"`
	Target    []string `json:"target"`
	GitBranch string   `json:"git_branch,omitempty"`
	Comment   string   `json:"comment,omitempty"`
	UpdatedAt int64    `json:"updated_at,omitempty"`
	// Plain (non-secret) values are shown directly; others need "reveal".
	Value string `json:"value,omitempty"`
}

func endpoint(path string, query url.Values) string {
	if query == nil { query = url.Values{} }
	if team := strings.TrimSpace(os.Getenv("VERCEL_TEAM_ID")); team != "" { query.Set("teamId", team) }
	if encoded := query.Encode(); encoded != "" { return "https://api.vercel.com" + path + "?" + encoded }
	return "https://api.vercel.com" + path
}

func call(method, target string, body interface{}, destination interface{}) error {
	token := strings.TrimSpace(os.Getenv("VERCEL_TOKEN"))
	if token == "" { return fmt.Errorf("VERCEL_TOKEN belum diisi di Vercel") }
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil { return err }
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil { req.Header.Set("Content-Type", "application/json") }
	resp, err := client.Do(req)
	if err != nil { return fmt.Errorf("Vercel tidak dapat dihubungi: %w", err) }
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		var failure struct { Error struct { Message string `json:"message"` } `json:"error"` }
		_ = json.Unmarshal(data, &failure)
		if failure.Error.Message != "" { return fmt.Errorf("Vercel: %s", failure.Error.Message) }
		return fmt.Errorf("Vercel HTTP %d", resp.StatusCode)
	}
	if destination != nil && len(data) > 0 { return json.Unmarshal(data, destination) }
	return nil
}

func validProjectID(id string) bool {
	return id != "" && len(id) <= 100 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.") == ""
}

func targetsOf(raw interface{}) []string {
	switch value := raw.(type) {
	case string: return []string{value}
	case []interface{}:
		out := []string{}
		for _, item := range value { if text, ok := item.(string); ok { out = append(out, text) } }
		return out
	}
	return []string{}
}

func cleanTargets(input []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range input {
		item = strings.ToLower(strings.TrimSpace(item))
		if !validTargets[item] { return nil, fmt.Errorf("target %q tidak dikenal", item) }
		if !seen[item] { seen[item] = true; out = append(out, item) }
	}
	if len(out) == 0 { return nil, fmt.Errorf("pilih minimal satu environment") }
	return out, nil
}

func audit(action, target string) {
	_, _ = d1.Query(`INSERT INTO admin_audit_log (action, target) VALUES (?, ?)`, action, target)
}

func listProjects(w http.ResponseWriter) {
	var response struct {
		Projects []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Framework string `json:"framework"`
			UpdatedAt int64  `json:"updatedAt"`
		} `json:"projects"`
	}
	if err := call(http.MethodGet, endpoint("/v9/projects", url.Values{"limit": {"100"}}), nil, &response); err != nil {
		util.Error(w, http.StatusBadGateway, err); return
	}
	self := os.Getenv("VERCEL_PROJECT_ID")
	out := []project{}
	for _, item := range response.Projects {
		if strings.Contains(item.Name, "selfupdate-test-") { continue } // temporary build-test projects
		out = append(out, project{ID: item.ID, Name: item.Name, Framework: item.Framework, UpdatedAt: item.UpdatedAt, IsSelf: item.ID != "" && item.ID == self})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsSelf != out[j].IsSelf { return out[j].IsSelf } // DevControl last: safer default
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	util.JSON(w, http.StatusOK, map[string]interface{}{"projects": out})
}

func listEnv(w http.ResponseWriter, projectID string) {
	var response struct { Envs []map[string]interface{} `json:"envs"` }
	if err := call(http.MethodGet, endpoint("/v10/projects/"+url.PathEscape(projectID)+"/env", nil), nil, &response); err != nil {
		util.Error(w, http.StatusBadGateway, err); return
	}
	out := make([]envVar, 0, len(response.Envs))
	for _, item := range response.Envs {
		text := func(key string) string { value, _ := item[key].(string); return value }
		number := func(key string) int64 { value, _ := item[key].(float64); return int64(value) }
		entry := envVar{ID: text("id"), Key: text("key"), Type: text("type"), Target: targetsOf(item["target"]),
			GitBranch: text("gitBranch"), Comment: text("comment"), UpdatedAt: number("updatedAt")}
		if entry.Type == "plain" { entry.Value = text("value") }
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	util.JSON(w, http.StatusOK, map[string]interface{}{"envs": out, "is_self": projectID == os.Getenv("VERCEL_PROJECT_ID")})
}

func reveal(w http.ResponseWriter, projectID, envID string) {
	if !validProjectID(envID) { util.Error(w, http.StatusBadRequest, fmt.Errorf("ID variabel tidak valid")); return }
	var response map[string]interface{}
	if err := call(http.MethodGet, endpoint("/v1/projects/"+url.PathEscape(projectID)+"/env/"+url.PathEscape(envID), nil), nil, &response); err != nil {
		util.Error(w, http.StatusBadGateway, err); return
	}
	kind, _ := response["type"].(string)
	if kind == "sensitive" {
		util.JSON(w, http.StatusOK, map[string]interface{}{"sensitive": true}); return
	}
	value, _ := response["value"].(string)
	util.JSON(w, http.StatusOK, map[string]interface{}{"value": value})
}

type mutation struct {
	Project string   `json:"project"`
	Action  string   `json:"action"`
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Value   *string  `json:"value"`
	Target  []string `json:"target"`
	Type    string   `json:"type"`
	Comment string   `json:"comment"`
}

func redeploy(w http.ResponseWriter, projectID string) {
	var list struct {
		Deployments []struct {
			UID  string `json:"uid"`
			Name string `json:"name"`
		} `json:"deployments"`
	}
	query := url.Values{"projectId": {projectID}, "target": {"production"}, "limit": {"1"}, "state": {"READY"}}
	if err := call(http.MethodGet, endpoint("/v6/deployments", query), nil, &list); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	if len(list.Deployments) == 0 { util.Error(w, http.StatusNotFound, fmt.Errorf("belum ada deployment production yang siap untuk di-redeploy")); return }
	latest := list.Deployments[0]
	var created struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	body := map[string]interface{}{"name": latest.Name, "deploymentId": latest.UID, "target": "production"}
	if err := call(http.MethodPost, endpoint("/v13/deployments", nil), body, &created); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	audit("vercel_env_redeploy", projectID)
	util.JSON(w, http.StatusOK, map[string]interface{}{"ok": true, "deployment_id": created.ID, "url": created.URL})
}

// Handle serves /api/vercel-env.
func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		if query.Get("view") == "projects" { listProjects(w); return }
		projectID := query.Get("project")
		if !validProjectID(projectID) { util.Error(w, http.StatusBadRequest, fmt.Errorf("project tidak valid")); return }
		if envID := query.Get("reveal"); envID != "" { reveal(w, projectID, envID); return }
		listEnv(w, projectID)
		return
	case http.MethodDelete:
		projectID, envID := query.Get("project"), query.Get("id")
		if !validProjectID(projectID) || !validProjectID(envID) { util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan hapus tidak valid")); return }
		if err := call(http.MethodDelete, endpoint("/v9/projects/"+url.PathEscape(projectID)+"/env/"+url.PathEscape(envID), nil), nil, nil); err != nil {
			util.Error(w, http.StatusBadGateway, err); return
		}
		audit("vercel_env_delete", projectID+":"+query.Get("key"))
		util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case http.MethodPost, http.MethodPatch:
	default:
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("metode tidak didukung")); return
	}

	var input mutation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&input); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("data variabel tidak valid")); return
	}
	if !validProjectID(input.Project) { util.Error(w, http.StatusBadRequest, fmt.Errorf("project tidak valid")); return }
	if input.Action == "redeploy" { redeploy(w, input.Project); return }
	if input.Value != nil && len(*input.Value) > 64<<10 { util.Error(w, http.StatusBadRequest, fmt.Errorf("nilai melebihi 64 KB")); return }
	targets, err := cleanTargets(input.Target)
	if err != nil { util.Error(w, http.StatusBadRequest, err); return }
	kind := input.Type
	if kind == "" { kind = "encrypted" }
	if kind != "encrypted" && kind != "sensitive" && kind != "plain" { util.Error(w, http.StatusBadRequest, fmt.Errorf("tipe variabel tidak dikenal")); return }
	if kind == "sensitive" {
		for _, target := range targets {
			if target == "development" { util.Error(w, http.StatusBadRequest, fmt.Errorf("variabel sensitif tidak bisa dipakai untuk Development")); return }
		}
	}

	if r.Method == http.MethodPost {
		key := strings.TrimSpace(input.Key)
		if !keyPattern.MatchString(key) { util.Error(w, http.StatusBadRequest, fmt.Errorf("nama variabel hanya boleh huruf, angka, dan _, tidak diawali angka")); return }
		if input.Value == nil { util.Error(w, http.StatusBadRequest, fmt.Errorf("nilai wajib diisi")); return }
		body := map[string]interface{}{"key": key, "value": *input.Value, "type": kind, "target": targets}
		if input.Comment != "" { body["comment"] = input.Comment }
		if err := call(http.MethodPost, endpoint("/v10/projects/"+url.PathEscape(input.Project)+"/env", url.Values{"upsert": {"true"}}), body, nil); err != nil {
			util.Error(w, http.StatusBadGateway, err); return
		}
		audit("vercel_env_set", input.Project+":"+key)
		util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	if !validProjectID(input.ID) { util.Error(w, http.StatusBadRequest, fmt.Errorf("ID variabel tidak valid")); return }
	body := map[string]interface{}{"target": targets, "type": kind}
	if input.Value != nil { body["value"] = *input.Value } // nil keeps the current value
	if err := call(http.MethodPatch, endpoint("/v9/projects/"+url.PathEscape(input.Project)+"/env/"+url.PathEscape(input.ID), nil), body, nil); err != nil {
		util.Error(w, http.StatusBadGateway, err); return
	}
	audit("vercel_env_update", input.Project+":"+input.Key)
	util.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

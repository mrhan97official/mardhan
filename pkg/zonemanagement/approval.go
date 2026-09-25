// Package zonemanagement lets an administrator approve a Cloudflare zone for
// live traffic metrics and records the same choice in Vercel for future builds.
package zonemanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
)

type zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Matches bool   `json:"matches_domain"`
}

type project struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	Link    *struct {
		Org  string `json:"org"`
		Repo string `json:"repo"`
		Type string `json:"type"`
	} `json:"link"`
}

type approval struct {
	ZoneID       string `json:"zone_id"`
	ZoneName     string `json:"zone_name"`
	ProjectID    string `json:"project_id"`
	VercelSynced bool   `json:"vercel_synced"`
	ApprovedAt   string `json:"approved_at"`
}

var client = &http.Client{Timeout: 10 * time.Second}

func ApprovedZone() (string, error) {
	rows, err := d1.Query(`SELECT zone_id FROM cloudflare_zone_approval WHERE id = 1 LIMIT 1`)
	if err != nil || len(rows) == 0 { return "", err }
	id, _ := rows[0]["zone_id"].(string)
	return id, nil
}

func currentApproval() (*approval, error) {
	rows, err := d1.Query(`SELECT zone_id, zone_name, project_id, vercel_synced, approved_at FROM cloudflare_zone_approval WHERE id = 1 LIMIT 1`)
	if err != nil || len(rows) == 0 { return nil, err }
	row := rows[0]
	result := &approval{}
	result.ZoneID, _ = row["zone_id"].(string)
	result.ZoneName, _ = row["zone_name"].(string)
	result.ProjectID, _ = row["project_id"].(string)
	result.ApprovedAt, _ = row["approved_at"].(string)
	synced, _ := row["vercel_synced"].(float64)
	result.VercelSynced = synced == 1
	return result, nil
}

func providerGET(ctx context.Context, endpoint, token string, destination interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(req)
	if err != nil { return err }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { return fmt.Errorf("HTTP %d", response.StatusCode) }
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(destination)
}

func cloudflareZones(ctx context.Context, token, accountID string) ([]zone, error) {
	zones := make([]zone, 0)
	for page := 1; page <= 10; page++ {
		parameters := url.Values{"account.id": {accountID}, "page": {fmt.Sprint(page)}, "per_page": {"50"}}
		var body struct {
			Success bool `json:"success"`
			Result []zone `json:"result"`
			ResultInfo struct { TotalPages int `json:"total_pages"` } `json:"result_info"`
		}
		if err := providerGET(ctx, "https://api.cloudflare.com/client/v4/zones?"+parameters.Encode(), token, &body); err != nil {
			return nil, fmt.Errorf("daftar zona Cloudflare gagal (%w); periksa izin Zone:Zone:Read", err)
		}
		if !body.Success || body.Result == nil { return nil, fmt.Errorf("daftar zona Cloudflare tidak tersedia; periksa izin Zone:Zone:Read") }
		for _, item := range body.Result {
			if item.ID != "" && item.Name != "" { zones = append(zones, item) }
		}
		if page >= body.ResultInfo.TotalPages || len(body.Result) < 50 { return zones, nil }
	}
	return nil, fmt.Errorf("daftar zona Cloudflare melebihi batas halaman; persetujuan dibatalkan")
}

func productionHost() string {
	value := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(os.Getenv("VERCEL_PROJECT_PRODUCTION_URL")), "."))
	if strings.ContainsAny(value, "/:@?# ") { return "" }
	return value
}

func domainMatches(host, domain string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	return host != "" && domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}

func vercelEndpoint(path string) string {
	endpoint := "https://api.vercel.com"+path
	if team := strings.TrimSpace(os.Getenv("VERCEL_TEAM_ID")); team != "" {
		separator := "?"
		if strings.Contains(endpoint, "?") { separator = "&" }
		endpoint += separator+"teamId="+url.QueryEscape(team)
	}
	return endpoint
}

func vercelProject(ctx context.Context, token string) (project, error) {
	id := strings.TrimSpace(os.Getenv("VERCEL_PROJECT_ID"))
	if id == "" {
		// Older Vercel projects may not expose system variables. Identify the
		// running project from its production domain, or a unique Git link.
		host := productionHost()
		owner, repo := os.Getenv("VERCEL_GIT_REPO_OWNER"), os.Getenv("VERCEL_GIT_REPO_SLUG")
		if host == "" && (owner == "" || repo == "") {
			return project{}, fmt.Errorf("Vercel belum menyediakan VERCEL_PROJECT_ID atau identitas domain/repo project ini; aktifkan System Environment Variables")
		}
		matches := make(map[string]project)
		parameters := url.Values{"limit": {"100"}}
		for page := 0; page < 10; page++ {
			var response struct {
				Projects []project `json:"projects"`
				Pagination struct { Next json.RawMessage `json:"next"` } `json:"pagination"`
			}
			if err := providerGET(ctx, vercelEndpoint("/v10/projects?"+parameters.Encode()), token, &response); err != nil {
				return project{}, fmt.Errorf("gagal mencari project Vercel (%w)", err)
			}
			if response.Projects == nil { return project{}, fmt.Errorf("daftar project Vercel tidak valid") }
			for _, item := range response.Projects {
				found := false
				for _, domain := range item.Domains { if strings.EqualFold(host, domain) && host != "" { found = true } }
				if !found && item.Link != nil && strings.EqualFold(item.Link.Type, "github") && owner != "" && repo != "" {
					found = strings.EqualFold(item.Link.Org, owner) && strings.EqualFold(item.Link.Repo, repo)
				}
				if found && item.ID != "" { matches[item.ID] = item }
			}
			next := strings.Trim(string(response.Pagination.Next), `"`)
			if next == "" || next == "null" { break }
			if page == 9 { return project{}, fmt.Errorf("daftar project Vercel terlalu panjang untuk memastikan project ini") }
			parameters.Set("from", next)
		}
		if len(matches) != 1 { return project{}, fmt.Errorf("project Vercel tidak dapat dikenali secara unik; aktifkan VERCEL_PROJECT_ID di System Environment Variables") }
		for key := range matches { id = key }
	}
	var result project
	if err := providerGET(ctx, vercelEndpoint("/v9/projects/"+url.PathEscape(id)), token, &result); err != nil {
		return project{}, fmt.Errorf("gagal memverifikasi project Vercel (%w)", err)
	}
	if result.ID != id || result.Name == "" { return project{}, fmt.Errorf("identitas project Vercel tidak cocok") }
	return result, nil
}

func matchesProject(z zone, host string, p project) bool {
	if domainMatches(host, z.Name) { return true }
	for _, domain := range p.Domains { if domainMatches(domain, z.Name) { return true } }
	return false
}

// Handle exposes only a server-side, admin-session guarded approval workflow.
// Analytics verification precedes persistence; only previously listed zones
// of the configured Cloudflare account can be approved.
func Handle(w http.ResponseWriter, r *http.Request, verify func(context.Context, string, string) ([]float64, []float64, error)) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST")); return }
	if !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("sesi admin diperlukan")); return }
	cfToken, accountID := strings.TrimSpace(os.Getenv("CF_API_TOKEN")), strings.TrimSpace(os.Getenv("CF_ACCOUNT_ID"))
	if cfToken == "" || accountID == "" { util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("CF_API_TOKEN dan CF_ACCOUNT_ID diperlukan")); return }
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("D1 belum siap: %w", err)); return }
	current, err := currentApproval()
	if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal membaca persetujuan zona: %w", err)); return }
	zones, err := cloudflareZones(r.Context(), cfToken, accountID)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	vercelToken := strings.TrimSpace(os.Getenv("VERCEL_TOKEN"))
	var p project
	var projectError string
	if vercelToken == "" { projectError = "VERCEL_TOKEN diperlukan untuk menyimpan CF_ZONE_ID ke Vercel" } else {
		p, err = vercelProject(r.Context(), vercelToken)
		if err != nil { projectError = err.Error() }
	}
	host := productionHost()
	for index := range zones { zones[index].Matches = matchesProject(zones[index], host, p) }
	sort.Slice(zones, func(i, j int) bool {
		if zones[i].Matches != zones[j].Matches { return zones[i].Matches }
		return zones[i].Name < zones[j].Name
	})
	if r.Method == http.MethodGet {
		util.JSON(w, http.StatusOK, map[string]interface{}{
			"zones": zones, "approved": current,
			"active_zone_id": func() string { if current != nil { return current.ZoneID }; return strings.TrimSpace(os.Getenv("CF_ZONE_ID")) }(),
			"project": map[string]string{"id": p.ID, "name": p.Name, "domain": host},
			"project_error": projectError,
		})
		return
	}
	if projectError != "" { util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("%s", projectError)); return }
	var input struct { ZoneID string `json:"zone_id"` }
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil { util.Error(w, http.StatusBadRequest, fmt.Errorf("pilihan zona tidak valid")); return }
	var selected zone
	for _, item := range zones { if item.ID == input.ZoneID { selected = item; break } }
	if selected.ID == "" { util.Error(w, http.StatusBadRequest, fmt.Errorf("zona tidak terdaftar di akun Cloudflare ini; segarkan pilihan")); return }
	if _, _, err := verify(r.Context(), selected.ID, cfToken); err != nil {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("analitik zona %s tidak dapat dibaca; periksa izin Account Analytics Read (%w)", selected.Name, err)); return
	}
	_, err = d1.Query(`INSERT INTO cloudflare_zone_approval (id, zone_id, zone_name, project_id, vercel_synced, approved_at)
		VALUES (1, ?, ?, ?, 0, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET zone_id = excluded.zone_id, zone_name = excluded.zone_name,
		project_id = excluded.project_id, vercel_synced = 0, approved_at = CURRENT_TIMESTAMP`, selected.ID, selected.Name, p.ID)
	if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal menyimpan persetujuan ke D1: %w", err)); return }
	result := &approval{ZoneID: selected.ID, ZoneName: selected.Name, ProjectID: p.ID}
	message := "Zona disetujui dan metrik langsung aktif."
	if err := saveVercelZone(r.Context(), vercelToken, p.ID, selected.ID); err != nil {
		message += " CF_ZONE_ID belum tersimpan di Vercel: "+err.Error()+". Tekan Setujui lagi untuk mencoba ulang."
	} else if _, err := d1.Query(`UPDATE cloudflare_zone_approval SET vercel_synced = 1 WHERE id = 1 AND zone_id = ? AND project_id = ?`, selected.ID, p.ID); err != nil {
		message += " Vercel sudah diperbarui, tetapi status sinkronisasi D1 belum tercatat; coba lagi."
	} else {
		result.VercelSynced = true
		message += " CF_ZONE_ID juga tersimpan untuk deployment berikutnya di Vercel."
	}
	util.JSON(w, http.StatusOK, map[string]interface{}{"approved": result, "message": message})
}

func saveVercelZone(ctx context.Context, token, projectID, zoneID string) error {
	payload, err := json.Marshal(map[string]interface{}{
		"key": "CF_ZONE_ID", "value": zoneID, "type": "plain", "target": []string{"production"},
	})
	if err != nil { return err }
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		vercelEndpoint("/v10/projects/"+url.PathEscape(projectID)+"/env?upsert=true"), bytes.NewReader(payload))
	if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil { return err }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return fmt.Errorf("HTTP %d (periksa izin mengubah environment project)", response.StatusCode)
	}
	return nil
}

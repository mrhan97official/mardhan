// Package projectdelete removes a GitHub project and its associated resources.
package projectdelete

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "os"
    "sort"
    "strings"
    "time"

    "devcontrol/pkg/archive"
    "devcontrol/pkg/projectthumbnail"
    "devcontrol/pkg/auth"
    "devcontrol/pkg/d1"
    "devcontrol/pkg/setup"
    "devcontrol/pkg/util"
)

type vercelProject struct {
    ID string `json:"id"`
    Name string `json:"name"`
    Domains []string `json:"domains"`
    Link *struct { Repo string `json:"repo"`; Org string `json:"org"`; Type string `json:"type"` } `json:"link"`
}

func validPart(part string) bool {
    if part == "" || part == "." || part == ".." || len(part) > 100 { return false }
    for _, ch := range part {
        if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
            (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') { return false }
    }
    return true
}

func linkedTo(project vercelProject, repo string) bool {
    if project.Link == nil || project.Link.Type != "github" || project.Link.Repo == "" { return false }
    return strings.EqualFold(project.Link.Repo, repo) || strings.EqualFold(project.Link.Org+"/"+project.Link.Repo, repo)
}

func vercelProjects(token, repo, stableName string) ([]vercelProject, error) {
    client := &http.Client{Timeout: 15 * time.Second}
    matches := make(map[string]vercelProject)
    query := url.Values{"repo": {repo}, "limit": {"100"}}
    if team := os.Getenv("VERCEL_TEAM_ID"); team != "" { query.Set("teamId", team) }
    for page := 0; page < 10; page++ {
        req, err := http.NewRequest(http.MethodGet, "https://api.vercel.com/v10/projects?"+query.Encode(), nil)
        if err != nil { return nil, err }
        req.Header.Set("Authorization", "Bearer "+token)
        resp, err := client.Do(req)
        if err != nil { return nil, err }
        data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
        resp.Body.Close()
        if resp.StatusCode != http.StatusOK { return nil, fmt.Errorf("gagal mencari project (HTTP %d)", resp.StatusCode) }
        if readErr != nil { return nil, readErr }
        var result struct { Projects []vercelProject `json:"projects"`; Pagination struct { Next json.RawMessage `json:"next"` } `json:"pagination"` }
        if len(data) == 0 { return nil, fmt.Errorf("daftar project Vercel tidak valid") }
        if data[0] == '[' {
            if err := json.Unmarshal(data, &result.Projects); err != nil { return nil, err }
            if len(result.Projects) >= 100 { return nil, fmt.Errorf("daftar project mungkin terpotong; penghapusan dibatalkan") }
        } else if err := json.Unmarshal(data, &result); err != nil { return nil, err }
        if result.Projects == nil { return nil, fmt.Errorf("Vercel tidak mengembalikan daftar project") }
        for _, project := range result.Projects {
            if project.ID != "" && linkedTo(project, repo) { matches[project.ID] = project }
        }
        next := strings.Trim(string(result.Pagination.Next), `"`)
        if next == "" || next == "null" { break }
        if page == 9 { return nil, fmt.Errorf("daftar project terlalu panjang; penghapusan dibatalkan") }
        query.Set("from", next)
    }
    // ZIP deployments have a stable project name even without a Git link.
    req, err := http.NewRequest(http.MethodGet, "https://api.vercel.com/v9/projects/"+url.PathEscape(stableName), nil)
    if err != nil { return nil, err }
    if team := os.Getenv("VERCEL_TEAM_ID"); team != "" { req.URL.RawQuery = url.Values{"teamId": {team}}.Encode() }
    req.Header.Set("Authorization", "Bearer "+token)
    resp, err := client.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusNotFound {
        if resp.StatusCode != http.StatusOK { return nil, fmt.Errorf("gagal memeriksa project %s (HTTP %d)", stableName, resp.StatusCode) }
        var project vercelProject
        if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&project); err != nil { return nil, err }
        if project.ID == "" || project.Name != stableName { return nil, fmt.Errorf("identitas project Vercel tidak cocok") }
        if project.Link != nil && project.Link.Repo != "" && !linkedTo(project, repo) {
            return nil, fmt.Errorf("project %s terhubung ke repo lain; penghapusan dibatalkan", stableName)
        }
        matches[project.ID] = project
    }
    out := make([]vercelProject, 0, len(matches))
    for _, project := range matches { out = append(out, project) }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out, nil
}

func deleteVercel(token, id string) error {
    endpoint := "https://api.vercel.com/v9/projects/"+url.PathEscape(id)
    if team := os.Getenv("VERCEL_TEAM_ID"); team != "" { endpoint += "?"+url.Values{"teamId": {team}}.Encode() }
    req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
    if err != nil { return err }
    req.Header.Set("Authorization", "Bearer "+token)
    resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
        return fmt.Errorf("HTTP %d dari Vercel", resp.StatusCode)
    }
    return nil
}

func githubTokenOwner(token string) (string, error) {
    req, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
    if err != nil { return "", err }
    req.Header.Set("Authorization", "Bearer "+token)
    resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK { return "", fmt.Errorf("GitHub HTTP %d saat membaca pemilik token", resp.StatusCode) }
    var result struct { Login string `json:"login"` }
    if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil { return "", err }
    if result.Login == "" { return "", fmt.Errorf("GitHub tidak mengembalikan pemilik token") }
    return result.Login, nil
}

// First deployments may fail before services is written. Match those ZIPs
// using the same app-name-to-repo transformation as the deployment handler.
func appRepoName(name string) string {
    var b strings.Builder
    for _, ch := range strings.TrimSpace(name) {
        if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
            (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' { b.WriteRune(ch) } else { b.WriteRune('-') }
    }
    result := strings.Trim(b.String(), "-")
    if result == "" { return "app" }
    return result
}

func githubRepoExists(token, owner, repo string) (bool, error) {
    req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), nil)
    if err != nil { return false, err }
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Accept", "application/vnd.github+json")
    resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
    if err != nil { return false, err }
    defer resp.Body.Close()
    if resp.StatusCode == http.StatusNotFound { return false, nil }
    if resp.StatusCode != http.StatusOK { return false, fmt.Errorf("HTTP %d saat memeriksa repo", resp.StatusCode) }
    var result struct { FullName string `json:"full_name"` }
    if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil { return false, err }
    if !strings.EqualFold(result.FullName, owner+"/"+repo) { return false, fmt.Errorf("nama repo berubah menjadi %s; segarkan Projects", result.FullName) }
    return true, nil
}

func deleteGithub(token, owner, repo string) error {
    req, err := http.NewRequest(http.MethodDelete, "https://api.github.com/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), nil)
    if err != nil { return err }
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Accept", "application/vnd.github+json")
    req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
    resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusNoContent { return fmt.Errorf("HTTP %d (token memerlukan izin hapus repo/Administration write)", resp.StatusCode) }
    return nil
}

// Handle requires a real admin session and an exact typed repository name.
// Services remain in D1 until the end, so partial provider failures can be retried.
func Handle(w http.ResponseWriter, r *http.Request, stableProjectName func(string, string) string) {
    w.Header().Set("Cache-Control", "no-store")
    if r.Method != http.MethodDelete { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan DELETE")); return }
    if !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("sesi admin diperlukan")); return }
    var input struct { Repo string `json:"repo"`; Confirmation string `json:"confirmation"` }
    if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
        util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan hapus tidak valid")); return
    }
    parts := strings.Split(input.Repo, "/")
    if len(parts) != 2 || !validPart(parts[0]) || !validPart(parts[1]) || input.Confirmation != input.Repo {
        util.Error(w, http.StatusBadRequest, fmt.Errorf("ketik nama lengkap owner/repo untuk mengonfirmasi")); return
    }
    owner, repoName := parts[0], parts[1]
    githubToken, vercelToken := os.Getenv("GITHUB_TOKEN"), os.Getenv("VERCEL_TOKEN")
    if githubToken == "" || vercelToken == "" { util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("GITHUB_TOKEN dan VERCEL_TOKEN wajib dikonfigurasi")); return }
    if os.Getenv("VERCEL_GIT_REPO_SLUG") != "" && strings.EqualFold(owner, os.Getenv("VERCEL_GIT_REPO_OWNER")) && strings.EqualFold(repoName, os.Getenv("VERCEL_GIT_REPO_SLUG")) {
        util.Error(w, http.StatusConflict, fmt.Errorf("repo DevControl yang sedang berjalan tidak dapat dihapus dari panel ini")); return
    }
    if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    thumbnailBusy, err := projectthumbnail.HasActiveUploads(input.Repo)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    if thumbnailBusy { util.Error(w, http.StatusConflict, fmt.Errorf("thumbnail sedang diunggah; tunggu sampai selesai sebelum menghapus project")); return }
    if _, err := d1.Query(`UPDATE deployment_jobs SET status = 'Interrupted', updated_at = CURRENT_TIMESTAMP WHERE status = 'Running' AND lease_until <= CURRENT_TIMESTAMP`); err != nil { util.Error(w, http.StatusBadGateway, err); return }
    if rows, err := d1.Query(`SELECT id FROM deployment_jobs WHERE lower(lock_key) = lower(?) AND status = 'Running' LIMIT 1`, input.Repo); err != nil {
        util.Error(w, http.StatusBadGateway, err); return
    } else if len(rows) > 0 {
        util.Error(w, http.StatusConflict, fmt.Errorf("repo sedang dideploy; tunggu proses selesai sebelum menghapus")); return
    }
    store, err := archive.New()
    if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
    appRows, err := d1.Query(`SELECT DISTINCT name FROM services WHERE lower(repo) = lower(?)
        UNION SELECT DISTINCT target AS name FROM deployment_jobs WHERE lower(lock_key) = lower(?) AND kind != 'self_update'`, input.Repo, input.Repo)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    // An expired failed pipeline may leave an orphan ZIP. Only this token's
    // owner could have created that repo under the standard app naming rule.
    tokenOwner, err := githubTokenOwner(githubToken)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    if strings.EqualFold(owner, tokenOwner) {
        orphanRows, queryErr := d1.Query(`SELECT DISTINCT a.target FROM zip_archives a WHERE a.scope = 'app'
            AND NOT EXISTS (SELECT 1 FROM services s WHERE s.name = a.target AND lower(s.repo) != lower(?))`, input.Repo)
        if queryErr != nil { util.Error(w, http.StatusBadGateway, queryErr); return }
        for _, row := range orphanRows {
            target, _ := row["target"].(string)
            if strings.EqualFold(appRepoName(target), repoName) { appRows = append(appRows, map[string]interface{}{"name": target}) }
        }
    }
    selfRows, err := d1.Query(`SELECT DISTINCT target FROM zip_archives WHERE scope = 'self'
        AND lower(substr(target, 1, instr(target, '@') - 1)) = lower(?)`, input.Repo)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    thumbnailRows, err := d1.Query(`SELECT repo FROM project_thumbnails WHERE repo = ?
        UNION SELECT repo FROM project_thumbnail_uploads WHERE repo = ?
        UNION SELECT repo FROM project_thumbnail_objects WHERE repo = ? LIMIT 1`, input.Repo, input.Repo, input.Repo)
    if err != nil { util.Error(w, http.StatusBadGateway, err); return }
    projects, err := vercelProjects(vercelToken, input.Repo, stableProjectName(owner, repoName))
    if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("Vercel: %w", err)); return }
    for _, project := range projects {
        if project.ID != "" && project.ID == os.Getenv("VERCEL_PROJECT_ID") { util.Error(w, http.StatusConflict, fmt.Errorf("project DevControl yang sedang berjalan tidak dapat dihapus")); return }
        for _, domain := range project.Domains {
            if strings.EqualFold(domain, r.Host) { util.Error(w, http.StatusConflict, fmt.Errorf("project DevControl yang sedang berjalan tidak dapat dihapus")); return }
        }
    }
    exists, err := githubRepoExists(githubToken, owner, repoName)
    if err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("GitHub: %w", err)); return }
    if !exists && len(appRows) == 0 && len(selfRows) == 0 && len(projects) == 0 && len(thumbnailRows) == 0 {
        util.Error(w, http.StatusNotFound, fmt.Errorf("repo tidak ditemukan dan tidak ada data untuk dilanjutkan")); return
    }
    for _, project := range projects {
        if err := deleteVercel(vercelToken, project.ID); err != nil {
            util.Error(w, http.StatusBadGateway, fmt.Errorf("Vercel %s gagal dihapus: %w; coba lagi", project.Name, err)); return
        }
    }
    if exists {
        if err := deleteGithub(githubToken, owner, repoName); err != nil {
            util.Error(w, http.StatusBadGateway, fmt.Errorf("Vercel sudah dihapus, tetapi GitHub gagal: %w; coba lagi", err)); return
        }
    }
    for _, row := range appRows {
        name, _ := row["name"].(string)
        if err := store.DeleteTarget("app", name); err != nil {
            util.Error(w, http.StatusBadGateway, fmt.Errorf("repo terhapus, tetapi arsip %q gagal dibersihkan: %w; coba lagi", name, err)); return
        }
    }
    for _, row := range selfRows {
        target, _ := row["target"].(string)
        if err := store.DeleteTarget("self", target); err != nil {
            util.Error(w, http.StatusBadGateway, fmt.Errorf("repo terhapus, tetapi arsip %q gagal dibersihkan: %w; coba lagi", target, err)); return
        }
    }
    if err := projectthumbnail.Delete(input.Repo, store); err != nil {
        util.Error(w, http.StatusBadGateway, fmt.Errorf("repo terhapus, tetapi thumbnail belum bersih: %w; coba lagi", err)); return
    }
    statements := []string{
        `DELETE FROM api_check_metrics WHERE api_id IN (SELECT id FROM managed_apis WHERE project IN (SELECT name FROM services WHERE lower(repo) = lower(?)))`,
        `DELETE FROM managed_apis WHERE project IN (SELECT name FROM services WHERE lower(repo) = lower(?))`,
        `DELETE FROM deployment_jobs WHERE lower(lock_key) = lower(?)`,
        `DELETE FROM services WHERE lower(repo) = lower(?)`,
    }
    for _, sql := range statements {
        if _, err := d1.Query(sql, input.Repo); err != nil {
            util.Error(w, http.StatusBadGateway, fmt.Errorf("repo terhapus, tetapi data di D1 belum bersih: %w; coba lagi", err)); return
        }
    }
    _, _ = d1.Query(`INSERT INTO admin_audit_log (action, target) VALUES ('delete_project', ?)`, input.Repo)
    util.JSON(w, http.StatusOK, map[string]interface{}{"deleted": true, "repo": input.Repo, "vercel_projects": len(projects)})
}

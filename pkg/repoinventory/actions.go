package repoinventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"devcontrol/pkg/d1"
	"devcontrol/pkg/util"
	"devcontrol/pkg/vercelapp"
)

// actionInput is POST /api/databases?view=inventory.
//
//	link-project:   connect an unconnected project to a GitHub repo, then
//	                (unless deploy=false) build its default branch from GitHub.
//	delete-preview: what a delete would remove, for the confirmation dialog.
//	delete-project: delete the project; confirm must equal its name.
//
// Deleting is allowed for projects without a Git repository and for projects
// linked to a GitHub repo that GITHUB_TOKEN cannot list ("di luar token").
// The linked repo itself is never touched.
type actionInput struct {
	Action    string `json:"action"`
	ProjectID string `json:"project_id"`
	Repo      string `json:"repo"`
	Deploy    *bool  `json:"deploy"`
	Confirm   string `json:"confirm"`
}

func handleAction(w http.ResponseWriter, r *http.Request, appName func(owner, repo string) string) {
	var input actionInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan tidak valid"))
		return
	}
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if input.ProjectID == "" || len(input.ProjectID) > 120 {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("project_id wajib diisi"))
		return
	}
	token := strings.TrimSpace(os.Getenv("VERCEL_TOKEN"))
	if token == "" {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("VERCEL_TOKEN belum diisi di environment variables Vercel"))
		return
	}
	client := vercelapp.New(token)
	project, found, err := client.GetProject(input.ProjectID)
	if err != nil {
		util.Error(w, http.StatusBadGateway, fmt.Errorf("gagal membaca project Vercel: %w", err))
		return
	}
	if !found || project.ID != input.ProjectID {
		util.Error(w, http.StatusNotFound, fmt.Errorf("project Vercel tidak ditemukan; muat ulang daftar"))
		return
	}
	linkedRepo := ""
	if hasGitLink(project) {
		if input.Action != "delete-preview" && input.Action != "delete-project" {
			util.Error(w, http.StatusConflict, fmt.Errorf("project %s sudah terhubung ke Git; muat ulang daftar", project.Name))
			return
		}
		repo, outside, err := outsideGithubRepo(project)
		if err != nil {
			util.Error(w, http.StatusBadGateway, err)
			return
		}
		if !outside {
			message := fmt.Sprintf("project %s terhubung ke repo yang ada di akun GitHub Anda; hapus lewat kartu repo di halaman Projects", project.Name)
			if !strings.EqualFold(project.LinkType, "github") {
				message = fmt.Sprintf("project %s terhubung ke %s; hapus langsung di Vercel", project.Name, project.LinkType)
			}
			util.Error(w, http.StatusConflict, errors.New(message))
			return
		}
		linkedRepo = repo
	}
	if isSelf(project) {
		util.Error(w, http.StatusForbidden, fmt.Errorf("project %s adalah project aplikasi DevControl ini sendiri", project.Name))
		return
	}

	switch input.Action {
	case "link-project":
		linkProject(w, client, project, input)
	case "delete-preview":
		previewDelete(w, client, project, linkedRepo, appName)
	case "delete-project":
		deleteProject(w, client, project, linkedRepo, input, appName)
	default:
		util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi tidak dikenal"))
	}
}

func hasGitLink(project vercelapp.Project) bool {
	if project.LinkType == "" {
		return false
	}
	return !(strings.EqualFold(project.LinkType, "github") && strings.TrimSpace(project.LinkRepo) == "")
}

// outsideGithubRepo reports the GitHub repo a project is linked to and
// whether GITHUB_TOKEN cannot list it — the same test the inventory uses for
// "Repo di luar token". GitLab/Bitbucket links are never "outside".
func outsideGithubRepo(project vercelapp.Project) (string, bool, error) {
	if !strings.EqualFold(project.LinkType, "github") {
		return "", false, nil
	}
	full := githubLinkRepo(project.LinkOrg, project.LinkRepo)
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return full, false, fmt.Errorf("GITHUB_TOKEN diperlukan untuk memastikan repo %s memang di luar token", full)
	}
	repos, truncated, err := fetchGithub(token)
	if err != nil {
		return full, false, err
	}
	for _, repo := range repos {
		if strings.EqualFold(repo.FullName, full) {
			return full, false, nil
		}
	}
	if truncated {
		return full, false, fmt.Errorf("daftar repo GitHub terpotong; tidak dapat memastikan repo %s di luar token", full)
	}
	return full, true, nil
}

func isSelf(project vercelapp.Project) bool {
	self := strings.TrimSpace(os.Getenv("VERCEL_PROJECT_ID"))
	return self != "" && (project.ID == self || strings.EqualFold(project.Name, self))
}

func linkProject(w http.ResponseWriter, client *vercelapp.Client, project vercelapp.Project, input actionInput) {
	owner, name, ok := splitRepo(input.Repo)
	if !ok {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("pilih repo GitHub dengan format owner/nama"))
		return
	}
	githubToken := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if githubToken == "" {
		util.Error(w, http.StatusPreconditionFailed, fmt.Errorf("GITHUB_TOKEN belum diisi di environment variables Vercel"))
		return
	}
	repo, err := githubRepoInfo(githubToken, owner, name)
	if err != nil {
		util.Error(w, http.StatusBadGateway, err)
		return
	}
	owner, name, _ = splitRepo(repo.FullName)

	if err := client.LinkProject(project.ID, repo.FullName); err != nil {
		util.Error(w, http.StatusBadGateway, fmt.Errorf(
			"Vercel belum dapat menghubungkan repo %s (%s). Pastikan akun GitHub sudah terhubung di Vercel dan repo ini diizinkan untuk aplikasi Vercel di GitHub (Settings → Applications → Vercel → Repository access), lalu coba lagi",
			repo.FullName, err.Error()))
		return
	}
	audit("link_vercel_project", project.Name+" -> "+repo.FullName)
	activity("Project Vercel terhubung ke Git", project.Name+" → "+repo.FullName, "check")

	result := map[string]interface{}{"ok": true, "project": project.Name, "repo": repo.FullName, "branch": repo.DefaultBranch}
	if input.Deploy != nil && !*input.Deploy {
		util.JSON(w, http.StatusOK, result)
		return
	}
	// Linking alone does not build anything until the next push, so import the
	// current default branch right away. A failure here keeps the link.
	sha, err := githubBranchSHA(githubToken, repo.FullName, repo.DefaultBranch)
	if err != nil {
		result["deploy_error"] = err.Error()
		util.JSON(w, http.StatusOK, result)
		return
	}
	deployment, err := client.CreateGitDeployment(project.Name, project.ID, owner, name, repo.DefaultBranch, sha)
	if err != nil {
		result["deploy_error"] = "repo sudah terhubung, tetapi deployment dari GitHub gagal dimulai: " + err.Error()
		util.JSON(w, http.StatusOK, result)
		return
	}
	result["deployment_id"] = deployment.ID
	if deployment.URL != "" {
		result["deployment_url"] = "https://" + strings.TrimPrefix(strings.TrimPrefix(deployment.URL, "https://"), "http://")
	}
	util.JSON(w, http.StatusOK, result)
}

func previewDelete(w http.ResponseWriter, client *vercelapp.Client, project vercelapp.Project, linkedRepo string, appName func(owner, repo string) string) {
	managed, err := managedRepoFor(project.Name, linkedRepo, appName)
	if err != nil {
		util.Error(w, http.StatusBadGateway, err)
		return
	}
	domains, domainErr := client.CustomDomains(project.ID)
	out := map[string]interface{}{"project": project.Name, "managed_repo": managed, "domains": domains, "linked_repo": linkedRepo}
	if domains == nil {
		out["domains"] = []string{}
	}
	if domainErr != nil {
		out["domains_error"] = domainErr.Error()
	}
	util.JSON(w, http.StatusOK, out)
}

func deleteProject(w http.ResponseWriter, client *vercelapp.Client, project vercelapp.Project, linkedRepo string, input actionInput, appName func(owner, repo string) string) {
	if strings.TrimSpace(input.Confirm) != project.Name {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("ketik nama project %s untuk mengonfirmasi penghapusan", project.Name))
		return
	}
	managed, err := managedRepoFor(project.Name, linkedRepo, appName)
	if err != nil {
		util.Error(w, http.StatusBadGateway, err)
		return
	}
	if managed != "" {
		util.Error(w, http.StatusConflict, fmt.Errorf(
			"project %s dipakai aplikasi DevControl %s. Hapus lewat halaman Projects agar data D1, arsip ZIP, dan thumbnail ikut dibersihkan, atau hubungkan ke Git",
			project.Name, managed))
		return
	}
	if err := client.DeleteProject(project.ID); err != nil {
		util.Error(w, http.StatusBadGateway, fmt.Errorf("Vercel menolak penghapusan: %w", err))
		return
	}
	target := project.Name + " (" + project.ID + ")"
	if linkedRepo != "" {
		target += " linked " + linkedRepo
	}
	audit("delete_vercel_project", target)
	activity("Project Vercel dihapus", project.Name+" beserta deployment, domain, dan environment variable-nya", "database")
	util.JSON(w, http.StatusOK, map[string]interface{}{"ok": true, "project": project.Name})
}

// managedProjects maps lower-case Vercel project names used by apps
// registered in DevControl (D1 services) to their repo. Without D1 no app can
// be registered, so the map is empty.
func managedProjects(appName func(owner, repo string) string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(os.Getenv("CF_D1_DATABASE_ID")) == "" {
		return out, nil
	}
	rows, err := d1.Query(`SELECT repo, app_url FROM services WHERE repo IS NOT NULL AND trim(repo) != ''`)
	if err != nil {
		return nil, fmt.Errorf("tidak dapat memastikan project tidak dipakai aplikasi DevControl: %w", err)
	}
	for _, row := range rows {
		repo, _ := row["repo"].(string)
		owner, name, ok := splitRepo(repo)
		if !ok {
			continue
		}
		if appName != nil {
			out[strings.ToLower(appName(owner, name))] = repo
		}
		// Vercel names cannot contain ':', so repo keys never clash with names.
		out[repoKey(repo)] = repo
		if raw, _ := row["app_url"].(string); raw != "" {
			if parsed, err := url.Parse(strings.TrimSpace(raw)); err == nil {
				host := strings.ToLower(parsed.Hostname())
				if strings.HasSuffix(host, ".vercel.app") {
					out[strings.TrimSuffix(host, ".vercel.app")] = repo
				}
			}
		}
	}
	return out, nil
}

func repoKey(repo string) string { return "repo:" + strings.ToLower(strings.TrimSpace(repo)) }

// managedRepoFor names the DevControl app that uses this project, by the
// project's name or by the repo it is linked to.
func managedRepoFor(projectName, linkedRepo string, appName func(owner, repo string) string) (string, error) {
	managed, err := managedProjects(appName)
	if err != nil {
		return "", err
	}
	return lookupManaged(managed, projectName, linkedRepo), nil
}

func lookupManaged(managed map[string]string, projectName, linkedRepo string) string {
	if repo := managed[strings.ToLower(projectName)]; repo != "" {
		return repo
	}
	if linkedRepo != "" {
		return managed[repoKey(linkedRepo)]
	}
	return ""
}

func splitRepo(full string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(full), "/")
	if len(parts) != 2 || !validRepoPart(parts[0]) || !validRepoPart(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func validRepoPart(part string) bool {
	if part == "" || part == "." || part == ".." || len(part) > 100 {
		return false
	}
	for _, ch := range part {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
			return false
		}
	}
	return true
}

type githubRepoDetail struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

func githubGet(token, endpoint string, out interface{}) (int, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("gagal menghubungi GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&detail)
		if detail.Message == "" {
			detail.Message = "periksa GITHUB_TOKEN dan akses repository"
		}
		return resp.StatusCode, errors.New(detail.Message)
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func githubRepoInfo(token, owner, name string) (githubRepoDetail, error) {
	var repo githubRepoDetail
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(owner), url.PathEscape(name))
	status, err := githubGet(token, endpoint, &repo)
	if status == http.StatusNotFound {
		return repo, fmt.Errorf("repo %s/%s tidak ditemukan atau tidak dapat diakses GITHUB_TOKEN", owner, name)
	}
	if err != nil {
		return repo, fmt.Errorf("GitHub: %w", err)
	}
	if repo.FullName == "" || repo.DefaultBranch == "" {
		return repo, fmt.Errorf("repo %s/%s kosong atau belum punya branch utama", owner, name)
	}
	return repo, nil
}

func githubBranchSHA(token, fullName, branch string) (string, error) {
	owner, name, ok := splitRepo(fullName)
	if !ok {
		return "", fmt.Errorf("nama repo tidak valid")
	}
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/branches/%s", url.PathEscape(owner), url.PathEscape(name), url.PathEscape(branch))
	if _, err := githubGet(token, endpoint, &out); err != nil {
		return "", fmt.Errorf("repo sudah terhubung, tetapi commit terakhir branch %s tidak terbaca: %w", branch, err)
	}
	if out.Commit.SHA == "" {
		return "", fmt.Errorf("repo sudah terhubung, tetapi branch %s belum punya commit", branch)
	}
	return out.Commit.SHA, nil
}

func audit(action, target string) {
	if strings.TrimSpace(os.Getenv("CF_D1_DATABASE_ID")) == "" {
		return
	}
	_, _ = d1.Query(`INSERT INTO admin_audit_log (action, target) VALUES (?, ?)`, action, target)
}

func activity(title, description, icon string) {
	if strings.TrimSpace(os.Getenv("CF_D1_DATABASE_ID")) == "" {
		return
	}
	_, _ = d1.Query(`INSERT INTO activity_log (title, description, icon) VALUES (?, ?, ?)`, title, description, icon)
}

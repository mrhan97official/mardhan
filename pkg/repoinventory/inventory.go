// Package repoinventory compares the repositories GITHUB_TOKEN can see with
// the projects VERCEL_TOKEN can see, so the Databases page can show how many
// exist on each side and which Vercel projects have no Git repository
// connected. It is read-only: nothing is linked, created or deleted here.
package repoinventory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"devcontrol/pkg/util"
	"devcontrol/pkg/vercelapp"
)

// Project connection states reported to the browser.
const (
	StatusConnected   = "connected"   // linked to a GitHub repo the token can see
	StatusOutside     = "outside"     // linked to a GitHub repo outside GITHUB_TOKEN's access
	StatusOtherGit    = "other-git"   // linked to GitLab/Bitbucket
	StatusUnconnected = "unconnected" // no Git repository connected
)

const (
	maxGithubPages = 10
	maxVercelPages = 10
)

// Repo is one GitHub repository plus the Vercel projects linked to it.
type Repo struct {
	FullName      string   `json:"full_name"`
	Name          string   `json:"name"`
	Private       bool     `json:"private"`
	Archived      bool     `json:"archived"`
	Fork          bool     `json:"fork"`
	HTMLURL       string   `json:"html_url"`
	PushedAt      string   `json:"pushed_at"`
	DefaultBranch string   `json:"default_branch"`
	Projects      []string `json:"projects"`
}

// Project is one Vercel project and its Git connection state.
type Project struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Framework        string `json:"framework,omitempty"`
	UpdatedAt        int64  `json:"updated_at,omitempty"`
	GitProvider      string `json:"git_provider,omitempty"`
	Repo             string `json:"repo,omitempty"`
	ProductionBranch string `json:"production_branch,omitempty"`
	Status           string `json:"status"`
	SuggestedRepo    string `json:"suggested_repo,omitempty"`
	URL              string `json:"url,omitempty"`
	ManagedRepo      string `json:"managed_repo,omitempty"` // app registered in DevControl that uses this project
	Self             bool   `json:"self,omitempty"`         // the project hosting this DevControl
}

// Source reports whether one side could be read.
type Source struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// Result is the whole inventory response.
type Result struct {
	GitHub    Source    `json:"github"`
	Vercel    Source    `json:"vercel"`
	Repos     []Repo    `json:"repos"`
	Projects  []Project `json:"projects"`
	CheckedAt string    `json:"checked_at"`
}

type vercelLink struct {
	Type             string `json:"type"`
	Org              string `json:"org"`
	Repo             string `json:"repo"`
	ProductionBranch string `json:"productionBranch"`
	ProjectNamespace string `json:"projectNamespace"`
	ProjectName      string `json:"projectName"`
	Owner            string `json:"owner"`
	Slug             string `json:"slug"`
}

type vercelProject struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Framework *string     `json:"framework"`
	UpdatedAt int64       `json:"updatedAt"`
	Link      *vercelLink `json:"link"`
}

// Handle answers /api/databases?view=inventory: GET returns the inventory,
// POST runs a connect/delete action on one unconnected project (owner/admin
// only, enforced by the gateway's permission matrix). It returns false for
// every other request so the caller can continue routing.
func Handle(w http.ResponseWriter, r *http.Request, appName func(owner, repo string) string) bool {
	if r.URL.Query().Get("view") != "inventory" {
		return false
	}
	switch r.Method {
	case http.MethodGet:
		util.JSON(w, http.StatusOK, Build(appName))
	case http.MethodPost:
		handleAction(w, r, appName)
	default:
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST"))
	}
	return true
}

// Build reads both sides concurrently. A failure on one side is reported in
// its Source and never hides the other side.
func Build(appName func(owner, repo string) string) Result {
	result := Result{Repos: []Repo{}, Projects: []Project{}, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	githubToken := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	vercelToken := strings.TrimSpace(os.Getenv("VERCEL_TOKEN"))
	result.GitHub.Configured = githubToken != ""
	result.Vercel.Configured = vercelToken != ""

	var (
		wg     sync.WaitGroup
		repos  []Repo
		raw    []vercelProject
		scope  string
		github Source
		vercel Source
	)
	github.Configured, vercel.Configured = result.GitHub.Configured, result.Vercel.Configured

	if githubToken == "" {
		github.Error = "GITHUB_TOKEN belum diisi di environment variables Vercel"
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, truncated, err := fetchGithub(githubToken)
			if err != nil {
				github.Error = err.Error()
				return
			}
			repos, github.OK, github.Truncated = list, true, truncated
		}()
	}
	if vercelToken == "" {
		vercel.Error = "VERCEL_TOKEN belum diisi di environment variables Vercel"
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := vercelapp.New(vercelToken)
			list, truncated, err := fetchVercel(client)
			if err != nil {
				vercel.Error = err.Error()
				return
			}
			raw, vercel.OK, vercel.Truncated = list, true, truncated
			if len(list) > 0 {
				scope = vercelScope(client)
			}
		}()
	}
	wg.Wait()

	result.GitHub, result.Vercel = github, vercel
	if repos == nil {
		repos = []Repo{}
	}
	result.Repos, result.Projects = classify(repos, raw, github.OK, appName, scope)
	// Best effort: an unreadable D1 only hides the "dikelola DevControl" hint;
	// the delete action re-checks and refuses when it cannot be sure.
	managed, _ := managedProjects(appName)
	self := strings.TrimSpace(os.Getenv("VERCEL_PROJECT_ID"))
	for i := range result.Projects {
		result.Projects[i].ManagedRepo = managed[strings.ToLower(result.Projects[i].Name)]
		result.Projects[i].Self = self != "" && (result.Projects[i].ID == self || strings.EqualFold(result.Projects[i].Name, self))
	}
	return result
}

// classify links projects to repos and labels each project's Git state.
// When GitHub could not be read, a GitHub-linked project is reported as
// connected because its repo cannot be checked either way.
func classify(repos []Repo, raw []vercelProject, githubOK bool, appName func(owner, repo string) string, scope string) ([]Repo, []Project) {
	index := make(map[string]int, len(repos))
	for i := range repos {
		repos[i].Projects = []string{}
		index[strings.ToLower(repos[i].FullName)] = i
	}

	projects := make([]Project, 0, len(raw))
	for _, item := range raw {
		p := Project{ID: item.ID, Name: item.Name, UpdatedAt: item.UpdatedAt, Status: StatusUnconnected}
		if item.Framework != nil {
			p.Framework = *item.Framework
		}
		if scope != "" && item.Name != "" {
			p.URL = "https://vercel.com/" + url.PathEscape(scope) + "/" + url.PathEscape(item.Name)
		}
		link := item.Link
		if link != nil {
			provider := strings.ToLower(strings.TrimSpace(link.Type))
			switch {
			case provider == "github" && strings.TrimSpace(link.Repo) != "":
				p.GitProvider = provider
				p.Repo = githubLinkRepo(link.Org, link.Repo)
				p.ProductionBranch = link.ProductionBranch
				if i, ok := index[strings.ToLower(p.Repo)]; ok {
					p.Status = StatusConnected
					repos[i].Projects = append(repos[i].Projects, item.Name)
				} else if githubOK {
					p.Status = StatusOutside
				} else {
					p.Status = StatusConnected
				}
			case provider != "" && provider != "github":
				p.GitProvider = provider
				p.Repo = otherLinkRepo(*link)
				p.ProductionBranch = link.ProductionBranch
				p.Status = StatusOtherGit
			}
		}
		projects = append(projects, p)
	}

	for i := range projects {
		if projects[i].Status == StatusUnconnected {
			projects[i].SuggestedRepo = suggestRepo(projects[i].Name, repos, appName)
		}
	}
	return repos, projects
}

func githubLinkRepo(org, repo string) string {
	repo = strings.TrimSpace(repo)
	if strings.Contains(repo, "/") || strings.TrimSpace(org) == "" {
		return repo
	}
	return strings.TrimSpace(org) + "/" + repo
}

func otherLinkRepo(link vercelLink) string {
	switch {
	case link.ProjectNamespace != "" && link.ProjectName != "":
		return link.ProjectNamespace + "/" + link.ProjectName
	case link.Owner != "" && link.Slug != "":
		return link.Owner + "/" + link.Slug
	case link.Repo != "":
		return githubLinkRepo(link.Org, link.Repo)
	}
	return ""
}

// suggestRepo names the one repo whose name matches the project name (or
// DevControl's own naming scheme). Ambiguous matches suggest nothing.
func suggestRepo(project string, repos []Repo, appName func(owner, repo string) string) string {
	target := normalize(project)
	if target == "" {
		return ""
	}
	found := ""
	for _, repo := range repos {
		owner, name := splitFullName(repo.FullName)
		match := normalize(name) == target
		if !match && appName != nil && owner != "" && name != "" {
			match = appName(owner, name) == project
		}
		if !match {
			continue
		}
		if found != "" && !strings.EqualFold(found, repo.FullName) {
			return ""
		}
		found = repo.FullName
	}
	return found
}

func splitFullName(full string) (string, string) {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 {
		return "", full
	}
	return parts[0], parts[1]
}

// normalize lower-cases a name and folds every run of non-alphanumerics into
// one hyphen, matching how Vercel project names are usually derived.
func normalize(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func fetchGithub(token string) ([]Repo, bool, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	repos := []Repo{}
	seen := map[string]bool{}
	for page := 1; page <= maxGithubPages; page++ {
		endpoint := fmt.Sprintf(
			"https://api.github.com/user/repos?per_page=100&page=%d&sort=pushed&affiliation=owner,collaborator,organization_member",
			page,
		)
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, false, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := client.Do(req)
		if err != nil {
			return nil, false, fmt.Errorf("gagal menghubungi GitHub: %w", err)
		}
		if resp.StatusCode >= 300 {
			var detail struct {
				Message string `json:"message"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&detail)
			resp.Body.Close()
			if detail.Message == "" {
				detail.Message = "periksa GITHUB_TOKEN dan akses repository"
			}
			return nil, false, fmt.Errorf("GitHub HTTP %d: %s", resp.StatusCode, detail.Message)
		}
		var batch []Repo
		err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&batch)
		resp.Body.Close()
		if err != nil {
			return nil, false, fmt.Errorf("respons GitHub tidak valid: %w", err)
		}
		for _, repo := range batch {
			key := strings.ToLower(repo.FullName)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			repos = append(repos, repo)
		}
		if len(batch) < 100 {
			return repos, false, nil
		}
	}
	return repos, true, nil
}

func fetchVercel(client *vercelapp.Client) ([]vercelProject, bool, error) {
	query := url.Values{"limit": {"100"}}
	projects := []vercelProject{}
	seen := map[string]bool{}
	for page := 0; page < maxVercelPages; page++ {
		var data json.RawMessage
		if err := client.Do(http.MethodGet, "/v10/projects", query, nil, &data); err != nil {
			return nil, false, fmt.Errorf("Vercel: %w", err)
		}
		data = bytes.TrimSpace(data)
		var result struct {
			Projects   []vercelProject `json:"projects"`
			Pagination struct {
				Next json.RawMessage `json:"next"`
			} `json:"pagination"`
		}
		if len(data) > 0 && data[0] == '[' {
			if err := json.Unmarshal(data, &result.Projects); err != nil {
				return nil, false, fmt.Errorf("respons project Vercel tidak valid: %w", err)
			}
		} else if err := json.Unmarshal(data, &result); err != nil {
			return nil, false, fmt.Errorf("respons project Vercel tidak valid: %w", err)
		}
		added := 0
		for _, item := range result.Projects {
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			projects = append(projects, item)
			added++
		}
		next := paginationNext(result.Pagination.Next)
		if next == "" {
			return projects, false, nil
		}
		if added == 0 {
			// The cursor no longer yields new projects; stop instead of looping.
			return projects, true, nil
		}
		query.Set("from", next)
	}
	return projects, true, nil
}

func paginationNext(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

// vercelScope returns the team slug (or personal username) used in Vercel
// dashboard links. Best effort: an empty result only hides the links.
func vercelScope(client *vercelapp.Client) string {
	if team := strings.TrimSpace(os.Getenv("VERCEL_TEAM_ID")); team != "" {
		var out struct {
			Slug string `json:"slug"`
		}
		if client.Do(http.MethodGet, "/v2/teams/"+url.PathEscape(team), nil, nil, &out) == nil {
			return out.Slug
		}
		return ""
	}
	var out struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if client.Do(http.MethodGet, "/v2/user", nil, nil, &out) == nil {
		return out.User.Username
	}
	return ""
}

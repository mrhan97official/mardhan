// Package vercelapp holds what an app deployment needs from Vercel beyond a
// plain file upload: the right framework preset, a project connected to the
// app's GitHub repo (the same state as a manual "Import Git Repository"),
// the app's .env values copied into the project, and a check that the page
// Vercel serves is really the app and not Vercel's own 404.
//
// Why this exists: a file-upload deployment with skipAutoDetectionConfirmation
// skips framework detection, so a Vite/CRA app landed in a project with no
// framework. Vercel then served the source folder instead of the build output
// (dist/, build/) and every visitor saw "404 NOT_FOUND" although the build
// itself reported READY.
package vercelapp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// File is one entry of the uploaded ZIP, path relative to the project root.
type File struct {
	Path string
	Data []byte
}

// EnvVar is one KEY=value pair read from the ZIP's .env files.
type EnvVar struct {
	Key   string
	Value string
}

// frameworkByDependency maps package.json dependencies to Vercel framework
// presets. Order matters: meta-frameworks built on Vite come before "vite".
var frameworkByDependency = []struct {
	slug string
	deps []string
}{
	{slug: "nextjs", deps: []string{"next"}},
	{slug: "nuxtjs", deps: []string{"nuxt", "nuxt3"}},
	{slug: "sveltekit-1", deps: []string{"@sveltejs/kit"}},
	{slug: "astro", deps: []string{"astro"}},
	{slug: "react-router", deps: []string{"@react-router/dev"}},
	{slug: "remix", deps: []string{"@remix-run/dev"}},
	{slug: "gatsby", deps: []string{"gatsby"}},
	{slug: "docusaurus-2", deps: []string{"@docusaurus/core"}},
	{slug: "vitepress", deps: []string{"vitepress"}},
	{slug: "vuepress", deps: []string{"vuepress"}},
	{slug: "angular", deps: []string{"@angular/cli"}},
	{slug: "create-react-app", deps: []string{"react-scripts"}},
	{slug: "vue", deps: []string{"@vue/cli-service"}},
	{slug: "preact", deps: []string{"preact-cli"}},
	{slug: "eleventy", deps: []string{"@11ty/eleventy"}},
	{slug: "vite", deps: []string{"vite"}},
	{slug: "parcel", deps: []string{"parcel"}},
}

type packageManifest struct {
	Scripts         map[string]string          `json:"scripts"`
	Dependencies    map[string]json.RawMessage `json:"dependencies"`
	DevDependencies map[string]json.RawMessage `json:"devDependencies"`
}

func readManifest(files []File) (packageManifest, bool) {
	var manifest packageManifest
	for _, file := range files {
		if file.Path != "package.json" {
			continue
		}
		if json.Unmarshal(file.Data, &manifest) != nil {
			return manifest, false
		}
		return manifest, true
	}
	return manifest, false
}

// DetectFramework returns the Vercel framework preset for the ZIP, or "" when
// package.json names none (Vercel's own detection is used then).
func DetectFramework(files []File) string {
	manifest, ok := readManifest(files)
	if !ok {
		return ""
	}
	for _, candidate := range frameworkByDependency {
		for _, dep := range candidate.deps {
			if _, found := manifest.Dependencies[dep]; found {
				return candidate.slug
			}
			if _, found := manifest.DevDependencies[dep]; found {
				return candidate.slug
			}
		}
	}
	return ""
}

// ExpectsHomePage: the ZIP is a web app, so Vercel's own 404 on "/" means
// the deployment is broken. API-only projects legitimately have no page.
func ExpectsHomePage(files []File) bool {
	for _, file := range files {
		if file.Path == "index.html" || file.Path == "public/index.html" {
			return true
		}
	}
	manifest, ok := readManifest(files)
	if !ok {
		return false
	}
	if strings.TrimSpace(manifest.Scripts["build"]) != "" {
		return true
	}
	return DetectFramework(files) != ""
}

// EnvFromFiles reads the ZIP's root .env files with the usual precedence
// (.env < .env.production < .env.local < .env.production.local).
func EnvFromFiles(files []File) []EnvVar {
	values := map[string]string{}
	for _, name := range []string{".env", ".env.production", ".env.local", ".env.production.local"} {
		for _, file := range files {
			if file.Path != name {
				continue
			}
			for key, value := range parseDotenv(string(file.Data)) {
				values[key] = value
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]EnvVar, 0, len(keys))
	for _, key := range keys {
		out = append(out, EnvVar{Key: key, Value: values[key]})
	}
	return out
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		letter := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func parseDotenv(text string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if !validEnvKey(key) {
			continue
		}
		if value != "" && (value[0] == '"' || value[0] == '\'' || value[0] == '`') {
			quote := value[0]
			body := value[1:]
			// Multi-line quoted values (private keys) continue until the
			// closing quote.
			for strings.IndexByte(body, quote) < 0 && i+1 < len(lines) {
				i++
				body += "\n" + lines[i]
			}
			if end := strings.IndexByte(body, quote); end >= 0 {
				body = body[:end]
			}
			if quote == '"' {
				body = strings.ReplaceAll(body, `\n`, "\n")
			}
			out[key] = body
			continue
		}
		if hash := strings.Index(value, " #"); hash >= 0 {
			value = strings.TrimSpace(value[:hash])
		}
		out[key] = value
	}
	return out
}

// Client is a small Vercel REST client scoped to VERCEL_TEAM_ID.
type Client struct {
	token string
	team  string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, team: strings.TrimSpace(os.Getenv("VERCEL_TEAM_ID")), http: &http.Client{Timeout: 25 * time.Second}}
}

// APIError is a non-2xx Vercel response, including the framework Vercel
// detected when it asks for project settings.
type APIError struct {
	Status          int
	Code            string
	Message         string
	Framework       json.RawMessage
	ProjectSettings json.RawMessage
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("HTTP %d dari Vercel", e.Status)
}

func (e *APIError) mentionsSettings() bool {
	text := strings.ToLower(e.Code + " " + e.Message)
	return strings.Contains(text, "framework") || strings.Contains(text, "project settings") ||
		strings.Contains(text, "projectsettings") || strings.Contains(text, "project_settings")
}

// detectedSettings turns Vercel's "confirm these settings" answer into the
// projectSettings of the retry. Only fields the deployment API accepts are
// copied.
func (e *APIError) detectedSettings() (map[string]interface{}, string) {
	slug := ""
	if len(e.Framework) > 0 {
		var object struct {
			Slug string `json:"slug"`
		}
		if json.Unmarshal(e.Framework, &object) == nil && object.Slug != "" {
			slug = object.Slug
		} else {
			var plain string
			if json.Unmarshal(e.Framework, &plain) == nil {
				slug = plain
			}
		}
	}
	proposed := map[string]interface{}{}
	if len(e.ProjectSettings) > 0 {
		_ = json.Unmarshal(e.ProjectSettings, &proposed)
	}
	settings := map[string]interface{}{}
	for _, key := range []string{"framework", "buildCommand", "devCommand", "installCommand", "outputDirectory"} {
		if value, ok := proposed[key]; ok {
			settings[key] = value
		}
	}
	if current, _ := settings["framework"].(string); current == "" && slug != "" {
		settings["framework"] = slug
	}
	if len(settings) == 0 {
		return nil, ""
	}
	framework, _ := settings["framework"].(string)
	return settings, framework
}

// Do performs one Vercel API call. out may be nil.
func (c *Client) Do(method, endpoint string, query url.Values, payload, out interface{}) error {
	if query == nil {
		query = url.Values{}
	}
	if c.team != "" {
		query.Set("teamId", c.team)
	}
	full := "https://api.vercel.com" + endpoint
	if encoded := query.Encode(); encoded != "" {
		full += "?" + encoded
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, full, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode}
		var wrapper struct {
			Error *struct {
				Code            string          `json:"code"`
				Message         string          `json:"message"`
				Framework       json.RawMessage `json:"framework"`
				ProjectSettings json.RawMessage `json:"projectSettings"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &wrapper) == nil && wrapper.Error != nil {
			apiErr.Code = wrapper.Error.Code
			apiErr.Message = wrapper.Error.Message
			apiErr.Framework = wrapper.Error.Framework
			apiErr.ProjectSettings = wrapper.Error.ProjectSettings
		}
		return apiErr
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Deployment is a created Vercel deployment.
type Deployment struct {
	ID  string
	URL string
}

type deploymentResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (c *Client) createDeployment(body map[string]interface{}, confirm bool) (Deployment, error) {
	query := url.Values{}
	if confirm {
		query.Set("skipAutoDetectionConfirmation", "1")
	}
	var out deploymentResponse
	if err := c.Do(http.MethodPost, "/v13/deployments", query, body, &out); err != nil {
		return Deployment{}, err
	}
	if out.ID == "" || out.URL == "" {
		return Deployment{}, fmt.Errorf("Vercel tidak mengembalikan ID atau URL deployment")
	}
	return Deployment{ID: out.ID, URL: "https://" + out.URL}, nil
}

// CreateFileDeployment uploads the files inline. framework is the preset to
// use ("" = let Vercel decide). The returned string is the framework the
// deployment was really created with ("" when the project's saved settings
// were used).
func (c *Client) CreateFileDeployment(project, target string, files []File, framework string) (Deployment, string, error) {
	inline := make([]map[string]string, 0, len(files))
	for _, file := range files {
		inline = append(inline, map[string]string{
			"file": file.Path, "data": base64.StdEncoding.EncodeToString(file.Data), "encoding": "base64",
		})
	}
	post := func(settings map[string]interface{}, confirm bool) (Deployment, error) {
		body := map[string]interface{}{"name": project, "files": inline}
		if target != "" {
			body["target"] = target
		}
		if settings != nil {
			body["projectSettings"] = settings
		}
		return c.createDeployment(body, confirm)
	}
	if framework != "" {
		deployment, err := post(map[string]interface{}{"framework": framework}, true)
		if err == nil {
			return deployment, framework, nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.mentionsSettings() {
			return Deployment{}, "", err
		}
		// Preset rejected: fall through to Vercel's own detection.
	}
	deployment, err := post(nil, false)
	if err == nil {
		return deployment, "", nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return Deployment{}, "", err
	}
	settings, detected := apiErr.detectedSettings()
	if settings == nil && apiErr.Code != "missing_project_settings" && !apiErr.mentionsSettings() {
		return Deployment{}, "", err
	}
	deployment, err = post(settings, true)
	if err != nil {
		return Deployment{}, "", err
	}
	return deployment, detected, nil
}

// CreateGitDeployment deploys a commit of the connected GitHub repo to
// production, exactly like a push-triggered build.
func (c *Client) CreateGitDeployment(project, org, repo, ref, sha, framework string) (Deployment, error) {
	source := map[string]interface{}{"type": "github", "org": org, "repo": repo, "ref": ref}
	if sha != "" {
		source["sha"] = sha
	}
	body := map[string]interface{}{"name": project, "target": "production", "gitSource": source}
	if framework != "" {
		body["projectSettings"] = map[string]interface{}{"framework": framework}
	}
	return c.createDeployment(body, true)
}

// Project is the part of a Vercel project DevControl needs.
type Project struct {
	ID        string
	Framework string
	LinkType  string
	LinkOrg   string
	LinkRepo  string
}

// LinkedTo reports whether the project is connected to github.com/owner/repo.
func (p Project) LinkedTo(owner, repo string) bool {
	if !strings.EqualFold(p.LinkType, "github") {
		return false
	}
	if strings.Contains(p.LinkRepo, "/") {
		return strings.EqualFold(p.LinkRepo, owner+"/"+repo)
	}
	return strings.EqualFold(p.LinkOrg, owner) && strings.EqualFold(p.LinkRepo, repo)
}

type projectResponse struct {
	ID        string  `json:"id"`
	Framework *string `json:"framework"`
	Link      *struct {
		Type string `json:"type"`
		Org  string `json:"org"`
		Repo string `json:"repo"`
	} `json:"link"`
}

func (r projectResponse) project() Project {
	out := Project{ID: r.ID}
	if r.Framework != nil {
		out.Framework = *r.Framework
	}
	if r.Link != nil {
		out.LinkType, out.LinkOrg, out.LinkRepo = r.Link.Type, r.Link.Org, r.Link.Repo
	}
	return out
}

// GetProject returns found=false (and no error) when the project does not exist.
func (c *Client) GetProject(nameOrID string) (Project, bool, error) {
	var out projectResponse
	err := c.Do(http.MethodGet, "/v9/projects/"+url.PathEscape(nameOrID), nil, nil, &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, err
	}
	if out.ID == "" {
		return Project{}, false, fmt.Errorf("Vercel tidak mengembalikan ID project")
	}
	return out.project(), true, nil
}

// CreateProject creates a project with the framework preset ("" = none) and,
// when gitRepo ("owner/repo") is set, connected to that GitHub repo.
func (c *Client) CreateProject(name, framework, gitRepo string) (Project, error) {
	body := map[string]interface{}{"name": name}
	if framework != "" {
		body["framework"] = framework
	} else {
		body["framework"] = nil
	}
	if gitRepo != "" {
		body["gitRepository"] = map[string]string{"type": "github", "repo": gitRepo}
	}
	var out projectResponse
	if err := c.Do(http.MethodPost, "/v11/projects", nil, body, &out); err != nil {
		return Project{}, err
	}
	if out.ID == "" {
		return Project{}, fmt.Errorf("Vercel tidak mengembalikan ID project")
	}
	return out.project(), nil
}

// LinkProject connects an existing project to a GitHub repo ("owner/repo").
func (c *Client) LinkProject(projectID, gitRepo string) error {
	return c.Do(http.MethodPost, "/v9/projects/"+url.PathEscape(projectID)+"/link", nil,
		map[string]string{"type": "github", "repo": gitRepo}, nil)
}

// SetFramework stores the framework preset on the project.
func (c *Client) SetFramework(projectID, framework string) error {
	return c.Do(http.MethodPatch, "/v9/projects/"+url.PathEscape(projectID), nil,
		map[string]interface{}{"framework": framework}, nil)
}

// DisableProtection turns off Vercel Authentication, used only on the
// throwaway test project so its preview can be checked anonymously.
func (c *Client) DisableProtection(projectID string) error {
	return c.Do(http.MethodPatch, "/v9/projects/"+url.PathEscape(projectID), nil,
		map[string]interface{}{"ssoProtection": nil}, nil)
}

func parseTargets(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var single string
	if json.Unmarshal(raw, &single) == nil && single != "" {
		return []string{single}
	}
	return nil
}

type newEnv struct {
	Key    string   `json:"key"`
	Value  string   `json:"value"`
	Type   string   `json:"type"`
	Target []string `json:"target"`
}

// AddMissingEnv copies the ZIP's variables into the project for production
// and preview. Variables already set in Vercel are never overwritten, the
// same precedence a platform variable has over a .env file at build time.
func (c *Client) AddMissingEnv(projectID string, vars []EnvVar) ([]string, []string, error) {
	var existing struct {
		Envs []struct {
			Key    string          `json:"key"`
			Target json.RawMessage `json:"target"`
		} `json:"envs"`
	}
	endpoint := "/v10/projects/" + url.PathEscape(projectID) + "/env"
	if err := c.Do(http.MethodGet, endpoint, nil, nil, &existing); err != nil {
		return nil, nil, err
	}
	have := map[string]map[string]bool{}
	for _, env := range existing.Envs {
		if have[env.Key] == nil {
			have[env.Key] = map[string]bool{}
		}
		for _, target := range parseTargets(env.Target) {
			have[env.Key][target] = true
		}
	}
	batch := make([]newEnv, 0, len(vars))
	for _, env := range vars {
		if env.Value == "" {
			continue
		}
		missing := []string{}
		for _, target := range []string{"production", "preview"} {
			if !have[env.Key][target] {
				missing = append(missing, target)
			}
		}
		if len(missing) > 0 {
			batch = append(batch, newEnv{Key: env.Key, Value: env.Value, Type: "encrypted", Target: missing})
		}
	}
	if len(batch) == 0 {
		return nil, nil, nil
	}
	added, failed := []string{}, []string{}
	var result struct {
		Failed []struct {
			Error struct {
				Key string `json:"key"`
			} `json:"error"`
		} `json:"failed"`
	}
	if err := c.Do(http.MethodPost, endpoint, nil, batch, &result); err != nil {
		// One name Vercel reserves must not block the others.
		for _, env := range batch {
			if c.Do(http.MethodPost, endpoint, nil, env, nil) != nil {
				failed = append(failed, env.Key)
			} else {
				added = append(added, env.Key)
			}
		}
		if len(added) == 0 {
			return nil, failed, err
		}
		return added, failed, nil
	}
	rejected := map[string]bool{}
	for _, item := range result.Failed {
		if item.Error.Key != "" {
			rejected[item.Error.Key] = true
			failed = append(failed, item.Error.Key)
		}
	}
	for _, env := range batch {
		if !rejected[env.Key] {
			added = append(added, env.Key)
		}
	}
	if len(added) == 0 && len(failed) > 0 {
		return nil, failed, fmt.Errorf("Vercel menolak semua variabel .env")
	}
	return added, failed, nil
}

// MissingHomePage reports true only when "/" is answered by Vercel itself
// with 404 NOT_FOUND (no build output where Vercel serves from). It asks up
// to three times to ride out alias propagation and the few seconds it takes
// for a lifted login wall to apply. Any real answer from the app, network
// errors and a login wall that stays up are inconclusive and report false.
func MissingHomePage(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	target := strings.TrimRight(rawURL, "/") + "/"
	notFound := 0
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		resp, err := client.Get(target)
		if err != nil {
			return false
		}
		platformError := strings.ToUpper(strings.TrimSpace(resp.Header.Get("X-Vercel-Error")))
		location := strings.ToLower(resp.Header.Get("Location"))
		status := resp.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		switch {
		case status == http.StatusNotFound && platformError == "NOT_FOUND":
			notFound++
		case status == http.StatusUnauthorized || status == http.StatusForbidden ||
			(status >= 300 && status < 400 && strings.Contains(location, "vercel.com/sso")):
			// Deployment protection still active: ask again.
		default:
			return false
		}
	}
	return notFound >= 2
}

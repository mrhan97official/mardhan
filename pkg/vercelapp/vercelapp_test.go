package vercelapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func pkg(body string) File { return File{Path: "package.json", Data: []byte(body)} }

func TestDetectFramework(t *testing.T) {
	cases := map[string]string{
		`{"dependencies":{"react":"18"},"devDependencies":{"vite":"5"}}`:          "vite",
		`{"dependencies":{"next":"14.2.5","react":"18"}}`:                        "nextjs",
		`{"devDependencies":{"@sveltejs/kit":"2","vite":"5"}}`:                   "sveltekit-1",
		`{"dependencies":{"react-scripts":"5"}}`:                                 "create-react-app",
		`{"dependencies":{"express":"4"}}`:                                       "",
		`not json`:                                                               "",
	}
	for body, want := range cases {
		if got := DetectFramework([]File{pkg(body)}); got != want {
			t.Errorf("DetectFramework(%s) = %q, want %q", body, got, want)
		}
	}
	if got := DetectFramework([]File{{Path: "index.html", Data: []byte("<h1>hi</h1>")}}); got != "" {
		t.Errorf("static site framework = %q, want empty", got)
	}
}

func TestWebRootMonorepoApotikPintar(t *testing.T) {
	files := []File{
		{Path: "server/api/index.go", Data: []byte("package handler")},
		{Path: "server/go.mod", Data: []byte("module apotik")},
		{Path: "web/package.json", Data: []byte(`{"scripts":{"build":"next build"},"dependencies":{"next":"15.0.0"}}`)},
		{Path: "web/app/page.js", Data: []byte("export default function Home() {}")},
		{Path: "web/.env.production", Data: []byte("API_URL=https://api.vercel.app")},
	}
	root, err := WebRoot(files)
	if err != nil || root != "web" { t.Fatalf("WebRoot = %q, %v; want web", root, err) }
	selected := FilesAtRoot(files, root)
	if DetectFramework(selected) != "nextjs" || !ExpectsHomePage(selected) { t.Fatalf("web root was not recognized: %v", selected) }
	if got := EnvFromFiles(selected); len(got) != 1 || got[0].Key != "API_URL" { t.Fatalf("web env not found: %v", got) }
	if _, err := WebRoot(append(files, File{Path: "admin/package.json", Data: []byte(`{"dependencies":{"next":"15"}}`)})); err == nil {
		t.Fatal("two web frontends need an explicit choice")
	}
}

func TestIsPlatform404(t *testing.T) {
	if !IsPlatform404(404, "NOT_FOUND", "") || !IsPlatform404(404, "", "This page doesn’t exist. 404 NOT_FOUND") {
		t.Fatal("Vercel platform 404 was missed")
	}
	if IsPlatform404(404, "", "My application says 404") || IsPlatform404(500, "NOT_FOUND", "") || IsPlatform404(200, "", "This page doesn’t exist. 404 NOT_FOUND") {
		t.Fatal("non-platform page reported broken")
	}
	if broken, err := Platform404("https://127.0.0.1/"); err == nil || broken { t.Fatalf("local probe allowed: %v, %v", broken, err) }
}

func TestFileDeploymentSetsMonorepoRoot(t *testing.T) {
	client := New("test-token")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct { ProjectSettings struct { Framework string `json:"framework"`; RootDirectory string `json:"rootDirectory"` } `json:"projectSettings"` }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
		if body.ProjectSettings.Framework != "nextjs" || body.ProjectSettings.RootDirectory != "web" { t.Fatalf("wrong project settings: %+v", body.ProjectSettings) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"dpl_web","url":"site.vercel.app"}`)), Header: make(http.Header)}, nil
	})}
	if _, _, err := client.CreateFileDeployment("site", "", []File{{Path:"web/package.json", Data:[]byte(`{}`)}}, "nextjs", "web"); err != nil { t.Fatal(err) }
}

func TestLatestProductionScopedToProject(t *testing.T) {
	client := New("test-token")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v7/deployments" || r.URL.Query().Get("projectId") != "prj_web" || r.URL.Query().Get("target") != "production" || r.URL.Query().Get("sha") != "commit123" { t.Fatalf("unexpected query: %s", r.URL) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"deployments":[{"uid":"dpl_healthy","target":"production"}]}`)), Header: make(http.Header)}, nil
	})}
	if id, err := client.LatestProduction("prj_web", "commit123"); err != nil || id != "dpl_healthy" { t.Fatalf("id=%s, err=%v", id, err) }
}

func TestWorkingManualProjectSettingsArePreserved(t *testing.T) {
	client := New("test-token")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("manual project with matching root and preset must not be patched")
		return nil, nil
	})}
	fields, err := client.AlignBuildSettings(Project{ID:"prj_web", Framework:"nextjs", RootDirectory:"web", BuildCommand:"pnpm build"}, "nextjs", "web")
	if err != nil || len(fields) != 0 { t.Fatalf("manual settings changed: %v, %v", fields, err) }
}

func TestProjectIDForURL(t *testing.T) {
	client := New("test-token")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v13/deployments/old.vercel.app" { t.Fatalf("unexpected URL: %s", r.URL) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"dpl_old","projectId":"prj_old"}`)), Header: make(http.Header)}, nil
	})}
	if id, err := client.ProjectIDForURL("https://old.vercel.app/"); err != nil || id != "prj_old" { t.Fatalf("project=%s, err=%v", id, err) }
}

func TestExpectsHomePage(t *testing.T) {
	if !ExpectsHomePage([]File{pkg(`{"devDependencies":{"vite":"5"}}`)}) {
		t.Error("vite app must expect a home page")
	}
	if !ExpectsHomePage([]File{{Path: "index.html", Data: []byte("x")}}) {
		t.Error("static site must expect a home page")
	}
	if ExpectsHomePage([]File{{Path: "api/index.go", Data: []byte("package handler")}, {Path: "go.mod", Data: []byte("module x")}}) {
		t.Error("API-only project must not expect a home page")
	}
	if ExpectsHomePage([]File{pkg(`{"dependencies":{"express":"4"}}`)}) {
		t.Error("package.json without build script or framework must not expect a home page")
	}
}

func TestEnvFromFiles(t *testing.T) {
	files := []File{
		{Path: ".env", Data: []byte("# comment\nexport A=1\nB=\"two words\"\nC='x' \nD=plain # note\nKEY=\"line1\nline2\"\n1BAD=x\n")},
		{Path: ".env.local", Data: []byte("A=override\r\n")},
		{Path: "sub/.env", Data: []byte("IGNORED=1")},
	}
	got := map[string]string{}
	for _, env := range EnvFromFiles(files) {
		got[env.Key] = env.Value
	}
	want := map[string]string{"A": "override", "B": "two words", "C": "x", "D": "plain", "KEY": "line1\nline2"}
	if len(got) != len(want) {
		t.Fatalf("EnvFromFiles = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
}

func TestLinkedTo(t *testing.T) {
	p := Project{LinkType: "github", LinkOrg: "MrHan97Official", LinkRepo: "test1"}
	if !p.LinkedTo("mrhan97official", "test1") || p.LinkedTo("mrhan97official", "other") {
		t.Error("LinkedTo must compare owner and repo case-insensitively")
	}
	if (Project{}).LinkedTo("a", "b") {
		t.Error("unlinked project reported as linked")
	}
}

func TestDetectedSettings(t *testing.T) {
	e := &APIError{Code: "missing_project_settings", Framework: []byte(`{"name":"Vite","slug":"vite"}`),
		ProjectSettings: []byte(`{"framework":null,"outputDirectory":null,"nodeVersion":"20.x"}`)}
	settings, framework := e.detectedSettings()
	if framework != "vite" || settings["framework"] != "vite" {
		t.Fatalf("detectedSettings = %v, %q", settings, framework)
	}
	if _, found := settings["nodeVersion"]; found {
		t.Error("fields the deployment API rejects must not be copied")
	}
}

func TestCheckHomePage(t *testing.T) {
	cases := []struct {
		name string
		status int
		header string
		location string
		wantError string
	}{
		{name: "home page", status: http.StatusOK},
		{name: "app login redirect", status: http.StatusFound, location: "/login"},
		{name: "platform 404", status: http.StatusNotFound, header: "NOT_FOUND", wantError: "404"},
		{name: "app 404", status: http.StatusNotFound, wantError: "404"},
		{name: "runtime error", status: http.StatusInternalServerError, wantError: "500"},
		{name: "deployment protection", status: http.StatusFound, location: "https://vercel.com/sso", wantError: "Deployment Protection"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" && tc.name == "app login redirect" { w.WriteHeader(http.StatusOK); return }
				if r.URL.Path != "/" { t.Errorf("requested %s", r.URL.Path) }
				if tc.header != "" { w.Header().Set("X-Vercel-Error", tc.header) }
				if tc.location != "" { w.Header().Set("Location", tc.location) }
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			err := CheckHomePage(server.URL)
			if tc.wantError == "" && err != nil { t.Fatalf("unexpected probe error: %v", err) }
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("probe error = %v, want %q", err, tc.wantError)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" { http.Redirect(w, r, "/missing", http.StatusFound); return }
		http.NotFound(w, r)
	}))
	defer server.Close()
	if err := CheckHomePage(server.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("redirect to a missing page must fail verification, got %v", err)
	}
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer loop.Close()
	if err := CheckHomePage(loop.URL); err == nil || !strings.Contains(err.Error(), "pengalihan") {
		t.Fatalf("redirect loop must fail verification, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)
func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitDeploymentKeepsProjectBuildSettings(t *testing.T) {
	client := New("test-token")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("skipAutoDetectionConfirmation") != "" {
			t.Fatal("Git deployment must keep Vercel framework confirmation enabled")
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
		if _, override := body["projectSettings"]; override {
			t.Fatal("Git deployment must use existing project build/output settings")
		}
		if body["target"] != "production" || body["gitSource"] == nil || body["project"] != "prj_manual" { t.Fatalf("unexpected deployment body: %v", body) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"dpl_123","url":"app.vercel.app"}`)), Header: make(http.Header)}, nil
	})}
	if _, err := client.CreateGitDeployment("project", "prj_manual", "owner", "repo", "main", "sha"); err != nil { t.Fatal(err) }
}

func TestFindLinkedProjectsUsesExactRepo(t *testing.T) {
	client := New("test-token")
	calls := 0
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v10/projects" || r.URL.Query().Get("repo") != "owner/site" { t.Fatalf("unexpected request: %s", r.URL) }
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"projects":[{"id":"wrong","name":"other","link":{"type":"github","org":"owner","repo":"other"}},{"id":"manual","name":"site","link":{"type":"github","org":"owner","repo":"site"}}],"pagination":{"next":"cursor"}}`)), Header: make(http.Header)}, nil
		}
		if r.URL.Query().Get("from") != "cursor" { t.Fatalf("missing cursor: %s", r.URL) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"projects":[],"pagination":{}}`)), Header: make(http.Header)}, nil
	})}
	projects, err := client.FindLinkedProjects("owner", "site")
	if err != nil { t.Fatal(err) }
	if calls != 2 || len(projects) != 1 || projects[0].ID != "manual" || projects[0].Name != "site" {
		t.Fatalf("FindLinkedProjects = %+v, calls = %d", projects, calls)
	}
}

func TestFindLinkedProjectsAcceptsArrayResponse(t *testing.T) {
	client := New("test-token")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"id":"manual","name":"site","link":{"type":"github","repo":"owner/site"}}]`)), Header: make(http.Header)}, nil
	})}
	projects, err := client.FindLinkedProjects("owner", "site")
	if err != nil || len(projects) != 1 || projects[0].ID != "manual" { t.Fatalf("projects=%v, err=%v", projects, err) }
}

func TestDeploymentIDForURL(t *testing.T) {
	client := New("test-token")
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v13/deployments/site.vercel.app" { t.Fatalf("unexpected path: %s", r.URL.Path) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"dpl_current"}`)), Header: make(http.Header)}, nil
	})}
	id, err := client.DeploymentIDForURL("https://site.vercel.app/")
	if err != nil || id != "dpl_current" { t.Fatalf("id=%q, err=%v", id, err) }
}

func TestAlignBuildSettings(t *testing.T) {
	client := New("test-token")
	patches := 0
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v9/projects/prj_old" { t.Fatalf("unexpected request: %s %s", r.Method, r.URL) }
		patches++
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
		want := map[string]interface{}{"framework": "vite", "rootDirectory": nil, "outputDirectory": nil, "buildCommand": nil, "installCommand": nil}
		if !reflect.DeepEqual(body, want) { t.Fatalf("PATCH body = %v, want %v", body, want) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	project := Project{ID: "prj_old", Framework: "", RootDirectory: "web", OutputDirectory: "public", BuildCommand: "echo old", InstallCommand: "npm ci"}
	fields, err := client.AlignBuildSettings(project, "vite", "")
	if err != nil { t.Fatal(err) }
	if patches != 1 || !reflect.DeepEqual(fields, []string{"buildCommand", "framework", "installCommand", "outputDirectory", "rootDirectory"}) {
		t.Fatalf("changes = %v, patches = %d", fields, patches)
	}
	fields, err = client.AlignBuildSettings(Project{ID: "prj_old", Framework: "vite"}, "vite", "")
	if err != nil || len(fields) != 0 || patches != 1 { t.Fatalf("already aligned: fields=%v, err=%v, patches=%d", fields, err, patches) }
	fields, err = client.AlignBuildSettings(Project{ID: "prj_old", Framework: "nextjs"}, "", "")
	if err != nil || len(fields) != 0 || patches != 1 { t.Fatalf("unknown framework should preserve preset: fields=%v, err=%v, patches=%d", fields, err, patches) }
}

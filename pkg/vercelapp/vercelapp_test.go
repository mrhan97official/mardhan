package vercelapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Fatal(err) }
		if _, override := body["projectSettings"]; override {
			t.Fatal("Git deployment must use existing project build/output settings")
		}
		if body["target"] != "production" || body["gitSource"] == nil { t.Fatalf("unexpected deployment body: %v", body) }
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"dpl_123","url":"app.vercel.app"}`)), Header: make(http.Header)}, nil
	})}
	if _, err := client.CreateGitDeployment("project", "owner", "repo", "main", "sha"); err != nil { t.Fatal(err) }
}

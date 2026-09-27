package vercelapp

import "testing"

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

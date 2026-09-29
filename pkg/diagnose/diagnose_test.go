package diagnose

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil { t.Fatal(err) }
		if _, err := file.Write([]byte(content)); err != nil { t.Fatal(err) }
	}
	if err := writer.Close(); err != nil { t.Fatal(err) }
	return buffer.Bytes()
}

// Each case is a message DevControl really produced (or Vercel really
// printed); the point is that the person is told where to act.
func TestAnalyzeSource(t *testing.T) {
	cases := []struct {
		name       string
		in         Input
		source     Source
		category   string
		logMissing bool
		retryable  bool
	}{
		{
			name: "deployment protection on the test project (gjctyyfxdt)",
			in: Input{Kind: "new_app", Target: "gjctyyfxdt", Stage: "Uji build Vercel",
				Message: "[Uji Build Vercel] Build Vercel READY, tetapi halaman utama https://gjctyyfxdt-test-1790645747732717761-12alpjnw4-adu-colective.vercel.app gagal dibuka: HTTP 302: akses deployment dibatasi; periksa Deployment Protection. Periksa Output dan Runtime Logs deployment, framework, Root Directory, Output Directory, serta Deployment Protection di Vercel. Tidak ada yang didorong ke GitHub dan production tidak berubah."},
			source: SourcePlatform, category: "Deployment Protection Vercel", retryable: true,
		},
		{
			name: "build log not stored yet (kasir-f-b)",
			in: Input{Kind: "new_app", Target: "kasir-f-b", Stage: "Uji build Vercel",
				Message: "[Uji Build Vercel] Build Vercel ERROR. Log belum dapat dibaca (log build kosong); periksa project uji kasir-f-b-selfupdate-test-1790642983238769296 di Vercel."},
			source: SourceUnknown, category: "Log build Vercel belum tersedia", logMissing: true, retryable: true,
		},
		{
			name: "home page shows Vercel 404 is not mistaken for protection",
			in: Input{Kind: "new_app", Stage: "Uji build Vercel",
				Message: "[Uji Build Vercel] Build Vercel READY, tetapi halaman utama menampilkan 404 NOT_FOUND. Periksa Output dan Runtime Logs deployment, framework, Root Directory, Output Directory, serta Deployment Protection di Vercel."},
			source: SourceConfig, category: "Hasil build tidak disajikan (404 NOT_FOUND)",
		},
		{
			name: "Go compile error in the uploaded code",
			in: Input{Kind: "update_app", Stage: "Uji build Vercel", Message: "[Uji Build Vercel] Build Vercel ERROR",
				BuildLog: "Running \"go build\"\napi/index.go:3:2: undefined: tidakAda\nError: Command failed"},
			source: SourceCode, category: "Error kompilasi Go — nama tidak dikenal",
		},
		{
			name: "missing npm package",
			in: Input{Kind: "new_app", Stage: "Uji build Vercel", Message: "[Uji Build Vercel] Build Vercel ERROR",
				BuildLog: "Module not found: Can't resolve 'framer-motion'\n> Build failed because of webpack errors"},
			source: SourceCode, category: "Paket npm belum terpasang",
		},
		{
			name: "bad GitHub token",
			in: Input{Kind: "new_app", Stage: "Dorong ke GitHub", Message: "[GitHub] Gagal membaca akun GitHub: HTTP 401: Bad credentials"},
			source: SourcePlatform, category: "Izin GITHUB_TOKEN",
		},
		{
			name: "temporary outage",
			in: Input{Kind: "new_app", Stage: "Dorong ke GitHub", Message: "[GitHub] HTTP 503 dari GitHub"},
			source: SourcePlatform, category: "Gangguan sementara layanan", retryable: true,
		},
		{
			name: "missing environment variable is the app's Vercel settings",
			in: Input{Kind: "new_app", Stage: "Uji build Vercel", Message: "[Uji Build Vercel] Build Vercel ERROR",
				BuildLog: "Error: Environment variable DATABASE_URL is not set"},
			source: SourceConfig, category: "Environment variable kosong",
		},
		{
			name: "unknown wording but the project's own build command failed",
			in: Input{Kind: "new_app", Stage: "Uji build Vercel", Message: "[Uji Build Vercel] Build Vercel ERROR",
				BuildLog: "Running \"npm run build\"\nsesuatu yang tidak biasa terjadi\nCommand \"npm run build\" exited with 1"},
			source: SourceCode, category: "Error build aplikasi (pola belum dikenali)",
		},
		{
			name: "unknown wording while pushing to GitHub is DevControl's step",
			in: Input{Kind: "new_app", Stage: "Dorong ke GitHub", Message: "[GitHub] respons tak terduga dari server"},
			source: SourcePlatform, category: "Error layanan DevControl (pola belum dikenali)",
		},
		{
			name: "DevControl's own storage message",
			in: Input{Kind: "new_app", Stage: "Simpan & ekstrak ZIP", Message: "penyiapan D1/R2 gagal: bucket tidak ditemukan"},
			source: SourcePlatform, category: "Error layanan DevControl (pola belum dikenali)",
		},
		{
			name: "no evidence at all stays honestly unknown",
			in:   Input{Kind: "new_app", Message: "sesuatu yang aneh"},
			source: SourceUnknown, category: "Error belum dikenali otomatis",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(tc.in)
			if got.Source != tc.source { t.Errorf("source = %q, want %q (category %q)", got.Source, tc.source, got.Category) }
			if got.Category != tc.category { t.Errorf("category = %q, want %q", got.Category, tc.category) }
			if got.LogMissing != tc.logMissing { t.Errorf("log_missing = %v, want %v", got.LogMissing, tc.logMissing) }
			if got.Retryable != tc.retryable { t.Errorf("retryable = %v, want %v", got.Retryable, tc.retryable) }
			if len(got.Fixes) == 0 { t.Error("no fixes suggested") }
			if !strings.HasPrefix(got.Prompt, FixDirectiveMarker) { t.Errorf("prompt does not start with the fix directive:\n%s", got.Prompt) }
		})
	}
}

// The AI prompt must send the fix to the right place: never ask for a ZIP
// patch when DevControl is at fault, and never blame DevControl for a code
// error in the ZIP.
func TestPromptTellsWhereToFix(t *testing.T) {
	platform := Analyze(Input{Kind: "new_app", Target: "gjctyyfxdt", Stage: "Uji build Vercel",
		Message: "[Uji Build Vercel] Build Vercel READY, tetapi halaman utama https://x.vercel.app gagal dibuka: HTTP 302: akses deployment dibatasi; periksa Deployment Protection."})
	for _, want := range []string{"DEVCONTROL, BUKAN ZIP", "Jangan mengubah file apa pun di ZIP"} {
		if !strings.Contains(platform.Prompt, want) { t.Errorf("platform prompt misses %q:\n%s", want, platform.Prompt) }
	}
	if strings.Contains(platform.Prompt, "patch-only) pada kode di ZIP") { t.Error("platform prompt still asks to patch the ZIP") }

	code := Analyze(Input{Kind: "new_app", Target: "kasir", Stage: "Uji build Vercel", Message: "[Uji Build Vercel] Build Vercel ERROR",
		BuildLog: "api/index.go:3:2: undefined: tidakAda"})
	for _, want := range []string{"ZIP APLIKASI", "DevControl tidak perlu diubah", "patch-only) pada kode di ZIP"} {
		if !strings.Contains(code.Prompt, want) { t.Errorf("code prompt misses %q:\n%s", want, code.Prompt) }
	}

	self := Analyze(Input{Kind: "self_update", Target: "owner/devcontrol", Stage: "Uji build Vercel", Message: "[Uji Build Vercel] Build Vercel ERROR",
		BuildLog: "api/gateway.go:10:2: undefined: rollback"})
	if !strings.Contains(self.Prompt, "KODE DEVCONTROL di ZIP Update Diri") { t.Errorf("self-update prompt should point at DevControl's own ZIP:\n%s", self.Prompt) }

	missing := Analyze(Input{Kind: "new_app", Stage: "Uji build Vercel",
		Message: "[Uji Build Vercel] Build Vercel ERROR. Log belum dapat dibaca (log build kosong); periksa project uji x di Vercel."})
	if !strings.Contains(missing.Prompt, "BELUM PASTI") || strings.Contains(missing.Prompt, "patch-only) pada kode di ZIP") {
		t.Errorf("missing-log prompt must not claim a place to fix:\n%s", missing.Prompt)
	}
}

func TestAnalyzeFindsLocationAndSnippetInZip(t *testing.T) {
	archive := zipOf(t, map[string]string{
		"go.mod":       "module kasir\n\ngo 1.21\n",
		"api/index.go": "package handler\n\nfunc Handler() { tidakAda() }\n",
	})
	got := Analyze(Input{Kind: "new_app", Stage: "Uji build Vercel", Message: "[Uji Build Vercel] Build Vercel ERROR",
		BuildLog: "api/index.go:3:19: undefined: tidakAda", Zip: archive})
	if got.Location == nil || got.Location.File != "api/index.go" || got.Location.Line != 3 || got.Location.Column != 19 {
		t.Fatalf("location = %+v, want api/index.go:3:19", got.Location)
	}
	if !strings.Contains(got.Snippet, "tidakAda") { t.Errorf("snippet does not show the failing line:\n%s", got.Snippet) }
	if !strings.Contains(got.Stack, "Go") { t.Errorf("stack = %q, want it to mention Go", got.Stack) }
	if got.Source != SourceCode { t.Errorf("source = %q, want code", got.Source) }
}

func TestAnalyzeNeverFailsOnEmptyOrBrokenInput(t *testing.T) {
	for _, in := range []Input{{}, {Message: "x", Zip: []byte("bukan zip")}, {BuildLog: "\x1b[31merror\x1b[0m"}} {
		got := Analyze(in)
		if got.Category == "" || got.Source == "" || got.Prompt == "" {
			t.Fatalf("Analyze(%+v) left fields empty: %+v", in, got)
		}
	}
}

// Diagnoses stored before sources existed must still show a source.
func TestReclassifyStoredDiagnoses(t *testing.T) {
	old := Diagnosis{Stage: "Uji build Vercel", Category: "Error kompilasi Go — nama ganda", Summary: "Handler redeclared in this block"}
	Reclassify(&old)
	if old.Source != SourceCode { t.Errorf("known category: source = %q, want code", old.Source) }

	token := Diagnosis{Stage: "Dorong ke GitHub", Category: "Izin GITHUB_TOKEN"}
	Reclassify(&token)
	if token.Source != SourcePlatform { t.Errorf("token category: source = %q, want platform", token.Source) }

	missing := Diagnosis{Stage: "Uji build Vercel", Category: "Error belum dikenali otomatis",
		Summary: "[Uji Build Vercel] Build Vercel ERROR. Log belum dapat dibaca (log build kosong); periksa project uji x di Vercel."}
	Reclassify(&missing)
	if !missing.LogMissing || missing.Source != SourceUnknown {
		t.Errorf("missing log: log_missing = %v, source = %q; want true, unknown", missing.LogMissing, missing.Source)
	}

	kept := Diagnosis{Source: SourceConfig, Category: "Error kompilasi Go — nama ganda"}
	Reclassify(&kept)
	if kept.Source != SourceConfig { t.Errorf("an existing source was overwritten: %q", kept.Source) }

	Reclassify(nil) // must not panic
}

// Every rule must say where to act, or the badge falls back to "unknown".
func TestEveryRuleHasASource(t *testing.T) {
	for _, candidate := range rules {
		if candidate.source == "" { t.Errorf("rule %q has no source", candidate.category) }
		if candidate.source == SourceUnknown && candidate.category != "Log build Vercel belum tersedia" {
			t.Errorf("rule %q is marked unknown; only the missing-log rule may be", candidate.category)
		}
		if len(candidate.fixes) == 0 { t.Errorf("rule %q has no fixes", candidate.category) }
	}
}

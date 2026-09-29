// Package appaudit runs passive security checks against the applications
// DevControl manages (and DevControl itself), rates every finding
// Tinggi/Sedang/Rendah, and keeps a short history per application.
//
// Only applications registered in DevControl can be audited (their URL and
// repo come from D1, never from the request), and every check is passive:
// ordinary GET requests and read-only GitHub/Vercel API calls. There is no
// brute force, fuzzing or exploitation.
package appaudit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/util"
	"devcontrol/pkg/vercelapp"
	"devcontrol/pkg/webpush"
)

// SelfKey identifies DevControl itself in the audit list.
const SelfKey = "devcontrol-self"

const (
	userAgent   = "DevControl-Audit/1.0 (+passive security check)"
	probeOrigin = "https://audit-probe.devcontrol.invalid"
	keepPerApp  = 30
)

type Target struct {
	Key    string `json:"app"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Repo   string `json:"repo"`
	Branch string `json:"branch,omitempty"`
	Self   bool   `json:"self"`
}

type Finding struct {
	ID       string `json:"id"`
	Stage    int    `json:"stage"`    // 1 web & konfigurasi, 2 kode & dependensi, 0 DevControl
	Category string `json:"category"` // Web, Vercel, GitHub, Kode, Dependensi, DevControl
	Severity string `json:"severity"` // high | medium | low
	Title    string `json:"title"`
	Location string `json:"location"`
	Detail   string `json:"detail"`
	Advice   string `json:"advice"`
}

type Report struct {
	App        string    `json:"app"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Repo       string    `json:"repo"`
	Score      int       `json:"score"`
	Findings   []Finding `json:"findings"`
	Passed     []string  `json:"passed"`
	Skipped    []string  `json:"skipped"`
	CreatedAt  string    `json:"created_at"`
	Source     string    `json:"source"`
	DurationMS int64     `json:"duration_ms"`
}

type collector struct {
	mu     sync.Mutex
	report *Report
}

func (c *collector) add(f Finding) { c.mu.Lock(); c.report.Findings = append(c.report.Findings, f); c.mu.Unlock() }
func (c *collector) pass(text string) { c.mu.Lock(); c.report.Passed = append(c.report.Passed, text); c.mu.Unlock() }
func (c *collector) skip(text string) { c.mu.Lock(); c.report.Skipped = append(c.report.Skipped, text); c.mu.Unlock() }

var severityRank = map[string]int{"high": 0, "medium": 1, "low": 2}

// Score starts at 100: each Tinggi costs 20, Sedang 8, Rendah 3.
func score(findings []Finding) int {
	value := 100
	for _, f := range findings {
		switch f.Severity {
		case "high": value -= 20
		case "medium": value -= 8
		case "low": value -= 3
		}
	}
	if value < 0 { value = 0 }
	return value
}

func counts(findings []Finding) (high, medium, low int) {
	for _, f := range findings {
		switch f.Severity {
		case "high": high++
		case "medium": medium++
		case "low": low++
		}
	}
	return
}

// Run audits one target. Checks run in parallel; a check that cannot run
// is listed under Skipped with its reason instead of failing the audit.
func Run(ctx context.Context, target Target, source string) Report {
	started := time.Now()
	c := &collector{report: &Report{App: target.Key, Name: target.Name, URL: target.URL, Repo: target.Repo,
		Findings: []Finding{}, Passed: []string{}, Skipped: []string{}, Source: source}}
	var wait sync.WaitGroup
	run := func(check func()) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() {
				if recovered := recover(); recovered != nil { c.skip(fmt.Sprintf("Satu pemeriksaan berhenti tak terduga: %v", recovered)) }
			}()
			check()
		}()
	}
	run(func() { checkWeb(ctx, c, target) })
	run(func() { checkExposedFiles(ctx, c, target) })
	if owner, repo, ok := splitRepo(target.Repo); ok {
		run(func() { checkVercel(c, owner, repo) })
		run(func() { checkGitHub(ctx, c, owner, repo, target.Branch) })
	} else {
		c.skip("Pemeriksaan Vercel, GitHub, kode dan dependensi dilewati: repo aplikasi belum tercatat.")
	}
	if target.Self { run(func() { checkSelf(ctx, c) }) }
	wait.Wait()

	report := *c.report
	sort.SliceStable(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if severityRank[a.Severity] != severityRank[b.Severity] { return severityRank[a.Severity] < severityRank[b.Severity] }
		return a.Category < b.Category
	})
	sort.Strings(report.Passed)
	report.Score = score(report.Findings)
	report.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	report.DurationMS = time.Since(started).Milliseconds()
	return report
}

func splitRepo(repo string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" { return "", "", false }
	return parts[0], parts[1], true
}

func shortError(err error) string {
	text := err.Error()
	if len(text) > 160 { text = text[:160] + "…" }
	return text
}

// ---------- tahap 1: web dari luar ----------

func webClient(follow bool) *http.Client {
	client := &http.Client{Timeout: 12 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !follow || len(via) >= 5 { return http.ErrUseLastResponse }
		return nil
	}
	return client
}

func get(ctx context.Context, client *http.Client, target string, headers map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil { return nil, nil, err }
	req.Header.Set("User-Agent", userAgent)
	for key, value := range headers { req.Header.Set(key, value) }
	resp, err := client.Do(req)
	if err != nil { return nil, nil, err }
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return resp, body, nil
}

func directive(csp, name string) (string, bool) {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		if lower == name || strings.HasPrefix(lower, name+" ") { return strings.TrimSpace(part[len(name):]), true }
	}
	return "", false
}

var maxAgePattern = regexp.MustCompile(`(?i)max-age\s*=\s*"?(\d+)`)
var versionPattern = regexp.MustCompile(`\d+\.\d+`)

func checkWeb(ctx context.Context, c *collector, t Target) {
	parsed, err := url.Parse(strings.TrimSpace(t.URL))
	if err != nil || parsed.Hostname() == "" { c.skip("Pemeriksaan web dilewati: URL aplikasi belum tersimpan."); return }
	if parsed.Scheme != "https" {
		c.add(Finding{ID: "web-no-https", Stage: 1, Category: "Web", Severity: "high", Title: "Alamat aplikasi tersimpan tanpa HTTPS",
			Location: t.URL, Detail: "Data (termasuk kata sandi dan cookie) bisa dibaca atau diubah di jaringan bila halaman dibuka lewat HTTP.",
			Advice: "Simpan dan bagikan alamat aplikasi dengan https://. Di Vercel HTTPS aktif otomatis untuk domain yang terhubung."})
	}
	home := "https://" + parsed.Host + "/"
	resp, _, err := get(ctx, webClient(true), home, map[string]string{"Origin": probeOrigin})
	if err != nil { c.skip("Halaman utama tidak dapat dibuka (" + shortError(err) + "); header keamanan belum diperiksa."); return }
	headers := resp.Header
	where := "Header respons " + home
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.skip(fmt.Sprintf("Halaman utama meminta login (HTTP %d); header yang terlihat tetap diperiksa.", resp.StatusCode))
	}

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		left := time.Until(resp.TLS.PeerCertificates[0].NotAfter)
		days := int(left.Hours() / 24)
		switch {
		case left < 14*24*time.Hour:
			c.add(Finding{ID: "tls-expiring", Stage: 1, Category: "Web", Severity: "high", Title: fmt.Sprintf("Sertifikat SSL habis dalam %d hari", days),
				Location: parsed.Host, Detail: "Setelah sertifikat habis, browser menolak membuka aplikasi.", Advice: "Periksa domain di Vercel → Settings → Domains; sertifikat biasanya diperbarui otomatis bila DNS masih mengarah ke Vercel."})
		case left < 30*24*time.Hour:
			c.add(Finding{ID: "tls-expiring", Stage: 1, Category: "Web", Severity: "medium", Title: fmt.Sprintf("Sertifikat SSL habis dalam %d hari", days),
				Location: parsed.Host, Detail: "Sertifikat mendekati masa berakhir.", Advice: "Pastikan perpanjangan otomatis berjalan (DNS domain masih mengarah ke Vercel)."})
		default:
			c.pass(fmt.Sprintf("Sertifikat SSL masih berlaku %d hari", days))
		}
		if resp.TLS.Version < tls.VersionTLS12 {
			c.add(Finding{ID: "tls-old", Stage: 1, Category: "Web", Severity: "medium", Title: "Koneksi memakai TLS versi lama", Location: parsed.Host,
				Detail: "TLS di bawah 1.2 punya kelemahan yang sudah diketahui.", Advice: "Nonaktifkan TLS 1.0/1.1 di penyedia hosting/CDN."})
		}
	}

	if hsts := headers.Get("Strict-Transport-Security"); hsts == "" {
		c.add(Finding{ID: "hdr-hsts", Stage: 1, Category: "Web", Severity: "medium", Title: "Header HSTS belum ada", Location: where,
			Detail: "Tanpa HSTS, kunjungan pertama lewat http:// bisa dibelokkan sebelum dialihkan ke HTTPS.",
			Advice: "Tambahkan header Strict-Transport-Security: max-age=63072000; includeSubDomains (misalnya di vercel.json → headers)."})
	} else if match := maxAgePattern.FindStringSubmatch(hsts); match == nil || atoi(match[1]) < 15552000 {
		c.add(Finding{ID: "hdr-hsts-short", Stage: 1, Category: "Web", Severity: "low", Title: "Masa berlaku HSTS terlalu pendek", Location: where,
			Detail: "Nilai max-age di bawah 6 bulan.", Advice: "Gunakan max-age minimal 15552000 (6 bulan), idealnya 63072000."})
	} else {
		c.pass("HSTS aktif dengan masa berlaku cukup")
	}

	csp := headers.Get("Content-Security-Policy")
	if csp == "" {
		c.add(Finding{ID: "hdr-csp", Stage: 1, Category: "Web", Severity: "medium", Title: "Content-Security-Policy belum ada", Location: where,
			Detail: "CSP membatasi script yang boleh berjalan, sehingga celah XSS jauh lebih sulit dimanfaatkan.",
			Advice: "Tambahkan header Content-Security-Policy, mulai dari default-src 'self' lalu izinkan hanya domain yang benar-benar dipakai."})
	} else {
		script, ok := directive(csp, "script-src")
		if !ok { script, _ = directive(csp, "default-src") }
		if strings.Contains(script, "'unsafe-inline'") && !strings.Contains(script, "'nonce-") && !strings.Contains(script, "'strict-dynamic'") {
			c.add(Finding{ID: "hdr-csp-inline", Stage: 1, Category: "Web", Severity: "low", Title: "CSP masih mengizinkan script inline", Location: where,
				Detail: "'unsafe-inline' pada script-src melemahkan perlindungan terhadap XSS.",
				Advice: "Gunakan CSP berbasis nonce (Next.js: nonce dibuat di middleware) lalu hapus 'unsafe-inline' dari script-src."})
		} else {
			c.pass("CSP membatasi script inline")
		}
		if strings.Contains(script, "'unsafe-eval'") {
			c.add(Finding{ID: "hdr-csp-eval", Stage: 1, Category: "Web", Severity: "low", Title: "CSP mengizinkan eval()", Location: where,
				Detail: "'unsafe-eval' memungkinkan teks dijalankan sebagai kode.", Advice: "Hapus 'unsafe-eval' bila library yang dipakai tidak membutuhkannya."})
		}
	}

	if _, framed := directive(csp, "frame-ancestors"); headers.Get("X-Frame-Options") == "" && !framed {
		c.add(Finding{ID: "hdr-frame", Stage: 1, Category: "Web", Severity: "medium", Title: "Aplikasi bisa disisipkan di situs lain (clickjacking)", Location: where,
			Detail: "Tanpa X-Frame-Options atau frame-ancestors, situs lain bisa menampilkan aplikasi di iframe tersembunyi dan menipu klik pengguna.",
			Advice: "Tambahkan X-Frame-Options: DENY atau CSP frame-ancestors 'none'."})
	} else {
		c.pass("Proteksi clickjacking aktif")
	}
	if !strings.EqualFold(strings.TrimSpace(headers.Get("X-Content-Type-Options")), "nosniff") {
		c.add(Finding{ID: "hdr-nosniff", Stage: 1, Category: "Web", Severity: "low", Title: "X-Content-Type-Options: nosniff belum ada", Location: where,
			Detail: "Browser bisa menebak jenis file dan menjalankan file unggahan sebagai script.", Advice: "Tambahkan header X-Content-Type-Options: nosniff."})
	} else {
		c.pass("X-Content-Type-Options: nosniff aktif")
	}
	if headers.Get("Referrer-Policy") == "" {
		c.add(Finding{ID: "hdr-referrer", Stage: 1, Category: "Web", Severity: "low", Title: "Referrer-Policy belum diatur", Location: where,
			Detail: "Alamat lengkap halaman (termasuk parameter) bisa terkirim ke situs lain lewat header Referer.", Advice: "Tambahkan Referrer-Policy: strict-origin-when-cross-origin."})
	} else {
		c.pass("Referrer-Policy diatur")
	}
	if headers.Get("Permissions-Policy") == "" {
		c.add(Finding{ID: "hdr-permissions", Stage: 1, Category: "Web", Severity: "low", Title: "Permissions-Policy belum diatur", Location: where,
			Detail: "Fitur perangkat (kamera, mikrofon, lokasi) tidak dibatasi secara eksplisit.", Advice: "Tambahkan Permissions-Policy, misalnya camera=(), microphone=(), geolocation=()."})
	} else {
		c.pass("Permissions-Policy diatur")
	}
	if powered := strings.TrimSpace(headers.Get("X-Powered-By")); powered != "" {
		c.add(Finding{ID: "hdr-powered-by", Stage: 1, Category: "Web", Severity: "low", Title: "Teknologi server terlihat (X-Powered-By: " + powered + ")", Location: where,
			Detail: "Informasi ini membantu penyerang memilih celah yang cocok.", Advice: "Next.js: tambahkan poweredByHeader: false di next.config.js. Express: app.disable(\"x-powered-by\")."})
	}
	if server := strings.TrimSpace(headers.Get("Server")); server != "" && versionPattern.MatchString(server) {
		c.add(Finding{ID: "hdr-server-version", Stage: 1, Category: "Web", Severity: "low", Title: "Versi server terlihat (" + server + ")", Location: where,
			Detail: "Nomor versi memudahkan pencarian celah yang sudah dipublikasikan.", Advice: "Sembunyikan nomor versi pada header Server."})
	}

	allowOrigin := strings.TrimSpace(headers.Get("Access-Control-Allow-Origin"))
	credentials := strings.EqualFold(strings.TrimSpace(headers.Get("Access-Control-Allow-Credentials")), "true")
	if allowOrigin == probeOrigin {
		severity := "medium"
		if credentials { severity = "high" }
		c.add(Finding{ID: "cors-reflect", Stage: 1, Category: "Web", Severity: severity, Title: "CORS menerima origin mana pun", Location: where,
			Detail: "Server memantulkan origin asing di Access-Control-Allow-Origin" + map[bool]string{true: " dan mengizinkan cookie, sehingga situs lain dapat membaca data pengguna yang sedang login.", false: "."}[credentials],
			Advice: "Batasi Access-Control-Allow-Origin ke daftar domain yang dipercaya saja."})
	} else {
		c.pass("CORS tidak memantulkan origin asing")
	}

	cookieIssues := 0
	for _, cookie := range resp.Cookies() {
		missing := []string{}
		if !cookie.Secure { missing = append(missing, "Secure") }
		if !cookie.HttpOnly { missing = append(missing, "HttpOnly") }
		if cookie.SameSite == 0 { missing = append(missing, "SameSite") }
		if len(missing) == 0 || cookieIssues >= 5 { continue }
		cookieIssues++
		severity := "low"
		if !cookie.Secure { severity = "medium" }
		c.add(Finding{ID: "cookie-" + strings.ToLower(cookie.Name), Stage: 1, Category: "Web", Severity: severity, Title: "Cookie " + cookie.Name + " tanpa " + strings.Join(missing, ", "),
			Location: "Set-Cookie di " + home,
			Detail: "Secure mencegah cookie terkirim lewat HTTP, HttpOnly mencegah script membacanya, SameSite menahan permintaan dari situs lain (CSRF).",
			Advice: "Set cookie dengan Secure; HttpOnly; SameSite=Lax (atau Strict untuk cookie sesi)."})
	}
	if len(resp.Cookies()) > 0 && cookieIssues == 0 { c.pass("Cookie halaman utama memakai Secure, HttpOnly dan SameSite") }

	plain := "http://" + parsed.Host + "/"
	redirect, _, err := get(ctx, webClient(false), plain, nil)
	switch {
	case err != nil:
		c.skip("Pengalihan HTTP → HTTPS tidak dapat diperiksa (" + shortError(err) + ").")
	case redirect.StatusCode >= 300 && redirect.StatusCode < 400 && strings.HasPrefix(strings.ToLower(redirect.Header.Get("Location")), "https://"):
		c.pass("Akses http:// dialihkan ke https://")
	default:
		c.add(Finding{ID: "web-http-redirect", Stage: 1, Category: "Web", Severity: "medium", Title: "Akses http:// tidak dialihkan ke HTTPS", Location: plain,
			Detail: fmt.Sprintf("Permintaan lewat HTTP dijawab HTTP %d tanpa pengalihan ke HTTPS.", redirect.StatusCode),
			Advice: "Aktifkan pengalihan permanen (301/308) dari http:// ke https://."})
	}
}

func atoi(text string) int { value, _ := strconv.Atoi(text); return value }

var envLine = regexp.MustCompile(`(?m)^[A-Z_][A-Z0-9_]*\s*=\s*\S`)

func looksLikeEnv(body string) bool {
	return envLine.MatchString(body) && !strings.Contains(strings.ToLower(body), "<html")
}

// checkExposedFiles looks for files that must never be public. The content
// is checked too, because many hosts answer every path with index.html.
func checkExposedFiles(ctx context.Context, c *collector, t Target) {
	parsed, err := url.Parse(strings.TrimSpace(t.URL))
	if err != nil || parsed.Hostname() == "" { return }
	probes := []struct {
		path  string
		looks func(string) bool
	}{
		{"/.env", looksLikeEnv},
		{"/.env.local", looksLikeEnv},
		{"/.env.production", looksLikeEnv},
		{"/.git/config", func(body string) bool { return strings.Contains(body, "[core]") }},
		{"/.git/HEAD", func(body string) bool { return strings.HasPrefix(strings.TrimSpace(body), "ref: refs/") }},
	}
	client := webClient(false)
	exposed := []string{}
	for _, probe := range probes {
		target := "https://" + parsed.Host + probe.path
		resp, body, err := get(ctx, client, target, nil)
		if err != nil { continue }
		if resp.StatusCode == http.StatusOK && probe.looks(string(body)) { exposed = append(exposed, target) }
	}
	if len(exposed) > 0 {
		c.add(Finding{ID: "exposed-files", Stage: 1, Category: "Web", Severity: "high", Title: "File rahasia dapat diunduh publik", Location: strings.Join(exposed, ", "),
			Detail: "File .env atau folder .git bisa dibuka siapa pun; isinya sering berupa kata sandi, token, atau seluruh kode sumber.",
			Advice: "Hapus file itu dari output yang di-deploy, lalu ganti (rotasi) semua rahasia yang pernah ada di dalamnya karena anggap sudah bocor."})
	} else {
		c.pass("Tidak ada file .env atau folder .git yang terbuka")
	}
}

// ---------- tahap 1: konfigurasi Vercel ----------

var publicPrefixes = []string{"NEXT_PUBLIC_", "VITE_", "REACT_APP_", "EXPO_PUBLIC_", "NUXT_PUBLIC_", "PUBLIC_", "GATSBY_"}
var secretWords = []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "PRIVATE", "SERVICE_ROLE", "API_KEY", "ACCESS_KEY", "CREDENTIAL"}
var publicSafeWords = []string{"PUBLISHABLE", "ANON", "PUBLIC_KEY", "SITE_KEY", "VAPID_PUBLIC", "MEASUREMENT_ID"}

func containsAny(text string, words []string) bool {
	for _, word := range words { if strings.Contains(text, word) { return true } }
	return false
}

func hasAnyPrefix(text string, prefixes []string) bool {
	for _, prefix := range prefixes { if strings.HasPrefix(text, prefix) { return true } }
	return false
}

func rawNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

func checkVercel(c *collector, owner, repo string) {
	token := strings.TrimSpace(os.Getenv("VERCEL_TOKEN"))
	if token == "" { c.skip("Konfigurasi Vercel tidak diperiksa: VERCEL_TOKEN belum diisi."); return }
	client := vercelapp.New(token)
	projects, err := client.FindLinkedProjects(owner, repo)
	if err != nil { c.skip("Project Vercel tidak dapat dibaca (" + shortError(err) + ")."); return }
	if len(projects) == 0 { c.skip("Tidak ada project Vercel yang terhubung ke repo " + owner + "/" + repo + "."); return }
	project := projects[0]
	location := "Vercel → project " + project.Name

	var detail struct {
		SSOProtection      json.RawMessage `json:"ssoProtection"`
		PasswordProtection json.RawMessage `json:"passwordProtection"`
	}
	if err := client.Do(http.MethodGet, "/v9/projects/"+url.PathEscape(project.ID), nil, nil, &detail); err != nil {
		c.skip("Pengaturan Deployment Protection tidak dapat dibaca (" + shortError(err) + ").")
	} else if rawNull(detail.SSOProtection) && rawNull(detail.PasswordProtection) {
		c.add(Finding{ID: "vercel-protection", Stage: 1, Category: "Vercel", Severity: "low", Title: "Deployment Protection nonaktif", Location: location + " → Settings → Deployment Protection",
			Detail: "URL preview (versi uji tiap commit) bisa dibuka siapa pun yang mengetahui alamatnya, termasuk fitur yang belum dirilis.",
			Advice: "Aktifkan Vercel Authentication untuk preview deployments. Production tetap publik."})
	} else {
		c.pass("Deployment Protection Vercel aktif")
	}

	var raw json.RawMessage
	if err := client.Do(http.MethodGet, "/v10/projects/"+url.PathEscape(project.ID)+"/env", nil, nil, &raw); err != nil {
		c.skip("Environment Variables Vercel tidak dapat dibaca (" + shortError(err) + ")."); return
	}
	type envEntry struct {
		Key  string `json:"key"`
		Type string `json:"type"`
	}
	var entries []envEntry
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		_ = json.Unmarshal(trimmed, &entries)
	} else {
		var wrapper struct { Envs []envEntry `json:"envs"` }
		_ = json.Unmarshal(trimmed, &wrapper)
		entries = wrapper.Envs
	}
	exposed, plain := []string{}, []string{}
	seen := map[string]bool{}
	for _, entry := range entries {
		key := strings.ToUpper(strings.TrimSpace(entry.Key))
		if key == "" || seen[key] { continue }
		seen[key] = true
		if !containsAny(key, secretWords) { continue }
		if hasAnyPrefix(key, publicPrefixes) {
			if !containsAny(key, publicSafeWords) { exposed = append(exposed, entry.Key) }
		} else if entry.Type == "plain" {
			plain = append(plain, entry.Key)
		}
	}
	if len(exposed) > 0 {
		c.add(Finding{ID: "vercel-public-secret", Stage: 1, Category: "Vercel", Severity: "high", Title: "Rahasia dikirim ke browser lewat variabel publik", Location: location + " → Environment Variables: " + strings.Join(exposed, ", "),
			Detail: "Variabel berawalan NEXT_PUBLIC_/VITE_/REACT_APP_ ikut masuk ke kode yang diunduh browser, sehingga siapa pun bisa membacanya.",
			Advice: "Pindahkan nilai rahasia ke variabel tanpa awalan publik dan pakai hanya di server (API route/backend), lalu rotasi nilainya."})
	} else {
		c.pass("Tidak ada rahasia di variabel publik Vercel")
	}
	if len(plain) > 0 {
		c.add(Finding{ID: "vercel-plain-secret", Stage: 1, Category: "Vercel", Severity: "low", Title: "Rahasia disimpan sebagai variabel Plain", Location: location + " → Environment Variables: " + strings.Join(plain, ", "),
			Detail: "Nilai Plain terlihat utuh di dashboard Vercel bagi semua anggota team.",
			Advice: "Simpan ulang sebagai Sensitive (atau Encrypted) agar nilainya tidak bisa dibaca kembali dari dashboard."})
	}
}

// ---------- tahap 1–2: GitHub, kode dan dependensi ----------

type githubClient struct {
	token string
	http  *http.Client
}

func (g githubClient) get(ctx context.Context, apiPath string, raw bool, out interface{}) (int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+apiPath, nil)
	if err != nil { return 0, nil, err }
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	if raw { req.Header.Set("Accept", "application/vnd.github.raw") } else { req.Header.Set("Accept", "application/vnd.github+json") }
	resp, err := g.http.Do(req)
	if err != nil { return 0, nil, err }
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil { return resp.StatusCode, resp.Header, err }
	if resp.StatusCode == http.StatusOK && out != nil {
		if bytesOut, ok := out.(*[]byte); ok { *bytesOut = body } else if err := json.Unmarshal(body, out); err != nil { return resp.StatusCode, resp.Header, err }
	}
	return resp.StatusCode, resp.Header, nil
}

func repoPath(owner, repo string) string { return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) }

func escapePath(filePath string) string {
	parts := strings.Split(filePath, "/")
	for i, part := range parts { parts[i] = url.PathEscape(part) }
	return strings.Join(parts, "/")
}

func isEnvFile(base string) bool {
	lower := strings.ToLower(base)
	if lower != ".env" && !strings.HasPrefix(lower, ".env.") { return false }
	for _, safe := range []string{".example", ".sample", ".template", ".dist", ".defaults"} {
		if strings.HasSuffix(lower, safe) { return false }
	}
	return true
}

func checkGitHub(ctx context.Context, c *collector, owner, repo, branch string) {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" { c.skip("Pemeriksaan GitHub, kode dan dependensi dilewati: GITHUB_TOKEN belum diisi."); return }
	g := githubClient{token: token, http: &http.Client{Timeout: 15 * time.Second}}
	base := repoPath(owner, repo)
	repoLabel := "GitHub " + owner + "/" + repo

	var info struct {
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	status, _, err := g.get(ctx, base, false, &info)
	if err != nil || status != http.StatusOK {
		c.skip(fmt.Sprintf("Repo %s/%s tidak dapat dibaca (HTTP %d); pemeriksaan kode dilewati.", owner, repo, status)); return
	}
	if !info.Private {
		c.add(Finding{ID: "gh-public", Stage: 1, Category: "GitHub", Severity: "medium", Title: "Repo aplikasi bersifat publik", Location: repoLabel,
			Detail: "Seluruh kode dan riwayat commit bisa dibaca siapa pun, termasuk rahasia yang mungkin pernah ter-commit.",
			Advice: "Jadikan repo privat bila tidak memang dimaksudkan open source (GitHub → Settings → Danger Zone → Change visibility)."})
	} else {
		c.pass("Repo GitHub privat")
	}
	if branch == "" { branch = info.DefaultBranch }
	if branch == "" { branch = "main" }

	switch status, _, _ := g.get(ctx, base+"/branches/"+url.PathEscape(branch)+"/protection", false, nil); status {
	case http.StatusOK:
		c.pass("Branch " + branch + " diproteksi")
	case http.StatusNotFound:
		c.add(Finding{ID: "gh-branch-protection", Stage: 1, Category: "GitHub", Severity: "low", Title: "Branch " + branch + " tanpa proteksi", Location: repoLabel + " → Settings → Branches",
			Detail: "Siapa pun yang punya akses tulis (atau token yang bocor) bisa menimpa atau menghapus riwayat branch utama.",
			Advice: "Tambahkan branch protection rule: larang force push dan penghapusan branch."})
	default:
		c.skip(fmt.Sprintf("Proteksi branch tidak dapat diperiksa (HTTP %d); untuk repo privat fitur ini butuh paket GitHub berbayar atau izin Administration: read.", status))
	}

	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	manifests := []string{}
	middleware := false
	if status, _, err := g.get(ctx, base+"/git/trees/"+url.PathEscape(branch)+"?recursive=1", false, &tree); err != nil || status != http.StatusOK {
		c.skip(fmt.Sprintf("Daftar file repo tidak dapat dibaca (HTTP %d).", status))
	} else {
		envFiles := []string{}
		for _, entry := range tree.Tree {
			if entry.Type != "blob" || strings.Contains(entry.Path, "node_modules/") { continue }
			name := path.Base(entry.Path)
			dir := path.Dir(entry.Path)
			depth := strings.Count(entry.Path, "/")
			if isEnvFile(name) { envFiles = append(envFiles, entry.Path) }
			if name == "package.json" && depth <= 2 && len(manifests) < 3 { manifests = append(manifests, entry.Path) }
			if (name == "middleware.ts" || name == "middleware.js") && depth <= 2 && (dir == "." || path.Base(dir) == "src" || depth == 1) { middleware = true }
		}
		if len(envFiles) > 0 {
			if len(envFiles) > 5 { envFiles = append(envFiles[:5], "…") }
			c.add(Finding{ID: "code-env-committed", Stage: 2, Category: "Kode", Severity: "high", Title: "File .env ter-commit ke repo", Location: repoLabel + ": " + strings.Join(envFiles, ", "),
				Detail: "File .env biasanya berisi kata sandi dan token. Setelah ter-commit, isinya tetap ada di riwayat git walaupun filenya dihapus.",
				Advice: "Hapus file dari repo, tambahkan .env* ke .gitignore, lalu ganti (rotasi) semua rahasia di dalamnya."})
		} else {
			c.pass("Tidak ada file .env yang ter-commit")
		}
	}

	deps := map[string]string{}
	depSource := map[string]string{}
	for _, manifest := range manifests {
		var content []byte
		status, _, err := g.get(ctx, base+"/contents/"+escapePath(manifest)+"?ref="+url.QueryEscape(branch), true, &content)
		if err != nil || status != http.StatusOK { continue }
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(content, &pkg) != nil { continue }
		for _, group := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
			for name, spec := range group {
				if _, known := deps[name]; known { continue }
				if version := minimumVersion(spec); version != "" { deps[name] = version; depSource[name] = manifest }
			}
		}
	}
	if version, ok := deps["next"]; ok { checkNextVersion(c, version, middleware, repoLabel+": "+depSource["next"]) }

	dependabotOK := checkDependabot(ctx, c, g, base, repoLabel)
	if !dependabotOK && len(deps) > 0 { checkOSV(ctx, c, deps, repoLabel) }

	var secrets []struct {
		SecretType string `json:"secret_type_display_name"`
	}
	switch status, _, err := g.get(ctx, base+"/secret-scanning/alerts?state=open&per_page=100", false, &secrets); {
	case err == nil && status == http.StatusOK && len(secrets) > 0:
		types := map[string]bool{}
		names := []string{}
		for _, item := range secrets { if !types[item.SecretType] { types[item.SecretType] = true; names = append(names, item.SecretType) } }
		c.add(Finding{ID: "code-secret-scanning", Stage: 2, Category: "Kode", Severity: "high", Title: fmt.Sprintf("%d rahasia terdeteksi di repo", len(secrets)), Location: repoLabel + " → Security → Secret scanning",
			Detail: "GitHub menemukan rahasia aktif di kode atau riwayat: " + strings.Join(names, ", ") + ".",
			Advice: "Cabut/rotasi setiap rahasia itu di penyedianya terlebih dahulu, baru hapus dari kode."})
	case err == nil && status == http.StatusOK:
		c.pass("Secret scanning GitHub tidak menemukan rahasia terbuka")
	default:
		c.skip(fmt.Sprintf("Secret scanning tidak dapat dibaca (HTTP %d): aktifkan di repo dan beri token izin Secret scanning alerts: read.", status))
	}
}

var semverPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// minimumVersion reads the lowest version a package.json range allows
// ("^14.2.5" → "14.2.5"); ranges that point elsewhere return "".
func minimumVersion(spec string) string {
	spec = strings.TrimSpace(spec)
	for _, prefix := range []string{"workspace:", "file:", "link:", "git", "http", "npm:"} {
		if strings.HasPrefix(spec, prefix) { return "" }
	}
	match := semverPattern.FindString(spec)
	return match
}

func versionParts(version string) (int, int, int) {
	match := semverPattern.FindStringSubmatch(version)
	if match == nil { return 0, 0, 0 }
	return atoi(match[1]), atoi(match[2]), atoi(match[3])
}

func versionLess(a, b string) bool {
	a1, a2, a3 := versionParts(a)
	b1, b2, b3 := versionParts(b)
	if a1 != b1 { return a1 < b1 }
	if a2 != b2 { return a2 < b2 }
	return a3 < b3
}

// checkNextVersion flags Next.js releases before the fix for the middleware
// authorization bypass (CVE-2025-29927) and other advisories of that period.
func checkNextVersion(c *collector, version string, middleware bool, location string) {
	major, _, _ := versionParts(version)
	fixed := map[int]string{12: "12.3.5", 13: "13.5.9", 14: "14.2.25", 15: "15.2.3"}
	threshold, known := fixed[major]
	vulnerable := major > 0 && major < 12 || (known && versionLess(version, threshold))
	if !vulnerable { c.pass("Versi Next.js (" + version + ") sudah melewati perbaikan keamanan besar 2025"); return }
	severity := "medium"
	detail := "Versi Next.js " + version + " sebelum rilis yang menambal beberapa advisory keamanan, termasuk CVE-2025-29927."
	if middleware {
		severity = "high"
		detail += " Proyek ini memakai middleware, sehingga pemeriksaan login di middleware bisa dilewati (bypass) pada versi ini."
	}
	target := threshold
	if target == "" { target = "14.2.25" }
	c.add(Finding{ID: "dep-next-version", Stage: 2, Category: "Dependensi", Severity: severity, Title: "Next.js " + version + " perlu diperbarui", Location: location,
		Detail: detail, Advice: "Naikkan next ke rilis terbaru di jalur versi yang sama (minimal " + target + "), lalu build dan uji ulang."})
}

func checkDependabot(ctx context.Context, c *collector, g githubClient, base, repoLabel string) bool {
	var alerts []struct {
		Dependency struct {
			Package struct { Name string `json:"name"` } `json:"package"`
		} `json:"dependency"`
		Advisory struct {
			Severity string `json:"severity"`
		} `json:"security_advisory"`
	}
	status, _, err := g.get(ctx, base+"/dependabot/alerts?state=open&per_page=100", false, &alerts)
	if err != nil || status != http.StatusOK {
		c.skip(fmt.Sprintf("Dependabot alerts tidak dapat dibaca (HTTP %d): aktifkan Dependabot alerts dan beri token izin Dependabot alerts: read. Database OSV dipakai sebagai gantinya.", status))
		return false
	}
	groups := map[string][]string{}
	for _, alert := range alerts {
		level := "low"
		switch strings.ToLower(alert.Advisory.Severity) {
		case "critical", "high": level = "high"
		case "medium", "moderate": level = "medium"
		}
		groups[level] = appendUnique(groups[level], alert.Dependency.Package.Name)
	}
	labels := map[string]string{"high": "tinggi/kritis", "medium": "sedang", "low": "rendah"}
	for _, level := range []string{"high", "medium", "low"} {
		packages := groups[level]
		if len(packages) == 0 { continue }
		c.add(Finding{ID: "dep-dependabot-" + level, Stage: 2, Category: "Dependensi", Severity: level,
			Title: fmt.Sprintf("%d paket dengan celah tingkat %s (Dependabot)", len(packages), labels[level]),
			Location: repoLabel + " → Security → Dependabot: " + limitList(packages, 8),
			Detail: "Versi paket yang dipakai punya celah keamanan yang sudah dipublikasikan.",
			Advice: "Perbarui paket-paket ini (atau terima pull request Dependabot), lalu build dan uji ulang."})
	}
	if len(alerts) == 0 { c.pass("Tidak ada peringatan Dependabot yang terbuka") }
	return true
}

func appendUnique(list []string, value string) []string {
	for _, item := range list { if item == value { return list } }
	return append(list, value)
}

func limitList(items []string, limit int) string {
	sort.Strings(items)
	if len(items) > limit { return strings.Join(items[:limit], ", ") + fmt.Sprintf(" (+%d lainnya)", len(items)-limit) }
	return strings.Join(items, ", ")
}

// checkOSV asks the public OSV database about the versions in package.json.
// It uses the lowest version each range allows, so results can include
// issues already fixed by a newer installed patch release.
func checkOSV(ctx context.Context, c *collector, deps map[string]string, repoLabel string) {
	names := make([]string, 0, len(deps))
	for name := range deps { names = append(names, name) }
	sort.Strings(names)
	if len(names) > 150 { names = names[:150] }
	type query struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Version string `json:"version"`
	}
	queries := make([]query, 0, len(names))
	for _, name := range names {
		var q query
		q.Package.Name, q.Package.Ecosystem, q.Version = name, "npm", deps[name]
		queries = append(queries, q)
	}
	payload, _ := json.Marshal(map[string]interface{}{"queries": queries})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.osv.dev/v1/querybatch", bytes.NewReader(payload))
	if err != nil { return }
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil { c.skip("Database celah OSV tidak dapat dihubungi (" + shortError(err) + ")."); return }
	defer resp.Body.Close()
	var result struct {
		Results []struct {
			Vulns []struct { ID string `json:"id"` } `json:"vulns"`
		} `json:"results"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result) != nil {
		c.skip(fmt.Sprintf("Database celah OSV menjawab HTTP %d.", resp.StatusCode)); return
	}
	affected := []string{}
	for i, item := range result.Results {
		if i >= len(names) || len(item.Vulns) == 0 { continue }
		affected = append(affected, fmt.Sprintf("%s@%s (%d)", names[i], deps[names[i]], len(item.Vulns)))
	}
	if len(affected) == 0 { c.pass("Database OSV tidak mencatat celah untuk versi di package.json"); return }
	c.add(Finding{ID: "dep-osv", Stage: 2, Category: "Dependensi", Severity: "medium", Title: fmt.Sprintf("%d paket punya celah yang diketahui (OSV)", len(affected)),
		Location: repoLabel + " → package.json: " + limitList(affected, 8),
		Detail: "Angka dalam kurung adalah jumlah advisory. Versi yang diperiksa adalah versi minimum di package.json; versi terpasang bisa lebih baru.",
		Advice: "Jalankan npm audit / npm outdated di proyek, perbarui paket yang terdampak, lalu build dan uji ulang."})
}

// ---------- DevControl sendiri ----------

func checkSelf(ctx context.Context, c *collector) {
	location := "DevControl → Settings → Keamanan akun"
	if enabled, err := auth.OwnerTwoFactorEnabled(); err != nil {
		c.skip("Status 2FA owner tidak dapat dibaca (" + shortError(err) + ").")
	} else if !enabled {
		c.add(Finding{ID: "self-2fa", Stage: 0, Category: "DevControl", Severity: "high", Title: "2FA owner belum aktif", Location: location,
			Detail: "Akun owner hanya dilindungi satu kata sandi, padahal akun ini menguasai token GitHub, Vercel dan Cloudflare serta Update Diri.",
			Advice: "Aktifkan 2FA (aplikasi authenticator) di Settings → Keamanan akun."})
	} else {
		c.pass("2FA owner aktif")
	}
	if strings.TrimSpace(os.Getenv("DEVCONTROL_TOTP_RESET")) == "1" {
		c.add(Finding{ID: "self-2fa-reset", Stage: 0, Category: "DevControl", Severity: "high", Title: "Sakelar darurat DEVCONTROL_TOTP_RESET masih aktif", Location: "Vercel → Environment Variables",
			Detail: "Selama variabel ini bernilai 1, 2FA owner diabaikan.", Advice: "Hapus DEVCONTROL_TOTP_RESET lalu redeploy, kemudian aktifkan ulang 2FA."})
	}

	password := os.Getenv("DEVCONTROL_ADMIN_PASSWORD")
	mac := hmac.New(sha256.New, []byte(password))
	_, _ = mac.Write([]byte("devcontrol/session/v1|" + strings.TrimSpace(os.Getenv("CF_API_TOKEN"))))
	if password != "" && os.Getenv("DEVCONTROL_SESSION_SECRET") == hex.EncodeToString(mac.Sum(nil)) {
		c.add(Finding{ID: "self-session-secret", Stage: 0, Category: "DevControl", Severity: "medium", Title: "Rahasia sesi diturunkan dari kata sandi admin", Location: "Vercel → Environment Variables → DEVCONTROL_SESSION_SECRET",
			Detail: "Karena belum diisi sendiri, kunci penanda tangan cookie dihitung dari kata sandi admin. Bila kata sandi bocor, cookie sesi juga bisa dipalsukan.",
			Advice: "Isi DEVCONTROL_SESSION_SECRET dengan 64 karakter acak (tipe Sensitive) lalu redeploy. Semua sesi keluar sekali; aktifkan ulang notifikasi atau jalankan satu deploy agar Worker runner memakai kunci baru."})
	} else if password != "" {
		c.pass("Rahasia sesi terpisah dari kata sandi admin")
	}
	if zipKey := os.Getenv("ZIP_ARCHIVE_ACCESS_TOKEN"); zipKey != "" && zipKey == password {
		c.add(Finding{ID: "self-zip-key", Stage: 0, Category: "DevControl", Severity: "high", Title: "Kunci arsip ZIP sama dengan kata sandi admin", Location: "Vercel → Environment Variables → ZIP_ARCHIVE_ACCESS_TOKEN",
			Detail: "Kata sandi owner jadi diketik dan disimpan di tempat kedua.", Advice: "Hapus ZIP_ARCHIVE_ACCESS_TOKEN (arsip tetap dilindungi sesi admin + konfirmasi) atau isi dengan nilai acak lain."})
	} else {
		c.pass("Kunci arsip ZIP tidak memakai ulang kata sandi admin")
	}
	if len(password) > 0 && len(password) < 20 {
		c.add(Finding{ID: "self-password-length", Stage: 0, Category: "DevControl", Severity: "low", Title: "Kata sandi admin relatif pendek", Location: "Vercel → Environment Variables → DEVCONTROL_ADMIN_PASSWORD",
			Detail: fmt.Sprintf("Panjangnya %d karakter.", len(password)), Advice: "Gunakan minimal 24 karakter acak dari password manager."})
	}

	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		g := githubClient{token: token, http: &http.Client{Timeout: 10 * time.Second}}
		status, headers, err := g.get(ctx, "/user", false, nil)
		switch {
		case err != nil || status != http.StatusOK:
			c.skip(fmt.Sprintf("Hak GITHUB_TOKEN tidak dapat diperiksa (HTTP %d).", status))
		case headers.Get("X-OAuth-Scopes") == "":
			c.pass("GITHUB_TOKEN adalah fine-grained token")
		default:
			scopes := headers.Get("X-OAuth-Scopes")
			severity := "low"
			if strings.Contains(scopes, "admin:") || strings.Contains(scopes, "delete_repo") { severity = "medium" }
			c.add(Finding{ID: "self-github-scopes", Stage: 0, Category: "DevControl", Severity: severity, Title: "GITHUB_TOKEN adalah token klasik berhak luas", Location: "GitHub → Settings → Developer settings",
				Detail: "Hak token saat ini: " + scopes + ". Token klasik berlaku untuk semua repo di akun.",
				Advice: "Ganti dengan fine-grained token yang dibatasi ke repo yang dikelola DevControl (Contents, Administration, Workflows sesuai kebutuhan) dan beri masa berlaku."})
		}
	}
}

// ---------- targets, storage and API ----------

// Targets lists DevControl itself plus every application with a saved URL.
func Targets(self Target) ([]Target, error) {
	rows, err := d1.Query(`SELECT name, display_name, repo, branch, app_url FROM services WHERE COALESCE(app_url, '') != '' ORDER BY id LIMIT 60`)
	if err != nil { return nil, err }
	targets := []Target{self}
	seen := map[string]bool{self.Key: true}
	for _, row := range rows {
		text := func(key string) string { value, _ := row[key].(string); return strings.TrimSpace(value) }
		key := text("name")
		if key == "" || seen[strings.ToLower(key)] { continue }
		seen[strings.ToLower(key)] = true
		name := text("display_name")
		if name == "" { name = key }
		appURL := text("app_url")
		if !strings.Contains(appURL, "://") { appURL = "https://" + appURL }
		targets = append(targets, Target{Key: key, Name: name, URL: appURL, Repo: text("repo"), Branch: text("branch")})
	}
	return targets, nil
}

func findTarget(targets []Target, key string) (Target, bool) {
	for _, target := range targets { if strings.EqualFold(target.Key, key) { return target, true } }
	return Target{}, false
}

func save(report Report) error {
	encoded, err := json.Marshal(report)
	if err != nil { return err }
	high, medium, low := counts(report.Findings)
	if _, err := d1.Query(`INSERT INTO app_audits (app, score, high, medium, low, report, source, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		report.App, report.Score, high, medium, low, string(encoded), report.Source, report.CreatedAt); err != nil { return err }
	_, _ = d1.Query(`DELETE FROM app_audits WHERE app = ? AND id NOT IN (SELECT id FROM app_audits WHERE app = ? ORDER BY id DESC LIMIT ?)`, report.App, report.App, keepPerApp)
	return nil
}

type historyPoint struct {
	Score     int    `json:"score"`
	CreatedAt string `json:"created_at"`
}

func toInt(value interface{}) int {
	switch number := value.(type) {
	case float64: return int(number)
	case int64: return int(number)
	case int: return number
	case string: return atoi(number)
	}
	return 0
}

// latest returns the newest report of one app and its score history (oldest first).
func latest(key string) (*Report, []historyPoint, error) {
	rows, err := d1.Query(`SELECT report, score, created_at FROM app_audits WHERE app = ? ORDER BY id DESC LIMIT 20`, key)
	if err != nil { return nil, nil, err }
	history := make([]historyPoint, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		created, _ := rows[i]["created_at"].(string)
		history = append(history, historyPoint{Score: toInt(rows[i]["score"]), CreatedAt: created})
	}
	if len(rows) == 0 { return nil, history, nil }
	text, _ := rows[0]["report"].(string)
	var report Report
	if err := json.Unmarshal([]byte(text), &report); err != nil { return nil, history, err }
	return &report, history, nil
}

type summary struct {
	Target
	Score     *int   `json:"score"`
	High      int    `json:"high"`
	Medium    int    `json:"medium"`
	Low       int    `json:"low"`
	CreatedAt string `json:"created_at"`
	Source    string `json:"source"`
	History   []int  `json:"history"`
}

func summaries(targets []Target) ([]summary, error) {
	rows, err := d1.Query(`SELECT app, score, high, medium, low, source, created_at FROM app_audits ORDER BY id DESC LIMIT 800`)
	if err != nil { return nil, err }
	byApp := map[string][]map[string]interface{}{}
	for _, row := range rows {
		app, _ := row["app"].(string)
		key := strings.ToLower(app)
		if len(byApp[key]) < 12 { byApp[key] = append(byApp[key], row) }
	}
	list := make([]summary, 0, len(targets))
	for _, target := range targets {
		item := summary{Target: target, History: []int{}}
		entries := byApp[strings.ToLower(target.Key)]
		if len(entries) > 0 {
			value := toInt(entries[0]["score"])
			item.Score = &value
			item.High, item.Medium, item.Low = toInt(entries[0]["high"]), toInt(entries[0]["medium"]), toInt(entries[0]["low"])
			item.CreatedAt, _ = entries[0]["created_at"].(string)
			item.Source, _ = entries[0]["source"].(string)
			for i := len(entries) - 1; i >= 0; i-- { item.History = append(item.History, toInt(entries[i]["score"])) }
		}
		list = append(list, item)
	}
	return list, nil
}

// Handle serves /api/app-audit (owner/admin):
// GET → every target with its latest score; GET ?app=<key> → latest report;
// POST {app} → run an audit now and store it.
func Handle(w http.ResponseWriter, r *http.Request, self Target) {
	w.Header().Set("Cache-Control", "no-store")
	if !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("Audit Aplikasi hanya untuk owner/admin")); return }
	targets, err := Targets(self)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	switch r.Method {
	case http.MethodGet:
		if key := strings.TrimSpace(r.URL.Query().Get("app")); key != "" {
			target, ok := findTarget(targets, key)
			if !ok { util.Error(w, http.StatusNotFound, fmt.Errorf("aplikasi tidak terdaftar di DevControl")); return }
			report, history, err := latest(target.Key)
			if err != nil { util.Error(w, http.StatusBadGateway, err); return }
			util.JSON(w, http.StatusOK, map[string]interface{}{"target": target, "report": report, "history": history})
			return
		}
		list, err := summaries(targets)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]interface{}{"targets": list})
	case http.MethodPost:
		var input struct { App string `json:"app"` }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan audit tidak valid")); return
		}
		target, ok := findTarget(targets, input.App)
		if !ok { util.Error(w, http.StatusNotFound, fmt.Errorf("hanya aplikasi yang terdaftar di DevControl yang dapat diaudit")); return }
		// A report younger than 30 seconds is reused, so repeated clicks do
		// not send a burst of requests to the application.
		if previous, _, err := latest(target.Key); err == nil && previous != nil {
			if created, parseErr := time.Parse("2006-01-02 15:04:05", previous.CreatedAt); parseErr == nil && time.Since(created) < 30*time.Second {
				util.JSON(w, http.StatusOK, map[string]interface{}{"report": previous, "reused": true}); return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		report := Run(ctx, target, "manual")
		if err := save(report); err != nil { util.Error(w, http.StatusBadGateway, fmt.Errorf("hasil audit tidak dapat disimpan: %w", err)); return }
		util.JSON(w, http.StatusOK, map[string]interface{}{"report": report})
	default:
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET atau POST"))
	}
}

// RunScheduled audits the target whose last audit is oldest, when that is
// more than a day ago (called every 15 minutes by the runner ping). A new
// high-risk finding sends a push notification to owner/admin devices.
func RunScheduled(parent context.Context, self Target) int {
	targets, err := Targets(self)
	if err != nil || len(targets) == 0 { return 0 }
	rows, err := d1.Query(`SELECT app, MAX(created_at) AS last FROM app_audits GROUP BY app`)
	if err != nil { return 0 }
	last := map[string]string{}
	for _, row := range rows {
		app, _ := row["app"].(string)
		stamp, _ := row["last"].(string)
		last[strings.ToLower(app)] = stamp
	}
	var chosen *Target
	oldest := ""
	for i := range targets {
		candidate := targets[i]
		stamp := last[strings.ToLower(candidate.Key)]
		if stamp == "" { chosen = &candidate; oldest = ""; break }
		if chosen == nil || stamp < oldest { chosen = &candidate; oldest = stamp }
	}
	if chosen == nil { return 0 }
	if oldest != "" {
		if created, err := time.Parse("2006-01-02 15:04:05", oldest); err == nil && time.Since(created) < 24*time.Hour { return 0 }
	}
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	previous, _, _ := latest(chosen.Key)
	report := Run(ctx, *chosen, "schedule")
	if err := save(report); err != nil { return 0 }
	notifyNewHigh(previous, report)
	return 1
}

func notifyNewHigh(previous *Report, current Report) {
	before := map[string]bool{}
	if previous != nil {
		for _, finding := range previous.Findings { if finding.Severity == "high" { before[finding.ID] = true } }
	}
	sent := 0
	for _, finding := range current.Findings {
		if finding.Severity != "high" || before[finding.ID] || sent >= 3 { continue }
		sent++
		day := current.CreatedAt
		if len(day) >= 10 { day = day[:10] }
		webpush.Notify(webpush.Message{Event: webpush.EventSecurity, Key: "audit:" + current.App + ":" + finding.ID + ":" + day,
			URL: "/audit?app=" + url.QueryEscape(current.App), Tag: "audit-" + current.App,
			Title: "🛡️ Risiko tinggi: " + current.Name, Body: finding.Title + " — buka Audit Aplikasi untuk saran perbaikannya."})
	}
}

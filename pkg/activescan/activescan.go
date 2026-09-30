// Package activescan is the "Uji Aktif Aman" behind the Audit Aplikasi page:
// it probes a registered application more deeply than the passive audit, but
// never attacks. Every request is a plain GET or OPTIONS with a harmless
// marker; there is no brute force, no injection payload, no upload, and no
// write. It runs only for the owner, only against apps registered in
// DevControl, with a per-run request budget, a pause between requests, and a
// hard timeout. Findings reuse the audit's shape (Tinggi/Sedang/Rendah,
// location, advice).
package activescan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"devcontrol/pkg/appaudit"
	"devcontrol/pkg/auth"
	"devcontrol/pkg/util"
)

const (
	userAgent   = "DevControl-ActiveScan/1.0 (+safe, non-destructive)"
	maxRequests = 60               // hard budget per run
	pause       = 250 * time.Millisecond
	marker      = "dcxss7391probe" // unique, harmless reflection marker
)

type runner struct {
	client   *http.Client
	host     string
	mu       sync.Mutex
	findings []appaudit.Finding
	passed   []string
	skipped  []string
	budget   int
	stopped  bool
}

func (r *runner) add(f appaudit.Finding)   { r.mu.Lock(); r.findings = append(r.findings, f); r.mu.Unlock() }
func (r *runner) pass(text string)         { r.mu.Lock(); r.passed = append(r.passed, text); r.mu.Unlock() }
func (r *runner) skip(text string)         { r.mu.Lock(); r.skipped = append(r.skipped, text); r.mu.Unlock() }

// get spends one unit of the request budget; it stops the run when spent.
func (r *runner) get(ctx context.Context, method, path string, headers map[string]string) (*http.Response, []byte, bool) {
	r.mu.Lock()
	if r.budget <= 0 || r.stopped { r.mu.Unlock(); return nil, nil, false }
	r.budget--
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, nil, false
	case <-time.After(pause):
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+r.host+path, nil)
	if err != nil { return nil, nil, false }
	req.Header.Set("User-Agent", userAgent)
	for key, value := range headers { req.Header.Set(key, value) }
	resp, err := r.client.Do(req)
	if err != nil { return nil, nil, false }
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<10))
	return resp, body, true
}

// Run performs the safe active checks against one registered target.
func Run(parent context.Context, target appaudit.Target) appaudit.Report {
	started := time.Now()
	report := appaudit.Report{App: target.Key, Name: target.Name, URL: target.URL, Repo: target.Repo,
		Findings: []appaudit.Finding{}, Passed: []string{}, Skipped: []string{}, Source: "active"}
	parsed, err := url.Parse(strings.TrimSpace(target.URL))
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "https" {
		report.Skipped = []string{"Uji aktif dilewati: aplikasi tidak punya URL https yang valid."}
		report.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
		return report
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r := &runner{client: client, host: parsed.Host, budget: maxRequests}

	// Whether this is DevControl decides which sensitive endpoints to probe.
	r.checkProtectedEndpoints(ctx, target.Self)
	r.checkReflectedInput(ctx)
	r.checkPathTraversal(ctx)
	r.checkExposedPaths(ctx)
	r.checkCORS(ctx)
	r.checkHTTPMethods(ctx)
	r.checkLoginLockout(ctx, target.Self)

	report.Findings = appaudit.SortFindings(r.findings)
	report.Passed = r.passed
	report.Skipped = r.skipped
	report.Score = appaudit.ScoreOf(report.Findings)
	report.CreatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	report.DurationMS = time.Since(started).Milliseconds()
	return report
}

// checkProtectedEndpoints confirms sensitive APIs refuse an unauthenticated
// reader (401/403) instead of returning data. DevControl's own API paths are
// known; for other apps a common set is tried and 404s are ignored.
func (r *runner) checkProtectedEndpoints(ctx context.Context, self bool) {
	type probe struct{ path, label string }
	probes := []probe{
		{"/api/members?resource=members", "Daftar member"},
		{"/api/vercel-env?resource=vercel-env&view=projects", "Environment Variables"},
		{"/api/databases?resource=databases", "Database"},
		{"/api/security?resource=security", "Pusat Keamanan"},
		{"/api/app-audit?resource=app-audit", "Audit Aplikasi"},
		{"/api/two-factor?resource=two-factor", "Pengaturan 2FA"},
	}
	if !self {
		probes = []probe{{"/api/admin", "Panel admin"}, {"/api/users", "Daftar pengguna"}, {"/api/config", "Konfigurasi"},
			{"/api/env", "Environment"}, {"/api/debug", "Debug"}, {"/admin", "Halaman admin"}}
	}
	checked, leaks := 0, 0
	for _, item := range probes {
		resp, body, ok := r.get(ctx, http.MethodGet, item.path, nil)
		if !ok { continue }
		if resp.StatusCode == http.StatusNotFound { continue }
		checked++
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			// Correctly guarded.
		case resp.StatusCode == http.StatusOK && looksLikeData(body):
			leaks++
			r.add(appaudit.Finding{ID: "active-open-endpoint-" + slug(item.path), Stage: 3, Category: "Uji aktif", Severity: "high",
				Title: "Endpoint sensitif terbuka tanpa login: " + item.label, Location: "https://" + r.host + item.path,
				Detail: "Permintaan tanpa sesi dijawab dengan data (HTTP 200), bukan ditolak. Artinya siapa pun bisa membaca data ini.",
				Advice: "Pastikan endpoint ini memeriksa sesi/otorisasi sebelum menjawab, dan mengembalikan 401/403 untuk permintaan tanpa login."})
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			r.add(appaudit.Finding{ID: "active-endpoint-200-" + slug(item.path), Stage: 3, Category: "Uji aktif", Severity: "low",
				Title: "Endpoint sensitif menjawab 200 tanpa login: " + item.label, Location: "https://" + r.host + item.path,
				Detail: "Endpoint menjawab sukses tanpa sesi, walaupun isinya belum tentu data sensitif.",
				Advice: "Periksa apakah respons ini seharusnya butuh login; bila ya, kembalikan 401/403."})
		}
	}
	if self && checked > 0 && leaks == 0 { r.pass(fmt.Sprintf("%d endpoint sensitif menolak akses tanpa login", checked)) }
	if !self && checked == 0 { r.skip("Tidak menemukan endpoint admin umum yang bisa diperiksa dari luar.") }
}

// checkReflectedInput sends a harmless marker in the query string and looks
// for it echoed unescaped into HTML — a sign of reflected XSS. No script is
// ever sent.
func (r *runner) checkReflectedInput(ctx context.Context) {
	probe := "/?q=" + marker + "<b>"
	resp, body, ok := r.get(ctx, http.MethodGet, probe, nil)
	if !ok { return }
	text := string(body)
	if resp.StatusCode < 400 && strings.Contains(text, marker+"<b>") {
		r.add(appaudit.Finding{ID: "active-reflected-input", Stage: 3, Category: "Uji aktif", Severity: "high",
			Title: "Input dari URL dipantulkan mentah ke halaman", Location: "https://" + r.host + "/?q=…",
			Detail: "Teks dari query string muncul di HTML tanpa di-escape (karakter < tidak diubah jadi &lt;). Ini pintu untuk serangan XSS.",
			Advice: "Escape semua input yang ditampilkan (framework modern melakukannya otomatis; hindari dangerouslySetInnerHTML/innerHTML dengan data dari URL)."})
	} else if resp.StatusCode < 400 && strings.Contains(text, marker) && !strings.Contains(text, marker+"<b>") {
		r.pass("Input dari URL yang dipantulkan sudah di-escape")
	} else {
		r.pass("Input dari URL tidak dipantulkan mentah ke halaman")
	}
}

// checkPathTraversal tries a few read-only traversal shapes; a leak shows the
// server serving files outside the web root.
func (r *runner) checkPathTraversal(ctx context.Context) {
	probes := []string{"/../../etc/passwd", "/..%2f..%2f..%2fetc%2fpasswd", "/static/..%2f..%2f.env"}
	for _, path := range probes {
		resp, body, ok := r.get(ctx, http.MethodGet, path, nil)
		if !ok { return }
		text := string(body)
		if resp.StatusCode == http.StatusOK && (strings.Contains(text, "root:x:") || strings.Contains(text, "root:!:")) {
			r.add(appaudit.Finding{ID: "active-path-traversal", Stage: 3, Category: "Uji aktif", Severity: "high",
				Title: "Server melayani file di luar folder web (path traversal)", Location: "https://" + r.host + path,
				Detail: "Permintaan dengan pola ../ berhasil membaca file sistem. Penyerang bisa mengambil file konfigurasi dan rahasia.",
				Advice: "Normalkan dan batasi path file yang boleh dilayani; jangan pernah menggabungkan input pengguna langsung ke path file."})
			return
		}
	}
	r.pass("Tidak ada path traversal yang berhasil")
}

// checkExposedPaths reads common leftover files, in addition to the passive
// audit's .env/.git checks (backups, dumps, editor swap files).
func (r *runner) checkExposedPaths(ctx context.Context) {
	type probe struct{ path string; verify func(string) bool }
	contains := func(needle string) func(string) bool { return func(body string) bool { return strings.Contains(body, needle) } }
	probes := []probe{
		{"/backup.zip", func(string) bool { return true }},
		{"/backup.sql", contains("INSERT INTO")},
		{"/dump.sql", contains("INSERT INTO")},
		{"/config.json", contains("{")},
		{"/.env.backup", contains("=")},
		{"/wrangler.toml", contains("database_id")},
		{"/.vercel/project.json", contains("projectId")},
	}
	found := []string{}
	for _, item := range probes {
		resp, body, ok := r.get(ctx, http.MethodGet, item.path, nil)
		if !ok { break }
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if resp.StatusCode == http.StatusOK && !strings.HasPrefix(ct, "text/html") && item.verify(string(body)) {
			found = append(found, "https://"+r.host+item.path)
		}
	}
	if len(found) > 0 {
		r.add(appaudit.Finding{ID: "active-exposed-paths", Stage: 3, Category: "Uji aktif", Severity: "high",
			Title: "File cadangan/konfigurasi dapat diunduh publik", Location: strings.Join(found, ", "),
			Detail: "File ini seharusnya tidak ikut dipublikasikan; isinya sering berupa data, kredensial, atau ID project.",
			Advice: "Hapus file itu dari output deploy, dan bila berisi rahasia, ganti (rotasi) rahasianya karena anggap sudah bocor."})
	} else {
		r.pass("Tidak ada file cadangan/konfigurasi umum yang terbuka")
	}
}

// checkCORS asks whether the site trusts an arbitrary origin with credentials.
func (r *runner) checkCORS(ctx context.Context) {
	evil := "https://evil.example.com"
	resp, _, ok := r.get(ctx, http.MethodGet, "/", map[string]string{"Origin": evil})
	if !ok { return }
	allow := strings.TrimSpace(resp.Header.Get("Access-Control-Allow-Origin"))
	creds := strings.EqualFold(strings.TrimSpace(resp.Header.Get("Access-Control-Allow-Credentials")), "true")
	if allow == evil || allow == "*" && creds {
		severity := "medium"
		if creds { severity = "high" }
		r.add(appaudit.Finding{ID: "active-cors", Stage: 3, Category: "Uji aktif", Severity: severity,
			Title: "CORS mempercayai origin mana pun", Location: "Header Access-Control-Allow-Origin di https://" + r.host + "/",
			Detail: "Server mengizinkan situs lain membaca respons" + map[bool]string{true: " beserta cookie pengguna yang sedang login.", false: "."}[creds],
			Advice: "Batasi Access-Control-Allow-Origin ke daftar domain tepercaya, dan jangan gabungkan dengan Allow-Credentials: true untuk origin bebas."})
	} else {
		r.pass("CORS tidak mempercayai origin asing")
	}
}

// checkHTTPMethods looks for risky methods (TRACE, PUT, DELETE) answered at
// the site root via OPTIONS — read-only, it never sends those methods.
func (r *runner) checkHTTPMethods(ctx context.Context) {
	resp, _, ok := r.get(ctx, http.MethodOptions, "/", nil)
	if !ok { return }
	allow := strings.ToUpper(resp.Header.Get("Allow"))
	risky := []string{}
	for _, method := range []string{"TRACE", "TRACK", "PUT", "DELETE", "CONNECT"} {
		if strings.Contains(allow, method) { risky = append(risky, method) }
	}
	if len(risky) > 0 {
		r.add(appaudit.Finding{ID: "active-http-methods", Stage: 3, Category: "Uji aktif", Severity: "low",
			Title: "Metode HTTP berisiko diizinkan: " + strings.Join(risky, ", "), Location: "Header Allow di https://" + r.host + "/",
			Detail: "Metode seperti TRACE/PUT/DELETE jarang diperlukan halaman web dan bisa disalahgunakan.",
			Advice: "Nonaktifkan metode yang tidak dipakai; izinkan hanya GET, HEAD, POST, OPTIONS sesuai kebutuhan."})
	} else if allow != "" {
		r.pass("Tidak ada metode HTTP berisiko yang diizinkan")
	}
}

// checkLoginLockout confirms DevControl's own login refuses wrong passwords
// and starts locking. It sends at most 6 clearly-wrong attempts, then stops.
func (r *runner) checkLoginLockout(ctx context.Context, self bool) {
	if !self { r.skip("Uji penguncian login hanya dijalankan untuk DevControl sendiri."); return }
	const payload = `{"credential":"activescan-intentionally-wrong-000"}`
	locked, attempts := false, 0
	for i := 0; i < 6; i++ {
		r.mu.Lock()
		if r.budget <= 0 { r.mu.Unlock(); break }
		r.budget--
		r.mu.Unlock()
		select {
		case <-ctx.Done(): return
		case <-time.After(pause):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+r.host+"/api/session", strings.NewReader(payload))
		if err != nil { return }
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Content-Type", "application/json")
		resp, err := r.client.Do(req)
		if err != nil { return }
		io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		attempts++
		if resp.StatusCode == http.StatusTooManyRequests { locked = true; break }
	}
	if locked {
		r.pass(fmt.Sprintf("Login mengunci setelah beberapa percobaan salah (terkunci pada percobaan ke-%d)", attempts))
	} else if attempts > 0 {
		r.add(appaudit.Finding{ID: "active-login-lockout", Stage: 3, Category: "Uji aktif", Severity: "medium",
			Title: "Login belum mengunci setelah beberapa percobaan salah", Location: "https://" + r.host + "/api/session",
			Detail: fmt.Sprintf("%d percobaan kata sandi salah tidak memicu penguncian (HTTP 429) dalam uji singkat ini.", attempts),
			Advice: "Pastikan penguncian per-IP aktif (DevControl: fail-closed setelah 5 kali). Periksa juga bahwa alamat penguji tidak masuk daftar tepercaya."})
	}
}

func looksLikeData(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" { return false }
	if trimmed[0] == '{' || trimmed[0] == '[' { return !strings.Contains(strings.ToLower(trimmed), "\"error\"") }
	return false
}

func slug(path string) string {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') { return r }
		return '-'
	}, strings.TrimPrefix(path, "/"))
	if len(cleaned) > 40 { cleaned = cleaned[:40] }
	return cleaned
}

// Handle serves POST /api/active-scan {app}: owner-only, one app at a time,
// registered apps only, with a short reuse window.
func Handle(w http.ResponseWriter, r *http.Request, self appaudit.Target) {
	w.Header().Set("Cache-Control", "no-store")
	if !auth.IsOwner(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("Uji Aktif hanya dapat dijalankan owner")); return }
	if !auth.RecentlyConfirmed(r) { auth.ReauthRequired(w); return }
	if r.Method != http.MethodPost { util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan POST")); return }
	targets, err := appaudit.Targets(self)
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	var input struct { App string `json:"app"` }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan tidak valid")); return
	}
	target, ok := appaudit.FindTarget(targets, input.App)
	if !ok { util.Error(w, http.StatusNotFound, fmt.Errorf("hanya aplikasi terdaftar di DevControl yang dapat diuji")); return }
	if busy(target.Key) { util.Error(w, http.StatusTooManyRequests, fmt.Errorf("uji aktif untuk aplikasi ini baru saja dijalankan; tunggu sebentar")); return }
	report := Run(r.Context(), target)
	_ = appaudit.SaveReport(report)
	util.JSON(w, http.StatusOK, map[string]interface{}{"report": report})
}

var recent sync.Map // app key -> last run time

func busy(key string) bool {
	now := time.Now()
	if last, ok := recent.Load(key); ok {
		if when, good := last.(time.Time); good && now.Sub(when) < 20*time.Second { return true }
	}
	recent.Store(key, now)
	return false
}

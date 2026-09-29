// Package diagnose turns a failed deployment (message + build log + the
// uploaded ZIP) into: where it failed, the likely cause, concrete fixes, and
// a ready-to-copy prompt the user can paste into any AI assistant.
// It is purely rule based: no network, no external service, fast and free.
package diagnose

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// Source tells the person where to act: "code" is their ZIP/source code,
// "config" is that app's own Vercel project settings (framework, root
// directory, env vars) — not DevControl's own credentials — and "platform"
// is DevControl's own token/permission/connectivity to GitHub, Vercel, or
// Cloudflare. "unknown" is an error pattern Source can't yet classify.
type Source string

const (
	SourceCode     Source = "code"
	SourceConfig   Source = "config"
	SourcePlatform Source = "platform"
	SourceUnknown  Source = "unknown"
)

type Diagnosis struct {
	Stage      string    `json:"stage"`
	Source     Source    `json:"source"`
	Category   string    `json:"category"`
	Summary    string    `json:"summary"`
	Location   *Location `json:"location,omitempty"`
	Cause      string    `json:"cause"`
	Fixes      []string  `json:"fixes"`
	Snippet    string    `json:"snippet,omitempty"`
	LogExcerpt string    `json:"log_excerpt,omitempty"`
	Stack      string    `json:"stack,omitempty"`
	Retryable  bool      `json:"retryable"`
	// LogMissing: the build failed but Vercel had not stored its log yet when
	// it was read. The source is decided once the log is read again.
	LogMissing bool      `json:"log_missing,omitempty"`
	Prompt     string    `json:"prompt"`
}

type Input struct {
	Kind     string // new_app | update_app | self_update
	Target   string
	Stage    string
	Message  string
	BuildLog string
	Zip      []byte // optional: the uploaded source, used for the code snippet
}

var (
	goLocation   = regexp.MustCompile(`(?m)((?:[\w.@-]+/)*[\w.@-]+\.go):(\d+):(\d+):\s*(.+)$`)
	jsLocation   = regexp.MustCompile(`((?:\.{1,2}/)?(?:[\w.@\[\]()-]+/)*[\w.@\[\]()-]+\.(?:tsx|ts|jsx|js|mjs|cjs|css|scss)):(\d+):(\d+)`)
	jsFileOnly   = regexp.MustCompile(`(?m)^\s*\.?/?((?:[\w.@\[\]()-]+/)+[\w.@\[\]()-]+\.(?:tsx|ts|jsx|js|mjs|cjs|css|scss))\s*$`)
	quotedModule = regexp.MustCompile(`(?:resolve|find module|find package)\s+'([^']+)'`)
	ansi         = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	lintPosition = regexp.MustCompile(`(?m)^\s*(\d+):(\d+)\s+(Error:.*)$`)
)

type rule struct {
	keys     []string
	all      bool // every key must be present instead of any
	source   Source
	category string
	cause    string
	fixes    []string
	retry    bool
}

var rules = []rule{
	{keys: []string{"log belum dapat dibaca", "log build kosong"}, source: SourceUnknown, category: "Log build Vercel belum tersedia",
		cause: "Build di Vercel berhenti dengan ERROR, tetapi Vercel belum menyimpan log-nya saat DevControl membacanya. Tanpa log, sumbernya belum bisa dipastikan; DevControl membaca ulang log ini secara otomatis.",
		fixes: []string{"Tunggu sebentar: diagnosis ini diperbarui otomatis begitu log terbaca, lengkap dengan sumber dan letak errornya.",
			"Bila tetap kosong, jalankan ulang deployment dengan ZIP yang sama; pembacaan log kini dicoba ulang otomatis."}, retry: true},
	{keys: []string{"halaman utama menampilkan 404", "http 404", "404 not_found"}, source: SourceConfig, category: "Hasil build tidak disajikan (404 NOT_FOUND)",
		cause: "Build selesai, tetapi Vercel tidak menemukan halaman pada URL deployment. Penyebabnya bisa framework/Root Directory/Output Directory yang tidak sesuai, project Git berbeda dari project import manual, atau alias mengarah ke deployment lama.",
		fixes: []string{"Pastikan package.json berada di akar ZIP dan mencantumkan framework-nya (vite, react-scripts, next, dll.) di dependencies/devDependencies.",
			"Pastikan package.json punya script \"build\" yang menghasilkan folder output (mis. vite build → dist/).",
			"Periksa nama project, koneksi GitHub dan VERCEL_TEAM_ID; di Vercel → Settings → Build and Deployment, samakan Framework, Root Directory dan Output Directory dengan project yang berhasil di-import manual.",
			"Untuk build khusus, tambahkan vercel.json di akar ZIP: { \"outputDirectory\": \"dist\" } (sesuaikan nama foldernya)."}},
	{keys: []string{"could not find an exported function"}, source: SourceCode, category: "Struktur fungsi Go di Vercel",
		cause: "Vercel menganggap setiap file .go langsung di folder /api sebagai fungsi terpisah, dan salah satunya tidak mengekspor handler.",
		fixes: []string{"Pastikan hanya ada satu file .go di /api yang berisi func Handler(w http.ResponseWriter, r *http.Request).",
			"Pindahkan fungsi pembantu ke folder pkg/<nama>/ lalu impor dari handler."}},
	{keys: []string{"missing go.sum entry", "no required module provides package", "go.mod file not found"}, source: SourceCode, category: "Dependensi Go",
		cause: "Paket Go yang diimpor tidak tercatat di go.mod/go.sum, atau path modul tidak cocok dengan nama module di go.mod.",
		fixes: []string{"Jalankan go mod tidy di akar proyek, lalu sertakan go.mod dan go.sum di ZIP.",
			"Pastikan path impor diawali nama module yang tertulis di baris pertama go.mod."}},
	{keys: []string{"redeclared"}, source: SourceCode, category: "Error kompilasi Go — nama ganda",
		cause: "Fungsi, tipe, atau variabel dengan nama yang sama dideklarasikan dua kali dalam satu package (sering di dua file berbeda).",
		fixes: []string{"Cari nama yang sama di semua file dalam package tersebut, lalu hapus atau ganti nama salah satunya."}},
	{keys: []string{"undefined:"}, source: SourceCode, category: "Error kompilasi Go — nama tidak dikenal",
		cause: "Kode memakai fungsi/variabel/tipe yang tidak ada: belum dibuat, salah ketik, package belum diimpor, atau namanya berhuruf kecil sehingga tidak diekspor dari package lain.",
		fixes: []string{"Periksa ejaan nama pada baris error.", "Jika berasal dari package lain, impor package itu dan pastikan namanya diawali huruf kapital.",
			"Jika fungsi belum ada, buat fungsinya atau kembalikan file yang terhapus."}},
	{keys: []string{"declared and not used", "declared but not used"}, source: SourceCode, category: "Error kompilasi Go — variabel tidak dipakai",
		cause: "Go menolak variabel lokal yang dideklarasikan tetapi tidak digunakan.",
		fixes: []string{"Hapus variabel tersebut, atau ganti namanya dengan _ bila nilainya memang diabaikan."}},
	{keys: []string{"imported and not used"}, source: SourceCode, category: "Error kompilasi Go — import tidak dipakai",
		cause: "Ada package yang diimpor tetapi tidak digunakan di file itu.",
		fixes: []string{"Hapus baris import yang disebut pada pesan error."}},
	{keys: []string{"cannot use", "mismatched types", "too many arguments", "not enough arguments", "missing return", "assignment mismatch"}, source: SourceCode, category: "Error kompilasi Go — tipe/argumen",
		cause: "Tipe data, jumlah argumen, atau nilai kembalian tidak sesuai dengan definisi fungsi.",
		fixes: []string{"Cocokkan pemanggilan fungsi dengan signature-nya (jumlah dan tipe parameter/return).", "Konversi tipe secara eksplisit bila perlu."}},
	{keys: []string{"syntax error"}, source: SourceCode, category: "Error sintaks",
		cause: "Ada kurung, kurawal, koma, atau kata kunci yang hilang/berlebih sehingga kode tidak bisa di-parse.",
		fixes: []string{"Periksa baris error dan satu-dua baris sebelumnya: biasanya kurung/kurawal belum ditutup atau koma hilang."}},
	{keys: []string{"type error:"}, source: SourceCode, category: "Error tipe TypeScript",
		cause: "TypeScript menemukan tipe yang tidak cocok, properti yang tidak ada, atau nilai yang mungkin undefined/null.",
		fixes: []string{"Buka file dan baris yang ditunjuk, sesuaikan tipe atau tambahkan pengecekan null/undefined.",
			"Jika properti belum ada di interface/type, tambahkan ke definisinya."}},
	{keys: []string{"eresolve", "could not resolve dependency", "conflicting peer dependency"}, source: SourceCode, category: "Konflik versi dependensi npm",
		cause: "Versi paket di package.json saling bertentangan (peer dependency).",
		fixes: []string{"Samakan versi paket yang disebut di log (mis. versi react dan paket yang memerlukannya).",
			"Alternatif cepat: tambahkan file .npmrc berisi legacy-peer-deps=true di akar proyek."}},
	{keys: []string{"npm err! 404", "etarget", "no matching version found", "is not in this registry"}, source: SourceCode, category: "Paket npm tidak ditemukan",
		cause: "Nama paket salah ketik atau versi yang diminta tidak ada di registry npm.",
		fixes: []string{"Periksa nama dan versi paket di package.json.", "Ganti ke versi yang tersedia (lihat npm view <paket> versions)."}},
	{keys: []string{"eslint", "react-hooks/", "no-unused-vars", "no-explicit-any"}, source: SourceCode, category: "Build dihentikan oleh ESLint",
		cause: "next build menjalankan ESLint dan menganggap pelanggaran aturan sebagai error.",
		fixes: []string{"Perbaiki baris yang disebut ESLint.", "Atau, sementara: tambahkan eslint: { ignoreDuringBuilds: true } di next.config.js."}},
	{keys: []string{"error occurred prerendering", "should be wrapped in a suspense boundary", "window is not defined", "document is not defined", "localstorage is not defined", "self is not defined", "navigator is not defined"}, source: SourceCode, category: "Kode browser berjalan di server (SSR/prerender)",
		cause: "Kode yang memakai window/document/localStorage dijalankan saat build di server, atau hook seperti useSearchParams tidak dibungkus Suspense.",
		fixes: []string{"Tambahkan \"use client\" di awal komponen dan pindahkan akses window/localStorage ke dalam useEffect.",
			"Bungkus komponen yang memakai useSearchParams dengan <Suspense>.", "Atau pakai dynamic(() => import(...), { ssr: false }) untuk komponen tersebut."}},
	{keys: []string{"heap out of memory", "sigkill", "exited with 137"}, source: SourceCode, category: "Kehabisan memori saat build",
		cause: "Proses build memakai memori melebihi batas mesin build Vercel.",
		fixes: []string{"Hapus dependensi besar yang tidak dipakai dan file besar (gambar/video) dari ZIP.", "Tambahkan environment NODE_OPTIONS=--max-old-space-size=4096 di project Vercel."}},
	{keys: []string{"no output directory", "could not find a production build", "couldn't find any `pages` or `app` directory", "missing script: \"build\"", "no package.json", "enoent: no such file or directory, open", "package.json"}, all: false, source: SourceCode, category: "Struktur ZIP / proyek",
		cause: "Vercel tidak menemukan proyek di akar ZIP (misalnya bersarang dua folder), package.json tidak ada, atau script build tidak didefinisikan.",
		fixes: []string{"Pastikan package.json (atau go.mod) berada di akar ZIP atau di satu folder teratas saja.",
			"Pastikan package.json punya \"build\": \"next build\" (atau perintah build yang sesuai).", "Jangan sertakan node_modules, .next, atau .git di ZIP."}},
	{keys: []string{"syntaxerror", "unexpected token", "unterminated", "expression expected", "expected \";\"", "expected '}'", "expected \"}\""}, source: SourceCode, category: "Error sintaks JavaScript/TypeScript",
		cause: "Ada tanda baca yang hilang/berlebih atau JSX yang tidak tertutup.",
		fixes: []string{"Periksa baris error dan beberapa baris di atasnya: tag JSX, kurung, kurawal, atau tanda kutip yang belum ditutup."}},
	{keys: []string{"environment variable", "missing env", "is not set", "process.env"}, source: SourceConfig, category: "Environment variable kosong",
		cause: "Aplikasi membutuhkan environment variable yang belum diisi di Vercel.",
		fixes: []string{"Isi variabel yang disebut di Vercel → Settings → Environment Variables, lalu jalankan ulang deployment.",
			"Untuk nilai yang dibaca browser, namanya harus diawali NEXT_PUBLIC_."}},
	{keys: []string{"bad credentials", "resource not accessible", "requires authentication"}, source: SourcePlatform, category: "Izin GITHUB_TOKEN",
		cause: "Token GitHub salah, kedaluwarsa, atau tidak punya izin menulis ke repo.",
		fixes: []string{"Buat ulang GITHUB_TOKEN dengan izin Contents: Read & Write (dan Administration untuk membuat/menghapus repo).", "Perbarui nilainya di Vercel lalu redeploy DevControl."}},
	{keys: []string{"invalid token", "not authorized", "forbidden", "missing scope"}, source: SourcePlatform, category: "Izin token (Vercel/Cloudflare)",
		cause: "Token tidak valid atau tidak memiliki izin untuk aksi ini.",
		fixes: []string{"Periksa VERCEL_TOKEN / CF_API_TOKEN beserta scope team-nya, buat ulang bila perlu, lalu redeploy DevControl."}},
	{keys: []string{"rate limit", "http 429", "timeout", "deadline exceeded", "connection reset", "http 502", "http 503", "unexpected eof", "tls handshake"}, source: SourcePlatform, category: "Gangguan sementara layanan",
		cause: "GitHub, Vercel, atau Cloudflare sedang membatasi/lambat merespons. Kode Anda kemungkinan tidak bermasalah.",
		fixes: []string{"Tunggu 1–5 menit, lalu jalankan ulang deployment dengan ZIP yang sama."}, retry: true},
	{keys: []string{"respons tahap sebelumnya tidak diketahui", "tidak aktif atau sudah kedaluwarsa", "status vercel gagal diperiksa berulang"}, source: SourcePlatform, category: "Proses terputus",
		cause: "Runner kehilangan jejak tahap terakhir (fungsi berhenti di tengah aksi). Kode belum tentu salah.",
		fixes: []string{"Buka GitHub dan Vercel untuk memastikan kondisi terakhir repo/deployment.", "Jalankan ulang deployment dengan ZIP yang sama."}, retry: true},
	{keys: []string{"check vercel tidak muncul", "integrasi github"}, source: SourceConfig, category: "Integrasi GitHub–Vercel",
		cause: "Commit sudah masuk GitHub, tetapi Vercel tidak memulai deployment production untuk repo/branch ini.",
		fixes: []string{"Pastikan project Vercel terhubung ke repo dan branch yang sama (Settings → Git).", "Pastikan branch tersebut adalah Production Branch di Vercel."}},
	{keys: []string{"4 mib", "4 mb", "melampaui batas"}, source: SourceCode, category: "Ukuran ZIP",
		cause: "ZIP melebihi batas 4 MiB.",
		fixes: []string{"Keluarkan node_modules, .next, .git, build output, dan media besar dari ZIP.", "Simpan gambar besar di penyimpanan terpisah (R2/CDN)."}},
	{keys: []string{"bukan zip", "zip tidak valid", "zip rusak", "not a valid zip"}, source: SourceCode, category: "File ZIP tidak valid",
		cause: "File yang diunggah rusak atau bukan arsip ZIP standar.",
		fixes: []string{"Kompres ulang folder proyek sebagai .zip biasa (bukan .rar/.7z), lalu unggah lagi."}},
}

// Hints for errors no specific rule recognised. Order matters in classify:
// DevControl's own service messages first, then evidence of the project's
// own build failing.
var platformHints = []string{
	"gagal memeriksa status vercel", "penyiapan d1/r2", "zip tidak dapat disimpan", "arsip zip wajib tersimpan",
	"runner tidak dapat", "runner cloudflare", "sesi deployment tidak valid", "sesi pemantauan build",
	"api.vercel.com", "api.github.com", "api.cloudflare.com", "http 401", "http 403", "http 500", "http 504",
	"gagal membaca akun github", "workers scripts edit",
}

var buildHints = []string{
	"exited with 1", "exited with 2", "exited with code 1", "build failed", "failed to compile", "compilation failed",
	"npm err!", "npm error", "yarn error", "pnpm err", "error: command", "go: ", "# devcontrol", "✗", "error ts",
	"error:", "err!",
}

// classify decides where to act when no specific rule matched: a file of
// the ZIP named in the error, or the project's own build command failing,
// points at the uploaded code; DevControl's messages about its services
// point at DevControl; otherwise the stage the run stopped at decides.
func classify(d Diagnosis, message, buildLog string) Source {
	lower := strings.ToLower(message + "\n" + buildLog)
	if strings.Contains(lower, "log belum dapat dibaca") || strings.Contains(lower, "log build kosong") { return SourceUnknown }
	for _, hint := range platformHints { if strings.Contains(lower, hint) { return SourcePlatform } }
	// A file:line counts only when it was found in the ZIP (snippet read) or
	// came from Vercel's build output, never from DevControl's own messages.
	if d.Location != nil && d.Location.File != "" && (d.Snippet != "" || strings.TrimSpace(buildLog) != "") { return SourceCode }
	stage := strings.ToLower(d.Stage)
	buildStage := strings.Contains(stage, "uji build") || strings.Contains(stage, "online") || strings.Contains(stage, "production")
	// Build-output wording is trusted from Vercel's build log, or from the
	// message only when the run stopped while Vercel was building.
	buildText := strings.ToLower(buildLog)
	if strings.TrimSpace(buildText) == "" && buildStage { buildText = lower }
	for _, hint := range buildHints { if buildText != "" && strings.Contains(buildText, hint) { return SourceCode } }
	switch {
	case strings.Contains(stage, "uji build") && strings.TrimSpace(buildLog) != "":
		// Vercel ran the project's own build and it produced output.
		return SourceCode
	case strings.Contains(stage, "github"), strings.Contains(stage, "simpan"):
		// Pushing to GitHub and storing the ZIP are DevControl's own steps.
		return SourcePlatform
	}
	return SourceUnknown
}

func logMissing(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "log belum dapat dibaca") || strings.Contains(lower, "log build kosong")
}

// Reclassify fills in the source of a diagnosis stored by an older version
// (before sources existed) or one left unknown, without any network call.
func Reclassify(d *Diagnosis) {
	if d == nil { return }
	if d.Source == "" || d.Source == SourceUnknown {
		for _, candidate := range rules {
			if candidate.category == d.Category && candidate.source != SourceUnknown { d.Source = candidate.source; break }
		}
	}
	if d.Source == "" || d.Source == SourceUnknown { d.Source = classify(*d, d.Summary, d.LogExcerpt) }
	if !d.LogMissing { d.LogMissing = logMissing(d.Summary + "\n" + d.LogExcerpt) }
}

func clean(text string) string {
	text = ansi.ReplaceAllString(text, "")
	return strings.ReplaceAll(text, "\r", "")
}

func truncate(text string, limit int) string {
	if len(text) <= limit { return text }
	return text[:limit] + "…"
}

func tail(text string, lines, limit int) string {
	parts := strings.Split(strings.TrimSpace(text), "\n")
	if len(parts) > lines { parts = parts[len(parts)-lines:] }
	out := strings.Join(parts, "\n")
	if len(out) > limit { out = out[len(out)-limit:] }
	return out
}

// firstOwn skips frames inside dependencies and build output.
func firstOwn(matches [][]string) []string {
	for _, match := range matches {
		if !strings.Contains(match[1], "node_modules/") && !strings.Contains(match[1], ".next/") && !strings.Contains(match[1], "/go/src/") && !strings.Contains(match[1], "/pkg/mod/") {
			return match
		}
	}
	return nil
}

func findLocation(text string) (*Location, string) {
	if match := firstOwn(goLocation.FindAllStringSubmatch(text, 20)); match != nil {
		line, _ := strconv.Atoi(match[2])
		column, _ := strconv.Atoi(match[3])
		return &Location{File: strings.TrimPrefix(match[1], "./"), Line: line, Column: column}, strings.TrimSpace(match[4])
	}
	if match := firstOwn(jsLocation.FindAllStringSubmatch(text, 20)); match != nil {
		line, _ := strconv.Atoi(match[2])
		column, _ := strconv.Atoi(match[3])
		return &Location{File: strings.TrimPrefix(strings.TrimPrefix(match[1], "./"), "/"), Line: line, Column: column}, ""
	}
	if bounds := jsFileOnly.FindStringSubmatchIndex(text); bounds != nil {
		location := &Location{File: text[bounds[2]:bounds[3]]}
		// ESLint prints the file on its own line, then "12:5  Error: ...".
		if match := lintPosition.FindStringSubmatch(text[bounds[1]:]); match != nil {
			location.Line, _ = strconv.Atoi(match[1])
			location.Column, _ = strconv.Atoi(match[2])
			return location, strings.TrimSpace(match[3])
		}
		return location, ""
	}
	return nil, ""
}

// summaryLine picks the most informative error line from the log.
func summaryLine(text string) string {
	lines := strings.Split(text, "\n")
	markers := []string{"type error:", "module not found", "cannot find module", "error:", "err!", "failed to compile", "syntaxerror", "undefined:", "error"}
	for _, marker := range markers {
		for _, raw := range lines {
			line := strings.TrimSpace(raw)
			lower := strings.ToLower(line)
			if line == "" || lower == "error" || strings.HasPrefix(lower, "error: command") || strings.Contains(lower, "exited with") { continue }
			if strings.Contains(lower, marker) { return truncate(line, 300) }
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" { return truncate(line, 300) }
	}
	return ""
}

type zipIndex struct {
	files map[string]*zip.File
	names []string
}

func openZip(data []byte) *zipIndex {
	if len(data) == 0 { return nil }
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil { return nil }
	index := &zipIndex{files: map[string]*zip.File{}}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() { continue }
		name := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(file.Name, "\\", "/")), "/")
		index.files[name] = file
		index.names = append(index.names, name)
	}
	return index
}

// find matches a path from the build machine to a ZIP entry by suffix, so
// "/vercel/path0/app/page.tsx" and "project/app/page.tsx" both resolve.
func (z *zipIndex) find(file string) (string, *zip.File) {
	if z == nil || file == "" { return "", nil }
	parts := strings.Split(strings.Trim(file, "/"), "/")
	for start := 0; start < len(parts); start++ {
		suffix := strings.Join(parts[start:], "/")
		for _, name := range z.names {
			if name == suffix || strings.HasSuffix(name, "/"+suffix) { return name, z.files[name] }
		}
		for _, name := range z.names { // Linux is case-sensitive; flag near matches too
			if strings.EqualFold(name, suffix) || strings.HasSuffix(strings.ToLower(name), "/"+strings.ToLower(suffix)) { return name, z.files[name] }
		}
	}
	return "", nil
}

func snippet(file *zip.File, line int) string {
	if file == nil || file.UncompressedSize64 > 2<<20 { return "" }
	reader, err := file.Open()
	if err != nil { return "" }
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 2<<20))
	if err != nil { return "" }
	lines := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
	if line <= 0 {
		if len(lines) > 25 { lines = lines[:25] }
		return truncate(strings.Join(lines, "\n"), 2000)
	}
	from, to := line-7, line+6
	if from < 1 { from = 1 }
	if to > len(lines) { to = len(lines) }
	var out strings.Builder
	for number := from; number <= to; number++ {
		mark := "  "
		if number == line { mark = "▶ " }
		fmt.Fprintf(&out, "%s%4d | %s\n", mark, number, lines[number-1])
	}
	return truncate(strings.TrimRight(out.String(), "\n"), 2000)
}

func (z *zipIndex) stack() string {
	if z == nil { return "" }
	found := []string{}
	add := func(item string) { for _, existing := range found { if existing == item { return } }; found = append(found, item) }
	for _, name := range z.names {
		base := path.Base(name)
		switch {
		case strings.HasPrefix(base, "next.config."): add("Next.js")
		case strings.HasPrefix(base, "vite.config."): add("Vite")
		case base == "go.mod": add("Go")
		case strings.HasPrefix(base, "tailwind.config."): add("Tailwind CSS")
		case base == "tsconfig.json": add("TypeScript")
		case base == "requirements.txt": add("Python")
		}
	}
	return strings.Join(found, ", ")
}

func fence(file string) string {
	switch strings.TrimPrefix(path.Ext(file), ".") {
	case "go": return "go"
	case "tsx", "ts": return "tsx"
	case "jsx", "js", "mjs", "cjs": return "jsx"
	case "css", "scss": return "css"
	}
	return ""
}

func kindLabel(kind string) string {
	switch kind {
	case "new_app": return "aplikasi baru"
	case "update_app": return "update aplikasi"
	case "self_update": return "update diri DevControl"
	}
	return "deployment"
}

// Analyze never fails: an unknown error still yields a useful prompt.
func Analyze(in Input) Diagnosis {
	message, buildLog := clean(in.Message), clean(in.BuildLog)
	text := strings.TrimSpace(message + "\n" + buildLog)
	lower := strings.ToLower(text)
	d := Diagnosis{Stage: in.Stage, Fixes: []string{}}
	if d.Stage == "" { d.Stage = "Tidak diketahui" }

	location, goMessage := findLocation(buildLog)
	if location == nil { location, goMessage = findLocation(message) }
	d.Summary = goMessage
	if d.Summary == "" { d.Summary = summaryLine(buildLog) }
	if d.Summary == "" { d.Summary = truncate(strings.TrimSpace(message), 300) }

	matched := false
	if module := quotedModule.FindStringSubmatch(text); module != nil && (strings.Contains(lower, "module not found") || strings.Contains(lower, "can't resolve") || strings.Contains(lower, "cannot find module") || strings.Contains(lower, "cannot find package")) {
		name := module[1]
		matched = true
		d.Source = SourceCode
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@/") || strings.HasPrefix(name, "~/") {
			d.Category = "File impor tidak ditemukan"
			d.Cause = fmt.Sprintf("Impor %q menunjuk ke file yang tidak ada di ZIP, salah path, atau beda huruf besar/kecil (server build Linux membedakan huruf besar/kecil).", name)
			d.Fixes = []string{"Pastikan file yang diimpor ikut di dalam ZIP.", "Samakan huruf besar/kecil nama file dengan teks impornya persis.",
				"Untuk alias @/, pastikan tsconfig.json memiliki \"paths\": { \"@/*\": [\"./*\"] }."}
		} else {
			packageName := name
			if strings.HasPrefix(packageName, "@") {
				if parts := strings.SplitN(packageName, "/", 3); len(parts) >= 2 { packageName = parts[0] + "/" + parts[1] }
			} else { packageName = strings.SplitN(packageName, "/", 2)[0] }
			d.Category = "Paket npm belum terpasang"
			d.Cause = fmt.Sprintf("Kode mengimpor %q, tetapi paket %q tidak ada di dependencies package.json.", name, packageName)
			d.Fixes = []string{fmt.Sprintf("Tambahkan \"%s\" ke \"dependencies\" di package.json (npm install %s), lalu ZIP ulang.", packageName, packageName),
				"Jika package-lock.json ikut di ZIP, perbarui juga agar sinkron."}
		}
	}
	if !matched {
		for _, candidate := range rules {
			hit := candidate.all
			for _, key := range candidate.keys {
				present := strings.Contains(lower, key)
				if candidate.all { hit = hit && present } else if present { hit = true; break }
			}
			if !hit { continue }
			// "package.json" alone is too broad; require a missing/not-found context.
			if candidate.category == "Struktur ZIP / proyek" && !strings.Contains(lower, "no package.json") &&
				!strings.Contains(lower, "no output directory") && !strings.Contains(lower, "production build") &&
				!strings.Contains(lower, "`pages` or `app`") && !strings.Contains(lower, "missing script") &&
				!(strings.Contains(lower, "enoent") && strings.Contains(lower, "package.json")) { continue }
			d.Source, d.Category, d.Cause, d.Fixes, d.Retryable = candidate.source, candidate.category, candidate.cause, append([]string{}, candidate.fixes...), candidate.retry
			matched = true
			break
		}
	}
	if !matched {
		d.Source = SourceUnknown
		d.Category = "Error belum dikenali otomatis"
		d.Cause = "Pola error ini belum ada di daftar DevControl. Log dan potongan kode di bawah biasanya cukup bagi AI untuk menemukan penyebabnya."
		d.Fixes = []string{"Salin prompt di bawah ke AI, lalu terapkan perbaikannya ke ZIP dan unggah ulang."}
	}

	index := openZip(in.Zip)
	d.Stack = index.stack()
	if location != nil {
		if name, file := index.find(location.File); file != nil {
			if !strings.EqualFold(name, location.File) && !strings.HasSuffix(name, location.File) {
				d.Fixes = append(d.Fixes, fmt.Sprintf("Catatan: nama file di ZIP adalah %q — periksa huruf besar/kecil.", name))
			}
			location.File = name
			d.Snippet = snippet(file, location.Line)
		}
		d.Location = location
	}
	if buildLog != "" { d.LogExcerpt = tail(buildLog, 40, 3500) } else { d.LogExcerpt = tail(message, 20, 1500) }
	if d.Source == "" || d.Source == SourceUnknown { d.Source = classify(d, message, buildLog) }
	d.LogMissing = logMissing(message)
	if !matched || d.Category == "Error belum dikenali otomatis" {
		switch d.Source {
		case SourceCode:
			d.Category = "Error build aplikasi (pola belum dikenali)"
			d.Cause = "Error muncul dari build proyek di ZIP (kode, dependensi, atau konfigurasi build-nya), tetapi jenis error spesifiknya belum ada di daftar DevControl. Baris error di log dan potongan kode di bawah menunjukkan letaknya."
		case SourcePlatform:
			d.Category = "Error layanan DevControl (pola belum dikenali)"
			d.Cause = "Error muncul saat DevControl berkomunikasi dengan GitHub, Vercel, Cloudflare, atau penyimpanan D1/R2 — bukan dari kode di ZIP. Jenis error spesifiknya belum ada di daftar DevControl."
			d.Fixes = []string{"Periksa token dan izin DevControl (GITHUB_TOKEN, VERCEL_TOKEN, CF_API_TOKEN) di Vercel.", "Jika tidak ada yang berubah, tunggu beberapa menit lalu jalankan ulang dengan ZIP yang sama."}
		}
	}
	d.Prompt = buildPrompt(in, d)
	return d
}

func buildPrompt(in Input, d Diagnosis) string {
	var b strings.Builder
	target := in.Target
	if target == "" { target = "(tanpa nama)" }
	fmt.Fprintf(&b, "Saya menjalankan %s untuk \"%s\" (di-deploy ke Vercel lewat GitHub) dan gagal pada tahap: %s.\n", kindLabel(in.Kind), target, d.Stage)
	if d.Stack != "" { fmt.Fprintf(&b, "Stack proyek: %s.\n", d.Stack) }
	fmt.Fprintf(&b, "\nError utama:\n%s\n", d.Summary)
	if d.Location != nil {
		where := d.Location.File
		if d.Location.Line > 0 { where += fmt.Sprintf(" baris %d", d.Location.Line) }
		if d.Location.Column > 0 { where += fmt.Sprintf(" kolom %d", d.Location.Column) }
		fmt.Fprintf(&b, "\nLokasi: %s\n", where)
	}
	if d.Snippet != "" {
		fmt.Fprintf(&b, "\nKode di sekitar baris error (▶ = baris error):\n```%s\n%s\n```\n", fence(d.Location.File), d.Snippet)
	}
	if d.LogExcerpt != "" { fmt.Fprintf(&b, "\nLog build (bagian akhir):\n```\n%s\n```\n", d.LogExcerpt) }
	fmt.Fprintf(&b, "\nDugaan awal (%s): %s\n", d.Category, d.Cause)
	b.WriteString("\nTolong:\n1. Jelaskan penyebab pastinya secara singkat.\n")
	b.WriteString("2. Beri perbaikan minimal (patch-only): ubah hanya file yang berhubungan dengan error ini, jangan menyentuh file lain.\n")
	b.WriteString("3. Tulis isi lengkap setiap file yang diubah beserta path-nya, supaya bisa langsung saya ganti.\n")
	b.WriteString("4. Pastikan hasilnya lolos build di Vercel (next build / go build) dan sebutkan jika ada environment variable atau dependensi yang perlu ditambahkan.\n")
	return b.String()
}

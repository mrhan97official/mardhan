// Package reposync decides, file by file, how a ZIP maps onto an existing
// GitHub repo: which ZIP entries are never published (OS junk, output of
// npm install / builds, secret .env files), and which repo files that are
// missing from the ZIP may be deleted as unused — and which must be kept
// (lockfiles, CI/Git metadata, licence) even though the ZIP omits them.
package reposync

import (
	"path"
	"sort"
	"strings"
)

// Directories created by package managers or builds. They are rebuilt by
// Vercel on every deploy and never belong in a ZIP or a Git repo.
var generatedDirs = map[string]bool{
	"node_modules": true, ".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true, ".vercel": true,
	".parcel-cache": true, ".cache": true, ".output": true, "coverage": true, ".nyc_output": true,
	"__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, ".venv": true, "venv": true, ".git": true,
	".angular": true, ".expo": true, ".docusaurus": true, "storybook-static": true, ".wrangler": true,
}

var generatedFiles = map[string]bool{
	"tsconfig.tsbuildinfo": true, "next-env.d.ts": false, // next-env.d.ts is regenerated but harmless; keep it
	"npm-debug.log": true, "yarn-error.log": true, "pnpm-debug.log": true, "lerna-debug.log": true,
}

// Lockfiles are produced by `npm install` & co. They are kept in the repo
// when the ZIP omits them, because deleting one silently changes which
// dependency versions Vercel installs.
var lockfiles = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"bun.lockb": true, "bun.lock": true, "go.sum": true, "composer.lock": true, "Gemfile.lock": true,
	"poetry.lock": true, "Pipfile.lock": true, "Cargo.lock": true, "uv.lock": true,
}

// Repo metadata that ZIPs commonly leave out but the project still needs.
var protectedDirs = map[string]bool{".github": true, ".devcontainer": true, ".husky": true, ".changeset": true}
var protectedFiles = map[string]bool{
	".gitignore": true, ".gitattributes": true, ".gitmodules": true, ".editorconfig": true, ".nvmrc": true,
	".node-version": true, ".npmrc": true, ".yarnrc.yml": true, ".tool-versions": true, "CODEOWNERS": true,
}

func segments(file string) []string { return strings.Split(strings.Trim(file, "/"), "/") }

// IsJunk: operating-system clutter from zipping on macOS/Windows.
func IsJunk(file string) bool {
	parts := segments(file)
	base := parts[len(parts)-1]
	return parts[0] == "__MACOSX" || base == ".DS_Store" || base == "Thumbs.db" || base == "desktop.ini" || strings.HasPrefix(base, "._")
}

// IsGenerated: output of npm install / builds (checked per path segment,
// so apps/web/node_modules/... in a monorepo is caught too).
func IsGenerated(file string) bool {
	parts := segments(file)
	for _, part := range parts[:len(parts)-1] {
		if generatedDirs[part] { return true }
	}
	base := parts[len(parts)-1]
	return generatedFiles[base] || strings.HasSuffix(base, ".pyc")
}

// IsSecretEnv: real .env files. Templates (.env.example etc.) are fine.
func IsSecretEnv(file string) bool {
	base := path.Base(file)
	if base != ".env" && !strings.HasPrefix(base, ".env.") { return false }
	for _, suffix := range []string{".example", ".sample", ".template", ".dist", ".defaults"} {
		if strings.HasSuffix(base, suffix) { return false }
	}
	return true
}

func isLockfile(file string) bool { return lockfiles[path.Base(file)] }

func isProtected(file string) bool {
	parts := segments(file)
	if protectedDirs[parts[0]] { return true }
	base := parts[len(parts)-1]
	if len(parts) == 1 && protectedFiles[base] { return true }
	upper := strings.ToUpper(base)
	return len(parts) == 1 && (strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "LICENCE") || strings.HasPrefix(upper, "COPYING"))
}

// Plan is what happens to repo files that are not in the ZIP.
type Plan struct {
	Delete        []string `json:"delete"`         // unused source files → deleted
	Generated     []string `json:"generated"`      // npm/build output committed by mistake → deleted
	Kept          []string `json:"kept"`           // protected (lockfile, CI, licence, …) → kept
	SecretsInRepo []string `json:"secrets_in_repo"` // .env files already in the repo → kept, but flagged
	Blocked       bool     `json:"blocked"`        // mass-deletion guard tripped → nothing deleted
	Reason        string   `json:"reason,omitempty"`
	SkippedSecrets []string `json:"skipped_secrets"` // .env files in the ZIP: deployed, never pushed
}

// PlanSync compares the ZIP (already filtered) with the repo's file list.
func PlanSync(zipPaths, repoPaths []string) Plan {
	inZip := make(map[string]bool, len(zipPaths))
	lockDirs := map[string]bool{} // directories whose ZIP brings its own lockfile
	for _, file := range zipPaths {
		inZip[file] = true
		if isLockfile(file) { lockDirs[path.Dir(file)] = true }
	}
	plan := Plan{Delete: []string{}, Generated: []string{}, Kept: []string{}, SecretsInRepo: []string{}}
	source := 0
	for _, file := range repoPaths {
		if !IsGenerated(file) && !IsJunk(file) { source++ }
		if inZip[file] { continue }
		switch {
		case IsGenerated(file) || IsJunk(file):
			plan.Generated = append(plan.Generated, file)
		case IsSecretEnv(file):
			plan.SecretsInRepo = append(plan.SecretsInRepo, file)
		case isLockfile(file) && !lockDirs[path.Dir(file)]:
			plan.Kept = append(plan.Kept, file) // ZIP has no lockfile here: keep the pinned versions
		case isLockfile(file):
			plan.Delete = append(plan.Delete, file) // replaced by another package manager's lockfile
		case isProtected(file):
			plan.Kept = append(plan.Kept, file)
		default:
			plan.Delete = append(plan.Delete, file)
		}
	}
	// A ZIP of the wrong folder would wipe the repo. If more than half of the
	// source files (and over 10) would go, delete nothing and ask for a check.
	if len(plan.Delete) > 10 && source > 0 && len(plan.Delete)*2 > source {
		plan.Blocked = true
		plan.Reason = "lebih dari separuh file sumber di GitHub tidak ada di ZIP; periksa apakah ZIP berisi folder proyek yang benar"
	}
	for _, list := range [][]string{plan.Delete, plan.Generated, plan.Kept, plan.SecretsInRepo} { sort.Strings(list) }
	return plan
}

// Removed lists every path the commit will actually delete.
func (p Plan) Removed() []string {
	if p.Blocked { return append([]string{}, p.Generated...) }
	return append(append([]string{}, p.Delete...), p.Generated...)
}

// SecretsNote explains which .env files were kept out of GitHub.
func (p Plan) SecretsNote() string {
	if len(p.SkippedSecrets) == 0 { return "" }
	return "File rahasia tidak didorong ke GitHub (" + preview(p.SkippedSecrets) + "); isi nilainya di Environment Variables Vercel"
}

func preview(list []string) string {
	if len(list) <= 5 { return strings.Join(list, ", ") }
	return strings.Join(list[:5], ", ") + ", …"
}

// Summary is a short human note stored with the update history.
func (p Plan) Summary() string {
	notes := []string{}
	if note := p.SecretsNote(); note != "" { notes = append(notes, note) }
	if removed := p.Removed(); len(removed) > 0 {
		notes = append(notes, "GitHub: "+itoa(len(removed))+" file tidak dipakai dihapus ("+preview(removed)+")")
	}
	if p.Blocked { notes = append(notes, "Penghapusan file lama dibatalkan: "+p.Reason) }
	if len(p.Kept) > 0 { notes = append(notes, "Dipertahankan: "+preview(p.Kept)) }
	if len(p.SecretsInRepo) > 0 {
		notes = append(notes, "Peringatan: file rahasia ada di repo ("+preview(p.SecretsInRepo)+"); pindahkan ke Environment Variables")
	}
	return strings.Join(notes, " · ")
}

func itoa(value int) string {
	if value == 0 { return "0" }
	digits := []byte{}
	for value > 0 { digits = append([]byte{byte('0' + value%10)}, digits...); value /= 10 }
	return string(digits)
}

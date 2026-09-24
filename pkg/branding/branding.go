// Package branding keeps the current PWA icon in private R2 and serves only
// generated PNG variants publicly for favicons and installation metadata.
package branding

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"strings"

	"devcontrol/pkg/archive"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
)

var iconNames = []string{"192.png", "512.png", "maskable.png"}

func validVersion(version string) bool {
	return len(version) == 32 && strings.Trim(version, "0123456789abcdef") == ""
}

func currentVersion() (string, error) {
	rows, err := d1.Query(`SELECT version FROM app_branding WHERE id = 1 LIMIT 1`)
	if err != nil || len(rows) == 0 { return "", err }
	version, _ := rows[0]["version"].(string)
	if !validVersion(version) { return "", fmt.Errorf("versi logo tersimpan tidak valid") }
	return version, nil
}

func iconKey(version, name string) string { return "branding/" + version + "/" + name }

func removeVersion(store *archive.Store, version string) {
	if !validVersion(version) { return }
	for _, name := range iconNames { _ = store.DeleteBranding(iconKey(version, name)) }
}

func readIcon(r *http.Request, name string, size int) ([]byte, error) {
	file, info, err := r.FormFile(name)
	if err != nil { return nil, fmt.Errorf("ikon %s belum dipilih", name) }
	defer file.Close()
	if info.Size <= 0 || info.Size > 2<<20 { return nil, fmt.Errorf("hasil PNG %s terlalu besar", name) }
	data, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 2<<20 { return nil, fmt.Errorf("gagal membaca PNG %s", name) }
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil || decoded.Bounds().Dx() != size || decoded.Bounds().Dy() != size {
		return nil, fmt.Errorf("ikon %s harus PNG persegi berukuran %dx%d", name, size, size)
	}
	return data, nil
}

// Handle requires an admin session (enforced in the gateway).
func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET, POST, atau DELETE")); return
	}
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }
	previous, err := currentVersion()
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	if r.Method == http.MethodGet {
		util.JSON(w, http.StatusOK, map[string]string{"version": previous}); return
	}
	if r.Method == http.MethodDelete {
		if _, err := d1.Query(`DELETE FROM app_branding WHERE id = 1`); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if previous != "" {
			if store, storeErr := archive.New(); storeErr == nil { removeVersion(store, previous) }
		}
		util.JSON(w, http.StatusOK, map[string]string{"version": ""}); return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		util.Error(w, http.StatusBadRequest, fmt.Errorf("unggahan ikon tidak valid atau melebihi 4 MiB")); return
	}
	if r.MultipartForm != nil { defer r.MultipartForm.RemoveAll() }
	data := make([][]byte, len(iconNames))
	for i, name := range iconNames {
		size := 512
		if i == 0 { size = 192 }
		data[i], err = readIcon(r, strings.TrimSuffix(name, ".png"), size)
		if err != nil { util.Error(w, http.StatusBadRequest, err); return }
	}
	store, err := archive.New()
	if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil { util.Error(w, http.StatusInternalServerError, err); return }
	version := hex.EncodeToString(idBytes)
	for i, name := range iconNames {
		if err := store.SaveBranding(iconKey(version, name), data[i]); err != nil {
			removeVersion(store, version)
			util.Error(w, http.StatusBadGateway, err); return
		}
	}
	if _, err := d1.Query(`INSERT INTO app_branding (id, version) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET version = excluded.version, updated_at = CURRENT_TIMESTAMP`, version); err != nil {
		removeVersion(store, version)
		util.Error(w, http.StatusBadGateway, err); return
	}
	if previous != "" { removeVersion(store, previous) }
	util.JSON(w, http.StatusOK, map[string]string{"version": version})
}

// HandlePublic may be called without login. Read errors fall back to the
// checked-in icon, so installation and login still work during D1 outages.
func HandlePublic(w http.ResponseWriter, r *http.Request, resource string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	version, err := currentVersion()
	if err != nil { version = "" }
	if resource == "branding-manifest" {
		icons := []map[string]string{
			{"src": "/icons/icon.svg", "sizes": "any", "type": "image/svg+xml", "purpose": "any"},
			{"src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": "/icons/icon-512-maskable.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		}
		if version != "" {
			icons = []map[string]string{
				{"src": "/api/branding/icon?size=192&v=" + version, "sizes": "192x192", "type": "image/png", "purpose": "any"},
				{"src": "/api/branding/icon?size=512&v=" + version, "sizes": "512x512", "type": "image/png", "purpose": "any"},
				{"src": "/api/branding/icon?size=maskable&v=" + version, "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
			}
		}
		manifest := map[string]interface{}{
			"name": "DevControl – Infrastructure & Deployment Workspace", "short_name": "DevControl",
			"description": "Offline-first infrastructure, deployment and monitoring workspace.",
			"start_url": "/", "scope": "/", "display": "standalone", "orientation": "any",
			"background_color": "#080D17", "theme_color": "#080D17", "icons": icons,
		}
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet { _ = json.NewEncoder(w).Encode(manifest) }
		return
	}

	size := r.URL.Query().Get("size")
	defaults := map[string]string{
		"favicon": "/icons/favicon-32.png", "apple": "/icons/apple-touch-icon.png",
		"192": "/icons/icon-192.png", "512": "/icons/icon-512.png",
		"maskable": "/icons/icon-512-maskable.png",
	}
	fallback, ok := defaults[size]
	if !ok { util.Error(w, http.StatusBadRequest, fmt.Errorf("ukuran ikon tidak valid")); return }
	if version == "" { http.Redirect(w, r, fallback, http.StatusTemporaryRedirect); return }
	name := "192.png"
	if size == "512" { name = "512.png" }
	if size == "maskable" { name = "maskable.png" }
	store, err := archive.New()
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	data, err := store.ReadBranding(iconKey(version, name))
	if err != nil { util.Error(w, http.StatusBadGateway, err); return }
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet { _, _ = w.Write(data) }
}

// Package projectthumbnail indexes private project images and verifies direct R2 uploads.
package projectthumbnail

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"devcontrol/pkg/archive"
	"devcontrol/pkg/auth"
	"devcontrol/pkg/d1"
	"devcontrol/pkg/setup"
	"devcontrol/pkg/util"
)

const maxSingleUpload = int64(5) << 30 // R2 single-part PUT limit, not an application MB cap.

func validRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 { return false }
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 100 { return false }
		for _, ch := range part {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
				(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') { return false }
		}
	}
	return true
}

func objectKey(repo string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(repo)))
	return "thumbnails/" + hex.EncodeToString(sum[:]) + ".img"
}

func uploadedKey(repo, id string) string {
	return strings.TrimSuffix(objectKey(repo), ".img") + "/" + id + ".img"
}

func validUploadedKey(repo, key string) bool {
	prefix := strings.TrimSuffix(objectKey(repo), ".img") + "/"
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, ".img") { return false }
	id := strings.TrimSuffix(strings.TrimPrefix(key, prefix), ".img")
	return validID(id)
}

func validID(id string) bool { return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == "" }

func imageType(data []byte) string {
	kind := http.DetectContentType(data)
	if kind == "image/jpeg" || kind == "image/png" { return kind }
	return ""
}

func currentKey(repo string, row map[string]interface{}) (string, error) {
	key, _ := row["object_key"].(string)
	if key == "" { return objectKey(repo), nil } // pre-v1.0.18 thumbnails
	if !validUploadedKey(repo, key) { return "", fmt.Errorf("kunci thumbnail tidak cocok dengan repo") }
	return key, nil
}

// HasActiveUploads prevents project deletion while a signed PUT can still
// finish. A closed tab is released when its upload URL expires.
func HasActiveUploads(repo string) (bool, error) {
	rows, err := d1.Query(`SELECT u.id FROM project_thumbnail_uploads u
		LEFT JOIN project_thumbnails t ON t.object_key = u.object_key
		WHERE u.repo = ? AND u.created_at > datetime('now', '-1 hour')
		AND t.repo IS NULL LIMIT 1`, repo)
	return len(rows) > 0, err
}

// Delete removes both the current image and pending objects for this repo.
// Old rows with no object_key retain their original deterministic R2 key.
func Delete(repo string, store *archive.Store) error {
	if !validRepo(repo) { return fmt.Errorf("nama repo tidak valid") }
	active, err := HasActiveUploads(repo)
	if err != nil { return err }
	if active { return fmt.Errorf("thumbnail sedang diunggah; tunggu sampai selesai sebelum menghapus") }
	objects, err := d1.Query(`SELECT object_key FROM project_thumbnail_objects WHERE repo = ?`, repo)
	if err != nil { return err }
	for _, row := range objects {
		key, _ := row["object_key"].(string)
		if key != objectKey(repo) && !validUploadedKey(repo, key) { return fmt.Errorf("kunci thumbnail tersimpan tidak valid") }
		if err := store.DeleteThumbnail(key); err != nil { return err }
		if _, err := d1.Query(`DELETE FROM project_thumbnail_objects WHERE object_key = ?`, key); err != nil { return err }
	}
	pending, err := d1.Query(`SELECT id, object_key FROM project_thumbnail_uploads WHERE repo = ?`, repo)
	if err != nil { return err }
	for _, row := range pending {
		id, _ := row["id"].(string)
		key, _ := row["object_key"].(string)
		if !validID(id) || key != uploadedKey(repo, id) { return fmt.Errorf("kunci unggahan thumbnail tidak valid") }
		if err := store.DeleteThumbnail(key); err != nil { return err }
		if _, err := d1.Query(`DELETE FROM project_thumbnail_uploads WHERE id = ?`, id); err != nil { return err }
	}
	rows, err := d1.Query(`SELECT object_key FROM project_thumbnails WHERE repo = ? LIMIT 1`, repo)
	if err != nil || len(rows) == 0 { return err }
	key, err := currentKey(repo, rows[0])
	if err != nil { return err }
	if err := store.DeleteThumbnail(key); err != nil { return err }
	_, err = d1.Query(`DELETE FROM project_thumbnails WHERE repo = ?`, repo)
	return err
}

func getUpload(id string) (map[string]interface{}, error) {
	if !validID(id) { return nil, fmt.Errorf("ID unggahan tidak valid") }
	rows, err := d1.Query(`SELECT id, repo, object_key, content_type, size_bytes FROM project_thumbnail_uploads WHERE id = ? LIMIT 1`, id)
	if err != nil { return nil, err }
	if len(rows) == 0 { return nil, fmt.Errorf("unggahan tidak ditemukan atau sudah dibatalkan") }
	return rows[0], nil
}

func cleanupPending(id, repo, key string, store *archive.Store) error {
	rows, err := d1.Query(`SELECT repo FROM project_thumbnails WHERE object_key = ? LIMIT 1`, key)
	if err != nil { return err }
	if len(rows) == 0 {
		if err := store.DeleteThumbnail(key); err != nil { return err }
		if _, err := d1.Query(`DELETE FROM project_thumbnail_objects WHERE object_key = ?`, key); err != nil { return err }
	}
	_, err = d1.Query(`DELETE FROM project_thumbnail_uploads WHERE id = ? AND repo = ?`, id, repo)
	return err
}

// Prune a few abandoned uploads per request, without ever removing the
// currently displayed image if commit succeeded but pending cleanup failed.
func pruneExpired(store *archive.Store) error {
	rows, err := d1.Query(`SELECT id, repo, object_key FROM project_thumbnail_uploads
		WHERE created_at < datetime('now', '-1 day') ORDER BY created_at LIMIT 10`)
	if err != nil { return err }
	for _, row := range rows {
		id, _ := row["id"].(string)
		repo, _ := row["repo"].(string)
		key, _ := row["object_key"].(string)
		if !validID(id) || !validRepo(repo) || key != uploadedKey(repo, id) { return fmt.Errorf("data unggahan lama tidak valid") }
		if err := cleanupPending(id, repo, key, store); err != nil { return err }
	}
	return nil
}

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !auth.IsAdmin(r) { util.Error(w, http.StatusForbidden, fmt.Errorf("sesi admin diperlukan")); return }
	if err := setup.Prepare(); err != nil { util.Error(w, http.StatusBadGateway, err); return }

	switch r.Method {
	case http.MethodGet:
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			rows, err := d1.Query(`SELECT repo, version FROM project_thumbnails ORDER BY repo`)
			if err != nil { util.Error(w, http.StatusBadGateway, err); return }
			if rows == nil { rows = []map[string]interface{}{} }
			util.JSON(w, http.StatusOK, rows)
			return
		}
		if !validRepo(repo) { util.Error(w, http.StatusBadRequest, fmt.Errorf("nama repo tidak valid")); return }
		rows, err := d1.Query(`SELECT object_key FROM project_thumbnails WHERE repo = ? LIMIT 1`, repo)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if len(rows) == 0 { util.Error(w, http.StatusNotFound, fmt.Errorf("thumbnail tidak ditemukan")); return }
		key, err := currentKey(repo, rows[0])
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		if key != objectKey(repo) {
			storage, err := newSignedStorage()
			if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
			location, err := storage.sign(http.MethodGet, key, "", 300)
			if err != nil { util.Error(w, http.StatusBadGateway, err); return }
			w.Header().Set("Location", location)
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		store, err := archive.New()
		if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
		data, err := store.ReadThumbnail(key)
		if err != nil { util.Error(w, http.StatusBadGateway, err); return }
		kind := imageType(data)
		if kind == "" { util.Error(w, http.StatusBadGateway, fmt.Errorf("format thumbnail tidak valid")); return }
		w.Header().Set("Content-Type", kind)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodPost:
		var input struct {
			Action string `json:"action"`
			Repo string `json:"repo"`
			ContentType string `json:"content_type"`
			SizeBytes int64 `json:"size_bytes"`
			UploadID string `json:"upload_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			util.Error(w, http.StatusBadRequest, fmt.Errorf("permintaan thumbnail tidak valid")); return
		}
		if input.Action == "begin" {
			if !validRepo(input.Repo) || (input.ContentType != "image/jpeg" && input.ContentType != "image/png") ||
				input.SizeBytes <= 0 || input.SizeBytes > maxSingleUpload {
				util.Error(w, http.StatusBadRequest, fmt.Errorf("gunakan JPG/PNG asli; batas unggah tunggal R2 adalah 5 GiB")); return
			}
			store, err := archive.New()
			if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
			storage, err := newSignedStorage()
			if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
			if err := pruneExpired(store); err != nil { util.Error(w, http.StatusBadGateway, err); return }
			idBytes := make([]byte, 16)
			if _, err := rand.Read(idBytes); err != nil { util.Error(w, http.StatusInternalServerError, err); return }
			id := hex.EncodeToString(idBytes)
			key := uploadedKey(input.Repo, id)
			location, err := storage.sign(http.MethodPut, key, input.ContentType, 900)
			if err != nil { util.Error(w, http.StatusBadGateway, err); return }
			if _, err := d1.Query(`INSERT INTO project_thumbnail_uploads (id, repo, object_key, content_type, size_bytes)
				VALUES (?, ?, ?, ?, ?)`, id, input.Repo, key, input.ContentType, input.SizeBytes); err != nil {
				util.Error(w, http.StatusBadGateway, err); return
			}
			if _, err := d1.Query(`INSERT INTO project_thumbnail_objects (repo, object_key) VALUES (?, ?)`, input.Repo, key); err != nil {
				_, _ = d1.Query(`DELETE FROM project_thumbnail_uploads WHERE id = ?`, id)
				util.Error(w, http.StatusBadGateway, err); return
			}
			util.JSON(w, http.StatusOK, map[string]string{"upload_id": id, "upload_url": location})
			return
		}
		if input.Action == "finish" {
			row, err := getUpload(input.UploadID)
			if err != nil { util.Error(w, http.StatusBadRequest, err); return }
			repo, _ := row["repo"].(string)
			key, _ := row["object_key"].(string)
			kind, _ := row["content_type"].(string)
			size, _ := row["size_bytes"].(float64)
			if !validRepo(repo) || key != uploadedKey(repo, input.UploadID) || (kind != "image/jpeg" && kind != "image/png") || size <= 0 || size > float64(maxSingleUpload) {
				util.Error(w, http.StatusBadGateway, fmt.Errorf("metadata unggahan tidak valid")); return
			}
			storage, err := newSignedStorage()
			if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
			if err := storage.verify(key, kind, int64(size)); err != nil { util.Error(w, http.StatusBadGateway, err); return }
			previous, err := d1.Query(`SELECT object_key FROM project_thumbnails WHERE repo = ? LIMIT 1`, repo)
			if err != nil { util.Error(w, http.StatusBadGateway, err); return }
			var old string
			if len(previous) > 0 {
				old, err = currentKey(repo, previous[0])
				if err != nil { util.Error(w, http.StatusBadGateway, err); return }
				if _, err := d1.Query(`INSERT OR IGNORE INTO project_thumbnail_objects (repo, object_key) VALUES (?, ?)`, repo, old); err != nil {
					util.Error(w, http.StatusBadGateway, err); return
				}
			}
			if _, err := d1.Query(`INSERT INTO project_thumbnails (repo, version, object_key) VALUES (?, ?, ?)
				ON CONFLICT(repo) DO UPDATE SET version = excluded.version, object_key = excluded.object_key,
				updated_at = CURRENT_TIMESTAMP`, repo, input.UploadID, key); err != nil {
				util.Error(w, http.StatusBadGateway, err); return
			}
			// A committed image is never removed by cancellation or stale-upload cleanup.
			_, _ = d1.Query(`DELETE FROM project_thumbnail_uploads WHERE id = ?`, input.UploadID)
			if old != "" && old != key {
				if store, storeErr := archive.New(); storeErr == nil && store.DeleteThumbnail(old) == nil {
					_, _ = d1.Query(`DELETE FROM project_thumbnail_objects WHERE object_key = ?`, old)
				}
			}
			util.JSON(w, http.StatusOK, map[string]string{"repo": repo, "version": input.UploadID})
			return
		}
		util.Error(w, http.StatusBadRequest, fmt.Errorf("aksi thumbnail tidak valid"))
	case http.MethodDelete:
		store, err := archive.New()
		if err != nil { util.Error(w, http.StatusPreconditionFailed, err); return }
		if id := r.URL.Query().Get("upload_id"); id != "" {
			row, err := getUpload(id)
			if err != nil { util.Error(w, http.StatusBadRequest, err); return }
			repo, _ := row["repo"].(string)
			key, _ := row["object_key"].(string)
			if !validRepo(repo) || key != uploadedKey(repo, id) { util.Error(w, http.StatusBadGateway, fmt.Errorf("kunci unggahan tidak valid")); return }
			if err := cleanupPending(id, repo, key, store); err != nil { util.Error(w, http.StatusBadGateway, err); return }
			util.JSON(w, http.StatusOK, map[string]bool{"cancelled": true})
			return
		}
		repo := r.URL.Query().Get("repo")
		if !validRepo(repo) { util.Error(w, http.StatusBadRequest, fmt.Errorf("nama repo tidak valid")); return }
		if err := Delete(repo, store); err != nil { util.Error(w, http.StatusBadGateway, err); return }
		util.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		util.Error(w, http.StatusMethodNotAllowed, fmt.Errorf("gunakan GET, POST, atau DELETE"))
	}
}

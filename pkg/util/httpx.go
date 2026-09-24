// Package util provides small shared helpers for the Vercel Go functions
// under /api so each handler stays focused on its query.
package util

import (
	"encoding/json"
	"net/http"
)

// JSON writes same-origin API responses without exposing credentials to other sites.
func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

// Error writes a JSON error body: {"error": "..."}.
func Error(w http.ResponseWriter, status int, err error) {
	JSON(w, status, map[string]string{"error": err.Error()})
}

// DevControl's browser UI is same-origin. Cross-origin preflight is denied.
func HandleCORSPreflight(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		Error(w, http.StatusForbidden, &forbiddenPreflight{})
		return true
	}
	return false
}

type forbiddenPreflight struct{}
func (*forbiddenPreflight) Error() string { return "akses lintas origin tidak diizinkan" }

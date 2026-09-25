// Package deploymentrunner installs the scheduled Cloudflare Worker which
// resumes ZIP deployments independently of the device that uploaded them.
package deploymentrunner

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const source = `export default {
  async scheduled(_event, env) {
    const job = await env.DB.prepare("SELECT j.id FROM deployment_jobs j JOIN deployment_runner r ON r.id = j.id WHERE j.status = 'Running' AND r.phase NOT IN ('done', 'error') AND r.claim_until <= CURRENT_TIMESTAMP LIMIT 1").first();
    if (!job) return;
    const response = await fetch(env.TARGET_URL + "/api/deployment-runner", {
      method: "POST", headers: { Authorization: "Bearer " + env.RUNNER_SECRET },
    });
    if (!response.ok) throw new Error("Deployment runner returned HTTP " + response.status);
  },
};`

func Name() string {
	sum := sha256.Sum256([]byte(os.Getenv("CF_D1_DATABASE_ID")))
	return "devcontrol-deploy-" + hex.EncodeToString(sum[:6])
}

// A distinct purpose-bound secret is sent to the Worker, not the admin password
// or the session signing key itself. Rotating the session key requires the next
// ZIP upload to refresh the Worker binding.
func Secret() string {
	mac := hmac.New(sha256.New, []byte(os.Getenv("DEVCONTROL_SESSION_SECRET")))
	_, _ = mac.Write([]byte("deployment-runner:" + os.Getenv("CF_D1_DATABASE_ID")))
	return hex.EncodeToString(mac.Sum(nil))
}

func Authorized(raw string) bool {
	if len(os.Getenv("DEVCONTROL_SESSION_SECRET")) < 32 || !strings.HasPrefix(raw, "Bearer ") { return false }
	want, got := Secret(), strings.TrimPrefix(raw, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func validateHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/:@?#\\ ") { return false }
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' { return false }
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') { return false }
		}
	}
	return true
}

func cloudflareCall(method, path, contentType string, body io.Reader) error {
	req, err := http.NewRequest(method, "https://api.cloudflare.com/client/v4/accounts/"+
		url.PathEscape(os.Getenv("CF_ACCOUNT_ID"))+path, body)
	if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+os.Getenv("CF_API_TOKEN"))
	if contentType != "" { req.Header.Set("Content-Type", contentType) }
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	var result struct {
		Success bool `json:"success"`
		Errors []struct { Message string `json:"message"` } `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&result); err != nil {
		return fmt.Errorf("respons Cloudflare HTTP %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 300 || !result.Success {
		message := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if len(result.Errors) > 0 { message = result.Errors[0].Message }
		return fmt.Errorf("%s", message)
	}
	return nil
}

// Ensure confirms the scheduled runner is installed before a ZIP is accepted.
// It uses the existing Cloudflare account and token, and needs Workers Scripts
// Edit permission in addition to the existing D1/R2 permissions.
func Ensure(host string) error {
	if !validateHost(host) || os.Getenv("CF_ACCOUNT_ID") == "" || os.Getenv("CF_D1_DATABASE_ID") == "" ||
		os.Getenv("CF_API_TOKEN") == "" || len(os.Getenv("DEVCONTROL_SESSION_SECRET")) < 32 {
		return fmt.Errorf("runner otomatis memerlukan domain produksi, D1, token Cloudflare, dan sesi admin yang lengkap")
	}
	name := Name()
	metadata, _ := json.Marshal(map[string]interface{}{
		"main_module": "runner.mjs", "compatibility_date": "2024-09-01",
		"bindings": []map[string]string{
			{"type": "plain_text", "name": "TARGET_URL", "text": "https://" + host},
			{"type": "secret_text", "name": "RUNNER_SECRET", "text": Secret()},
			{"type": "d1", "name": "DB", "id": os.Getenv("CF_D1_DATABASE_ID")},
		},
	})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="metadata"`},
		"Content-Type": {"application/json"},
	})
	if err != nil { return err }
	if _, err = part.Write(metadata); err != nil { return err }
	part, err = form.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="runner.mjs"; filename="runner.mjs"`},
		"Content-Type": {"application/javascript+module"},
	})
	if err != nil { return err }
	if _, err = part.Write([]byte(source)); err != nil { return err }
	if err = form.Close(); err != nil { return err }
	base := "/workers/scripts/" + name
	if err = cloudflareCall(http.MethodPut, base, form.FormDataContentType(), &body); err != nil {
		return fmt.Errorf("gagal memasang runner Cloudflare (%w); CF_API_TOKEN membutuhkan Workers Scripts Edit", err)
	}
	if err = cloudflareCall(http.MethodPut, base+"/schedules", "application/json",
		strings.NewReader(`[{"cron":"* * * * *"}]`)); err != nil {
		return fmt.Errorf("gagal mengaktifkan jadwal runner Cloudflare (%w); CF_API_TOKEN membutuhkan Workers Scripts Edit", err)
	}
	return nil
}

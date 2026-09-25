package projectthumbnail

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A browser uploads the original bytes to R2. Only short-lived, object-scoped
// SigV4 URLs leave this server; the access keys never reach the browser.
type signedStorage struct {
	account string
	bucket string
	accessKey string
	secretKey string
	client *http.Client
}

var verifiedR2Token struct {
	sync.Mutex
	digest [32]byte
	id string
	expires time.Time
}

// Cloudflare's R2 S3 access key is an API token's ID and its secret key is
// the SHA-256 hex digest of the token value. Resolve the ID server-side only;
// the bearer token and its derived secret are never returned to the browser.
func r2KeyFromToken(client *http.Client, account, token string) (string, string, error) {
	digest := sha256.Sum256([]byte(token))
	verifiedR2Token.Lock()
	defer verifiedR2Token.Unlock()
	if verifiedR2Token.digest == digest && time.Now().Before(verifiedR2Token.expires) {
		return verifiedR2Token.id, hex.EncodeToString(digest[:]), nil
	}
	endpoints := []string{
		"https://api.cloudflare.com/client/v4/user/tokens/verify",
		"https://api.cloudflare.com/client/v4/accounts/" + account + "/tokens/verify",
	}
	for _, endpoint := range endpoints {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil { return "", "", err }
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil { return "", "", fmt.Errorf("gagal memeriksa token Cloudflare: %w", err) }
		var result struct {
			Success bool `json:"success"`
			Result struct { ID string `json:"id"`; Status string `json:"status"` } `json:"result"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result)
		resp.Body.Close()
		if resp.StatusCode >= 300 || decodeErr != nil || !result.Success || result.Result.Status != "active" ||
			len(result.Result.ID) != 32 || strings.Trim(result.Result.ID, "0123456789abcdefABCDEF") != "" { continue }
		verifiedR2Token.digest, verifiedR2Token.id = digest, result.Result.ID
		verifiedR2Token.expires = time.Now().Add(10 * time.Minute)
		return result.Result.ID, hex.EncodeToString(digest[:]), nil
	}
	return "", "", fmt.Errorf("ID token Cloudflare tidak tersedia; buat token R2 khusus bucket lalu isi R2_ACCESS_KEY_ID dan R2_SECRET_ACCESS_KEY di Vercel")
}

func newSignedStorage() (*signedStorage, error) {
	s := &signedStorage{account: os.Getenv("CF_ACCOUNT_ID"), bucket: os.Getenv("CF_R2_BUCKET"),
		accessKey: os.Getenv("R2_ACCESS_KEY_ID"), secretKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		client: &http.Client{Timeout: 20 * time.Second}}
	if len(s.account) != 32 || strings.Trim(s.account, "0123456789abcdefABCDEF") != "" || s.bucket == "" {
		return nil, fmt.Errorf("unggah gambar besar memerlukan CF_ACCOUNT_ID dan CF_R2_BUCKET di Vercel")
	}
	if (s.accessKey == "") != (s.secretKey == "") {
		return nil, fmt.Errorf("isi R2_ACCESS_KEY_ID dan R2_SECRET_ACCESS_KEY bersama-sama di Vercel")
	}
	if s.accessKey == "" {
		token := os.Getenv("CF_API_TOKEN")
		if token == "" { return nil, fmt.Errorf("unggah gambar besar memerlukan CF_API_TOKEN dengan izin R2 atau kredensial R2_ACCESS_KEY_ID dan R2_SECRET_ACCESS_KEY") }
		var err error
		s.accessKey, s.secretKey, err = r2KeyFromToken(s.client, s.account, token)
		if err != nil { return nil, err }
	}
	return s, nil
}

// Add only this site's upload rule and preserve every existing rule, including
// Cloudflare fields we do not recognize yet.
func (s *signedStorage) ensureUploadCORS(origin string) error {
	apiToken := os.Getenv("CF_API_TOKEN")
	if origin == "" || apiToken == "" { return nil }
	endpoint := "https://api.cloudflare.com/client/v4/accounts/" + s.account + "/r2/buckets/" + url.PathEscape(s.bucket) + "/cors"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+apiToken)
	resp, err := s.client.Do(req)
	if err != nil { return fmt.Errorf("gagal memeriksa CORS R2: %w", err) }
	var result struct {
		Success bool `json:"success"`
		Result struct { Rules []json.RawMessage `json:"rules"` } `json:"result"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&result)
	resp.Body.Close()
	// An object-only token may not be allowed to read bucket configuration.
	// Its existing manually configured CORS rule can still serve uploads.
	if resp.StatusCode == http.StatusForbidden { return nil }
	if resp.StatusCode != http.StatusNotFound && (resp.StatusCode >= 300 || decodeErr != nil || !result.Success) {
		return fmt.Errorf("gagal memeriksa aturan CORS R2 (HTTP %d)", resp.StatusCode)
	}
	for _, raw := range result.Result.Rules {
		var rule struct { Allowed struct { Origins, Methods, Headers []string } `json:"allowed"` }
		if json.Unmarshal(raw, &rule) != nil { continue }
		if (containsString(rule.Allowed.Origins, origin) || containsString(rule.Allowed.Origins, "*")) &&
			containsString(rule.Allowed.Methods, "PUT") &&
			(containsStringFold(rule.Allowed.Headers, "Content-Type") || containsString(rule.Allowed.Headers, "*")) { return nil }
	}
	newRule, err := json.Marshal(map[string]interface{}{
		"allowed": map[string]interface{}{
			"origins": []string{origin}, "methods": []string{"PUT"}, "headers": []string{"Content-Type"},
		}, "maxAgeSeconds": 3600,
	})
	if err != nil { return err }
	rules := append(result.Result.Rules, newRule)
	body, err := json.Marshal(map[string]interface{}{"rules": rules})
	if err != nil { return err }
	req, err = http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = s.client.Do(req)
	if err != nil { return fmt.Errorf("gagal menyiapkan CORS R2: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode >= 300 { return fmt.Errorf("CORS R2 tidak dapat dibuat otomatis (HTTP %d); tambahkan izin PUT dan Content-Type untuk %s pada bucket %s", resp.StatusCode, origin, s.bucket) }
	var updated struct { Success bool `json:"success"` }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&updated); err != nil || !updated.Success {
		return fmt.Errorf("Cloudflare tidak mengonfirmasi aturan CORS R2 untuk %s", origin)
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values { if value == target { return true } }
	return false
}

func containsStringFold(values []string, target string) bool {
	for _, value := range values { if strings.EqualFold(value, target) { return true } }
	return false
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func (s *signedStorage) sign(method, key, contentType string, seconds int) (string, error) {
	if key == "" || strings.ContainsAny(key, " ?#%\\") || seconds < 1 || seconds > 3600 {
		return "", fmt.Errorf("parameter URL R2 tidak valid")
	}
	now := time.Now().UTC()
	date, stamp := now.Format("20060102"), now.Format("20060102T150405Z")
	scope := date + "/auto/s3/aws4_request"
	host := s.account + ".r2.cloudflarestorage.com"
	uri := "/" + url.PathEscape(s.bucket) + "/" + key
	signedHeaders := "host"
	headers := "host:" + host + "\n"
	if contentType != "" {
		signedHeaders = "content-type;host"
		headers = "content-type:" + contentType + "\n" + headers
	}
	query := url.Values{
		"X-Amz-Algorithm":      {"AWS4-HMAC-SHA256"},
		"X-Amz-Content-Sha256": {"UNSIGNED-PAYLOAD"},
		"X-Amz-Credential":     {s.accessKey + "/" + scope},
		"X-Amz-Date":           {stamp},
		"X-Amz-Expires":        {strconv.Itoa(seconds)},
		"X-Amz-SignedHeaders":  {signedHeaders},
	}
	canonical := method + "\n" + uri + "\n" + query.Encode() + "\n" + headers + "\n" + signedHeaders + "\nUNSIGNED-PAYLOAD"
	digest := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(digest[:])
	secret := hmacSHA256([]byte("AWS4"+s.secretKey), date)
	secret = hmacSHA256(secret, "auto")
	secret = hmacSHA256(secret, "s3")
	secret = hmacSHA256(secret, "aws4_request")
	query.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA256(secret, toSign)))
	return "https://" + host + uri + "?" + query.Encode(), nil
}

func (s *signedStorage) delete(key string) error {
	location, err := s.sign(http.MethodDelete, key, "", 60)
	if err != nil { return err }
	req, err := http.NewRequest(http.MethodDelete, location, nil)
	if err != nil { return err }
	resp, err := s.client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent { return fmt.Errorf("R2 S3 mengembalikan HTTP %d", resp.StatusCode) }
	return nil
}

func (s *signedStorage) verify(key, contentType string, size int64) error {
	url, err := s.sign(http.MethodHead, key, "", 60)
	if err != nil { return err }
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil { return err }
	resp, err := s.client.Do(req)
	if err != nil { return err }
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength != size || resp.Header.Get("Content-Type") != contentType {
		return fmt.Errorf("ukuran atau format gambar di R2 tidak sesuai (HTTP %d)", resp.StatusCode)
	}

	url, err = s.sign(http.MethodGet, key, "", 60)
	if err != nil { return err }
	req, err = http.NewRequest(http.MethodGet, url, nil)
	if err != nil { return err }
	req.Header.Set("Range", "bytes=0-511")
	resp, err = s.client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent { return fmt.Errorf("R2 tidak mengembalikan cuplikan gambar (HTTP %d)", resp.StatusCode) }
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil || imageType(prefix) != contentType { return fmt.Errorf("isi gambar di R2 tidak sesuai tipe JPG/PNG") }
	return nil
}

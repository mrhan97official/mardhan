package projectthumbnail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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

func newSignedStorage() (*signedStorage, error) {
	s := &signedStorage{account: os.Getenv("CF_ACCOUNT_ID"), bucket: os.Getenv("CF_R2_BUCKET"),
		accessKey: os.Getenv("R2_ACCESS_KEY_ID"), secretKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		client: &http.Client{Timeout: 20 * time.Second}}
	if len(s.account) != 32 || strings.Trim(s.account, "0123456789abcdef") != "" ||
		s.bucket == "" || s.accessKey == "" || s.secretKey == "" {
		return nil, fmt.Errorf("unggah gambar besar memerlukan CF_ACCOUNT_ID, CF_R2_BUCKET, R2_ACCESS_KEY_ID dan R2_SECRET_ACCESS_KEY di Vercel")
	}
	return s, nil
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

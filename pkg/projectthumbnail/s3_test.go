package projectthumbnail

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestSignedR2DeleteRequiresConfirmedSuccess(t *testing.T) {
	key := "thumbnails/" + strings.Repeat("a", 64) + "/" + strings.Repeat("b", 32) + ".img"
	status := http.StatusNoContent
	storage := &signedStorage{account: strings.Repeat("c", 32), bucket: "test-bucket", accessKey: "test-id", secretKey: "test-secret"}
	storage.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodDelete || !strings.HasSuffix(req.URL.Path, "/"+key) || req.URL.Query().Get("X-Amz-Signature") == "" {
			t.Errorf("unsigned or incorrect delete request: %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if err := storage.delete(key); err != nil { t.Fatalf("confirmed delete: %v", err) }
	status = http.StatusForbidden
	if err := storage.delete(key); err == nil { t.Fatal("rejected delete must leave thumbnail metadata intact") }
}

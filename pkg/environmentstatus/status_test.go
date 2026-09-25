package environmentstatus

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (fn transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestProjectDeploymentsUsesVercelStateAndFindsOlderProduction(t *testing.T) {
	var productionRequest bool
	client := &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("projectId"); got != "devcontrol-app" { t.Errorf("projectId = %q", got) }
		body := `{"deployments":[{"uid":"dpl_preview_new","target":"preview","readyState":"ERROR","url":"preview.vercel.app"},` +
			`{"uid":"dpl_preview_old","target":"preview","readyState":"READY"},` +
			`{"uid":"dpl_staging","target":"staging","readyState":"BUILDING","customEnvironment":{"slug":"Staging"}}]}`
		if req.URL.Query().Get("target") == "production" {
			productionRequest = true
			body = `{"deployments":[{"uid":"dpl_production","target":"production","readyState":"READY","meta":{"githubCommitSha":"abcdef0123456789"}}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	rows, err := projectDeployments(context.Background(), client, "test-token", project{ID: "devcontrol-app", Label: "App"})
	if err != nil { t.Fatal(err) }
	if !productionRequest || len(rows) != 3 { t.Fatalf("productionRequest=%v, rows=%+v", productionRequest, rows) }
	if rows[0].Name != "Preview" || rows[0].Status != "Failed" || rows[0].URL != "https://preview.vercel.app" {
		t.Fatalf("latest preview not reported: %+v", rows[0])
	}
	if rows[2].Name != "Production" || rows[2].Status != "Ready" || rows[2].Version != "abcdef012345" {
		t.Fatalf("production deployment not reported: %+v", rows[2])
	}
}

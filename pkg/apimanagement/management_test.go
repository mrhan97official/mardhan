package apimanagement

import (
  "context"
  "net"
  "testing"
)

func TestEndpointCheckRejectsUnsafeTargets(t *testing.T) {
  for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.168.1.1", "2001:db8::1", "::1"} {
    if publicIP(net.ParseIP(raw)) { t.Errorf("internal address allowed: %s", raw) }
  }
  if !publicIP(net.ParseIP("1.1.1.1")) { t.Fatal("public address rejected") }
  for _, path := range []string{"//private", "/a/../private", "/%2e%2e/", "https://evil.example/"} {
    if validPath(path) { t.Errorf("unsafe path allowed: %s", path) }
  }
  if !validPath("/api/health") { t.Fatal("ordinary health path rejected") }
  if _, _, err := checkURL(context.Background(), "http://127.0.0.1/", "GET"); err == nil {
    t.Fatal("non-HTTPS endpoint accepted")
  }
}

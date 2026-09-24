package auth

import (
  "net/http"
  "net/http/httptest"
  "strings"
  "testing"
)

func TestAdminSessionAndOrigin(t *testing.T) {
  t.Setenv("DEVCONTROL_ADMIN_PASSWORD", "a-long-test-password-only")
  t.Setenv("DEVCONTROL_SESSION_SECRET", "a-long-test-session-secret-at-least-32-characters")
  login := httptest.NewRequest(http.MethodPost, "https://devcontrol.example/api/session", strings.NewReader(`{"password":"a-long-test-password-only"}`))
  login.Header.Set("Origin", "https://devcontrol.example")
  if !SameOrigin(login) { t.Fatal("same-origin login rejected") }
  response := httptest.NewRecorder()
  HandleSession(response, login)
  if response.Code != http.StatusOK { t.Fatalf("login HTTP %d: %s", response.Code, response.Body.String()) }
  cookies := response.Result().Cookies()
  if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure { t.Fatal("admin cookie is missing protection") }
  valid := httptest.NewRequest(http.MethodGet, "https://devcontrol.example/api/overview", nil)
  valid.AddCookie(cookies[0])
  if !IsAdmin(valid) { t.Fatal("signed session rejected") }
  tampered := httptest.NewRequest(http.MethodGet, "https://devcontrol.example/api/overview", nil)
  altered := *cookies[0]
  altered.Value += "x"
  tampered.AddCookie(&altered)
  if IsAdmin(tampered) { t.Fatal("tampered session accepted") }
  foreign := httptest.NewRequest(http.MethodPost, "https://devcontrol.example/api/databases", nil)
  foreign.Header.Set("Origin", "https://other.example")
  if SameOrigin(foreign) { t.Fatal("cross-origin mutation accepted") }
  keyWrite := httptest.NewRequest(http.MethodPost, "https://devcontrol.example/api/databases", nil)
  keyWrite.Header.Set("Authorization", "Bearer dc_unrelated")
  if Allowed(keyWrite, "databases") { t.Fatal("API key permitted admin action") }
}

package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdminSecuritySessionLifecycleAndPasswordRotation(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "emergency-token"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	var initialized struct {
		Token string `json:"token"`
	}
	requestJSON(t, httpServer.URL+"/api/v2/auth/init", "", map[string]string{"username": "admin", "password": "Admin-pass1"}, http.StatusCreated, &initialized)
	if initialized.Token == "" {
		t.Fatal("admin initialization did not return a session")
	}
	var login struct {
		Token string `json:"token"`
	}
	requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "admin", "password": "Admin-pass1"}, http.StatusOK, &login)
	if login.Token == "" || login.Token == initialized.Token {
		t.Fatal("admin login did not create a separate session")
	}

	var sessions []AdminSession
	getJSON(t, httpServer.URL+"/api/v2/security/sessions", login.Token, http.StatusOK, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("expected two active sessions, got %+v", sessions)
	}
	var firstID string
	for _, session := range sessions {
		if !session.Current {
			firstID = session.ID
		}
	}
	if firstID == "" {
		t.Fatal("session list did not identify the current session")
	}
	deleteJSON(t, httpServer.URL+"/api/v2/security/sessions/"+firstID, login.Token, http.StatusNoContent)
	requestJSON(t, httpServer.URL+"/api/v2/security/sessions/revoke-others", login.Token, nil, http.StatusNoContent, nil)
	getJSON(t, httpServer.URL+"/api/v2/security/sessions", login.Token, http.StatusOK, &sessions)
	if len(sessions) != 1 || !sessions[0].Current {
		t.Fatalf("other sessions were not revoked: %+v", sessions)
	}

	requestJSON(t, httpServer.URL+"/api/v2/security/admin-password", login.Token, map[string]string{"current_password": "Admin-pass1", "new_password": "Secure-admin-123"}, http.StatusOK, nil)
	getJSON(t, httpServer.URL+"/api/v2/auth/me", login.Token, http.StatusUnauthorized, nil)
	requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "admin", "password": "Secure-admin-123"}, http.StatusOK, &login)
}

func TestAdminTwoFactorAuthenticationLifecycle(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "emergency-token"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	var initResponse struct {
		Token string `json:"token"`
	}
	requestJSON(t, httpServer.URL+"/api/v2/auth/init", "", map[string]string{"username": "admin", "password": "Admin-pass1"}, http.StatusCreated, &initResponse)
	var setup struct {
		Secret string `json:"secret"`
	}
	requestJSON(t, httpServer.URL+"/api/v2/security/2fa/setup", initResponse.Token, nil, http.StatusOK, &setup)
	if setup.Secret == "" {
		t.Fatal("2FA setup did not return a secret")
	}
	code, err := totpCode(setup.Secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	requestJSON(t, httpServer.URL+"/api/v2/security/2fa/confirm", initResponse.Token, map[string]string{"code": code}, http.StatusOK, nil)
	getJSON(t, httpServer.URL+"/api/v2/auth/me", initResponse.Token, http.StatusUnauthorized, nil)
	requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "admin", "password": "Admin-pass1"}, http.StatusUnauthorized, nil)
	code, _ = totpCode(setup.Secret, time.Now().UTC())
	var login struct {
		Token string `json:"token"`
	}
	requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "admin", "password": "Admin-pass1", "two_factor_code": code}, http.StatusOK, &login)
	if login.Token == "" {
		t.Fatal("2FA login did not return a session")
	}
	code, _ = totpCode(setup.Secret, time.Now().UTC())
	getJSON(t, httpServer.URL+"/api/v2/security/2fa", login.Token, http.StatusOK, nil)
	deleteJSONPayload(t, httpServer.URL+"/api/v2/security/2fa", login.Token, map[string]string{"code": code}, http.StatusOK)
}

func TestTOTPCodeMatchesRFC6238EpochVector(t *testing.T) {
	code, err := totpCode("JBSWY3DPEHPK3PXP", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if code != "282760" {
		t.Fatalf("unexpected TOTP code: %s", code)
	}
}

func TestAdminLoginRateLimit(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "emergency-token"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	for index := 0; index < loginFailureLimit; index++ {
		requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "admin", "password": "wrong"}, http.StatusUnauthorized, nil)
	}
	requestJSON(t, httpServer.URL+"/api/v2/auth/login", "", map[string]string{"username": "admin", "password": "wrong"}, http.StatusTooManyRequests, nil)
}

func TestAPIResponsesAreNotCacheable(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "emergency-token"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/auth/initialized", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API cache policy is %q", got)
	}
}

func TestEnrollmentTokenLifetimeIsBounded(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "emergency-token"})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"ttl_minutes":1441}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/enrollment-tokens", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer emergency-token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized token lifetime returned %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestLoginFailureLimiterHasHardCapacity(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := NewServer(store, ServerOptions{AdminToken: "emergency-token"})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < loginFailureMaxEntries+100; index++ {
		server.recordLoginFailure(fmt.Sprintf("2001:db8::%x", index))
	}
	if size := len(server.loginFailures); size > loginFailureMaxEntries {
		t.Fatalf("login limiter grew to %d entries, cap is %d", size, loginFailureMaxEntries)
	}
}

func deleteJSON(t *testing.T, url, token string, expected int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodDelete, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		var body map[string]interface{}
		_ = json.NewDecoder(response.Body).Decode(&body)
		t.Fatalf("expected status %d, got %d: %v", expected, response.StatusCode, body)
	}
}

func deleteJSONPayload(t *testing.T, url, token string, payload interface{}, expected int) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodDelete, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expected {
		var body map[string]interface{}
		_ = json.NewDecoder(response.Body).Decode(&body)
		t.Fatalf("expected status %d, got %d: %v", expected, response.StatusCode, body)
	}
}

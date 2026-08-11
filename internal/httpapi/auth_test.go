package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLoginEndpointAcceptsCorrectCredentialsAndSetsSessionCookie(t *testing.T) {
	application := NewHandler(nil)
	response := serveRequest(application, http.MethodPost, "/api/login", `{"username":"admin","password":"usage-viewer-2026"}`, "application/json", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || cookies[0].Value == "" || !cookies[0].HttpOnly {
		t.Fatalf("cookies=%#v", cookies)
	}
	assertSecurityHeaders(t, response.Result())
}

func TestLoginEndpointRejectsInvalidRequests(t *testing.T) {
	const sentinel = "login-secret-sentinel"
	tests := []struct {
		name        string
		method      string
		body        string
		contentType string
		origin      string
		wantStatus  int
	}{
		{name: "method", method: http.MethodGet, contentType: "application/json", wantStatus: http.StatusMethodNotAllowed},
		{name: "missing content type", method: http.MethodPost, body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "cross origin", method: http.MethodPost, body: `{}`, contentType: "application/json", origin: "https://other.invalid", wantStatus: http.StatusForbidden},
		{name: "malformed", method: http.MethodPost, body: `{`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: `{"username":"admin","password":"x","extra":"` + sentinel + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "wrong password", method: http.MethodPost, body: `{"username":"admin","password":"` + sentinel + `"}`, contentType: "application/json", wantStatus: http.StatusUnauthorized},
		{name: "wrong username", method: http.MethodPost, body: `{"username":"` + sentinel + `","password":"usage-viewer-2026"}`, contentType: "application/json", wantStatus: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			application := NewHandler(nil)
			response := serveRequest(application, tt.method, "/api/login", tt.body, tt.contentType, tt.origin)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), sentinel) {
				t.Fatalf("rejection echoed request input: %q", response.Body.String())
			}
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("no session cookie should be set on a rejected login")
			}
		})
	}
}

func TestAuthStatusReflectsSessionState(t *testing.T) {
	application := NewHandler(nil)

	loggedOut := serveRequest(application, http.MethodGet, "/api/auth/status", "", "", "")
	if loggedOut.Code != http.StatusOK || loggedOut.Body.String() != `{"authenticated":false}`+"\n" {
		t.Fatalf("status=%d body=%q", loggedOut.Code, loggedOut.Body.String())
	}

	cookie := loginCookie(t, application)
	loggedIn := serveRequestWithCookie(application, http.MethodGet, "/api/auth/status", "", "", "", cookie)
	if loggedIn.Code != http.StatusOK || loggedIn.Body.String() != `{"authenticated":true}`+"\n" {
		t.Fatalf("status=%d body=%q", loggedIn.Code, loggedIn.Body.String())
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	application := NewHandler(nil)
	cookie := loginCookie(t, application)

	logout := serveRequestWithCookie(application, http.MethodPost, "/api/logout", "", "", "", cookie)
	if logout.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", logout.Code, logout.Body.String())
	}

	afterLogout := serveRequestWithCookie(application, http.MethodGet, "/api/auth/status", "", "", "", cookie)
	if afterLogout.Body.String() != `{"authenticated":false}`+"\n" {
		t.Fatalf("body=%q", afterLogout.Body.String())
	}
}

func TestLogoutRejectsWrongMethodAndCrossOrigin(t *testing.T) {
	application := NewHandler(nil)
	if response := serveRequest(application, http.MethodGet, "/api/logout", "", "", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", response.Code)
	}
	if response := serveRequest(application, http.MethodPost, "/api/logout", "", "", "https://other.invalid"); response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestProtectedPagesRedirectUnauthenticatedVisitorsToLoginWithNext(t *testing.T) {
	application := NewHandler(nil)
	for _, path := range []string{"/keys", "/leaderboard"} {
		response := serveRequest(application, http.MethodGet, path, "", "", "")
		if response.Code != http.StatusFound {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
		location := response.Header().Get("Location")
		if location != "/login?next="+url.QueryEscape(path) {
			t.Fatalf("path=%s location=%q", path, location)
		}
	}
}

func TestProtectedPagesServeContentForAuthenticatedVisitors(t *testing.T) {
	application := NewHandler(nil)
	cookie := loginCookie(t, application)
	for _, path := range []string{"/keys", "/leaderboard"} {
		response := serveRequestWithCookie(application, http.MethodGet, path, "", "", "", cookie)
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func TestPublicPagesRemainAccessibleWithoutAuthentication(t *testing.T) {
	application := NewHandler(nil)
	for _, path := range []string{"/", "/self", "/login"} {
		response := serveRequest(application, http.MethodGet, path, "", "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
}

func TestConfigureAuthOverridesLoginCredentials(t *testing.T) {
	previousUsername, previousPassword := authUsername, authPassword
	t.Cleanup(func() { authUsername, authPassword = previousUsername, previousPassword })

	ConfigureAuth("operator", "configured-secret")

	application := NewHandler(nil)
	rejected := serveRequest(application, http.MethodPost, "/api/login", `{"username":"admin","password":"usage-viewer-2026"}`, "application/json", "")
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("old default credentials should be rejected: status=%d", rejected.Code)
	}
	accepted := serveRequest(application, http.MethodPost, "/api/login", `{"username":"operator","password":"configured-secret"}`, "application/json", "")
	if accepted.Code != http.StatusOK {
		t.Fatalf("configured credentials should be accepted: status=%d body=%q", accepted.Code, accepted.Body.String())
	}
}

func TestConfigureAuthIgnoresBlankArguments(t *testing.T) {
	previousUsername, previousPassword := authUsername, authPassword
	t.Cleanup(func() { authUsername, authPassword = previousUsername, previousPassword })

	ConfigureAuth("operator", "configured-secret")
	ConfigureAuth("", "")
	if authUsername != "operator" || authPassword != "configured-secret" {
		t.Fatalf("blank arguments should not clear a prior override: username=%q password=%q", authUsername, authPassword)
	}
}

func TestSelfLookupAPIRemainsPublic(t *testing.T) {
	service := &fakeSelfLookupService{ok: false}
	application := NewFullHandler(nil, nil, nil, service)
	response := serveRequest(application, http.MethodPost, "/api/self-lookup", `{"credential":"anything"}`, "application/json", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

//go:build integration

package tests

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeServesCurrentTargetSearchAndRejectsCraftedRequests(t *testing.T) {
	harness := requireRuntimePostgres(t)
	harness.execAdmin(t,
		`INSERT INTO public.accounts (id, name, platform, status, deleted_at) VALUES
            (98201, 'Runtime Current Account', 'openai', 'active', NULL),
            (98202, 'Runtime Deleted Account', 'openai', 'active', pg_catalog.now())`,
	)
	harness.execAdmin(t,
		`INSERT INTO public.users (id, email, username, status, deleted_at) VALUES
            (98301, 'runtime-current@example.invalid', 'runtime-current', 'active', NULL),
            (98302, 'runtime-deleted@example.invalid', 'runtime-deleted', 'active', pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t, `DELETE FROM public.accounts WHERE id IN (98201, 98202)`)
		harness.execAdmin(t, `DELETE FROM public.users WHERE id IN (98301, 98302)`)
	})

	binary := buildViewerBinary(t)
	listenAddress := reserveLoopbackAddress(t)
	databaseURL := harness.candidateURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = environmentList(runtimeEnvironment(databaseURL, runtimeRoleName, listenAddress))
	output := &lockedBuffer{}
	command.Stdout = output
	command.Stderr = output
	require.NoError(t, command.Start())
	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()
	waitForReadyEvent(t, output, waitResult)

	rootResponse, err := http.Get("http://" + listenAddress + "/")
	require.NoError(t, err)
	rootBody, err := io.ReadAll(rootResponse.Body)
	_ = rootResponse.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rootResponse.StatusCode)
	require.Contains(t, string(rootBody), "自助查询")

	jar, jarErr := cookiejar.New(nil)
	require.NoError(t, jarErr)
	client := &http.Client{Jar: jar}
	loginRequest, loginRequestErr := http.NewRequest(http.MethodPost, "http://"+listenAddress+"/api/login", strings.NewReader(`{"username":"admin","password":"usage-viewer-2026"}`))
	require.NoError(t, loginRequestErr)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse, loginErr := client.Do(loginRequest)
	require.NoError(t, loginErr)
	_, _ = io.Copy(io.Discard, loginResponse.Body)
	_ = loginResponse.Body.Close()
	require.Equal(t, http.StatusOK, loginResponse.StatusCode)

	tests := []struct {
		name       string
		body       string
		origin     string
		wantStatus int
		wantBody   string
	}{
		{name: "current account", body: `{"targetType":"account","query":"Runtime Current"}`, wantStatus: http.StatusOK, wantBody: `{"targetType":"account","results":[{"id":"98201","name":"Runtime Current Account"}]}`},
		{name: "deleted account", body: `{"targetType":"account","query":"98202"}`, wantStatus: http.StatusOK, wantBody: `{"targetType":"account","results":[]}`},
		{name: "current user", body: `{"targetType":"user","query":"runtime-current@example.invalid"}`, wantStatus: http.StatusOK, wantBody: `{"targetType":"user","results":[{"id":"98301","username":"runtime-current","email":"runtime-current@example.invalid"}]}`},
		{name: "deleted user", body: `{"targetType":"user","query":"98302"}`, wantStatus: http.StatusOK, wantBody: `{"targetType":"user","results":[]}`},
		{name: "crafted target", body: `{"targetType":"crafted-runtime-sentinel","query":"runtime-query-sentinel"}`, wantStatus: http.StatusBadRequest, wantBody: `{"error":{"code":"INVALID_SEARCH","message":"The search criteria are invalid."}}`},
		{name: "cross origin", body: `{"targetType":"account","query":"Runtime Current"}`, origin: "https://other.invalid", wantStatus: http.StatusForbidden, wantBody: `{"error":{"code":"ORIGIN_REJECTED","message":"The request origin is not allowed."}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, requestErr := http.NewRequest(http.MethodPost, "http://"+listenAddress+"/api/search", strings.NewReader(tt.body))
			require.NoError(t, requestErr)
			request.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			response, requestErr := client.Do(request)
			require.NoError(t, requestErr)
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			require.NoError(t, readErr)
			require.Equal(t, tt.wantStatus, response.StatusCode)
			require.Equal(t, tt.wantBody+"\n", string(body))
			require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
			require.Empty(t, response.Header.Get("Access-Control-Allow-Origin"))
		})
	}

	require.NoError(t, command.Process.Signal(syscall.SIGTERM))
	select {
	case err := <-waitResult:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("viewer did not terminate within shutdown timeout")
	}
	waitForSocketClosed(t, listenAddress)
	harness.waitForCandidateSessions(t, 0)
	assertSentinelsAbsent(t, output.String(), "crafted-runtime-sentinel", "runtime-query-sentinel", databaseURL, runtimeRolePassword)
}

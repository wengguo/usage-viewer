package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/app"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/security"
)

func TestExitCodeMapping(t *testing.T) {
	tests := []struct {
		code diagnostics.Code
		want int
	}{
		{code: diagnostics.CodeConfiguration, want: 2},
		{code: diagnostics.CodeUnsafeBind, want: 2},
		{code: diagnostics.CodeDatabaseConnectivity, want: 3},
		{code: diagnostics.CodeDatabasePrivilege, want: 4},
		{code: diagnostics.CodeDatabaseReadOnly, want: 5},
		{code: diagnostics.CodeSchemaCompatibility, want: 6},
		{code: diagnostics.CodeServer, want: 7},
	}
	for _, tt := range tests {
		err := diagnostics.New(tt.code, "ignored", "raw-secret-sentinel")
		if got := exitCode(err); got != tt.want {
			t.Errorf("exitCode(%s) = %d, want %d", tt.code, got, tt.want)
		}
	}
	if got := exitCode(errors.New("unknown-raw-secret-sentinel")); got != 7 {
		t.Fatalf("unknown exit code = %d", got)
	}
}

func TestRunWithLoadsOnceWiresDependenciesAndLogsOneFailure(t *testing.T) {
	for _, wantCode := range []diagnostics.Code{
		diagnostics.CodeDatabaseConnectivity,
		diagnostics.CodeDatabasePrivilege,
		diagnostics.CodeDatabaseReadOnly,
		diagnostics.CodeSchemaCompatibility,
		diagnostics.CodeServer,
	} {
		t.Run(string(wantCode), func(t *testing.T) {
			loadCalls := 0
			runnerCalls := 0
			output := &bytes.Buffer{}
			loader := func(lookup config.LookupEnv) (config.Config, error) {
				loadCalls++
				return config.Load(lookup)
			}
			runner := func(_ context.Context, _ config.Config, _ *security.Logger, deps app.Dependencies) error {
				runnerCalls++
				if deps.OpenPool == nil || deps.OpenLightPool == nil || deps.Preflight == nil || deps.LightPreflight == nil || deps.ClosePool == nil || deps.Listen == nil || deps.Serve == nil || deps.Shutdown == nil || deps.NewHandler == nil || deps.NewCredentialHandler == nil {
					t.Fatal("production dependency is nil")
				}
				handler := deps.NewHandler(&pgxpool.Pool{}, config.Config{DatabaseQueryTimeout: time.Second})
				response := &responseRecorder{header: make(http.Header)}
				handler.ServeHTTP(response, mustRequest(t, http.MethodGet, "/livez"))
				if response.status != http.StatusOK || response.body.String() != `{"status":"ok"}` {
					t.Fatalf("wired handler status=%d body=%q", response.status, response.body.String())
				}
				return diagnostics.New(wantCode, "ignored", "runner-raw-secret-sentinel")
			}

			got := runWith(context.Background(), validLookup(), output, loader, runner)
			if got != exitCode(diagnostics.New(wantCode, "", "")) || loadCalls != 1 || runnerCalls != 1 {
				t.Fatalf("exit=%d loads=%d runners=%d", got, loadCalls, runnerCalls)
			}
			text := output.String()
			if strings.Count(text, string(wantCode)) != 1 {
				t.Fatalf("diagnostic count in %q", text)
			}
			for _, sentinel := range []string{"database-password-sentinel", "role-secret-sentinel", "runner-raw-secret-sentinel", "127.0.0.1:18091"} {
				if strings.Contains(text, sentinel) {
					t.Fatalf("output disclosed %q", sentinel)
				}
			}
		})
	}
}

func TestRunWithEmptyEnvironmentLoadsConfigAndDelegatesToApplication(t *testing.T) {
	output := &bytes.Buffer{}
	runnerCalls := 0
	exit := runWith(context.Background(), func(string) (string, bool) { return "", false }, output, config.Load, func(context.Context, config.Config, *security.Logger, app.Dependencies) error {
		runnerCalls++
		return nil
	})
	if exit != 0 || runnerCalls != 1 {
		t.Fatalf("exit=%d runners=%d output=%q", exit, runnerCalls, output.String())
	}
}

func TestBuildProducesNamedBinary(t *testing.T) {
	output := filepath.Join(t.TempDir(), "sub2api-usage-viewer")
	cache := filepath.Join(t.TempDir(), "go-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-trimpath", "-o", output, ".")
	command.Dir = "."
	command.Env = overrideEnvironment(os.Environ(), "GOCACHE", cache)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, combined)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() || filepath.Base(output) != "sub2api-usage-viewer" {
		t.Fatalf("build output = %q", output)
	}
}

func overrideEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func validLookup() config.LookupEnv {
	values := map[string]string{
		"SUB2API_USAGE_VIEWER_DATABASE_URL":  "postgres://viewer:database-password-sentinel@127.0.0.1/sub2api?sslmode=disable",
		"SUB2API_USAGE_VIEWER_DATABASE_ROLE": "role-secret-sentinel",
		"SUB2API_USAGE_VIEWER_LISTEN_ADDR":   "127.0.0.1:18091",
	}
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func mustRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (recorder *responseRecorder) Header() http.Header { return recorder.header }
func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.body.Write(body)
}
func (recorder *responseRecorder) WriteHeader(status int) { recorder.status = status }

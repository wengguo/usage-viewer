package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestLoadRequiresDatabaseSettingsWithoutDisclosingValues(t *testing.T) {
	// DatabaseURL and DATABASE_ROLE are no longer required; Load succeeds
	// with empty values when they are absent. Credential resolution happens
	// later in the startup flow.
	cfg, err := Load(mapLookup(nil))
	if err != nil || cfg.DatabaseURL != "" {
		t.Fatalf("Load() with empty env should return empty DatabaseURL")
	}

	const secretRole = "role-secret-sentinel"
	_, err = Load(mapLookup(map[string]string{
		databaseURLKey:  "postgres://user:password-secret-sentinel@127.0.0.1/viewer?forbidden-secret-sentinel=value",
		databaseRoleKey: secretRole,
	}))
	assertDiagnostic(t, err, "UV-CFG-001")
	assertAbsent(t, err.Error(), "password-secret-sentinel", "forbidden-secret-sentinel", secretRole)
}

func TestLoadUsesConservativeDefaults(t *testing.T) {
	cfg, err := Load(mapLookup(requiredEnvironment()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DatabaseURL != "postgres://viewer@127.0.0.1/sub2api?sslmode=disable" {
		t.Fatal("DatabaseURL was not preserved")
	}
	if cfg.ExpectedDatabaseRole != "viewer" {
		t.Fatal("ExpectedDatabaseRole was not preserved")
	}
	if cfg.ListenAddress != "127.0.0.1:8081" || cfg.AcknowledgeNonLoopback {
		t.Fatalf("listen defaults = %q, %t", cfg.ListenAddress, cfg.AcknowledgeNonLoopback)
	}

	wantDurations := map[string]struct {
		got  time.Duration
		want time.Duration
	}{
		"connect":     {cfg.DatabaseConnectTimeout, 5 * time.Second},
		"acquire":     {cfg.DatabaseAcquireTimeout, 2 * time.Second},
		"query":       {cfg.DatabaseQueryTimeout, 5 * time.Second},
		"lifetime":    {cfg.DatabaseMaxConnLifetime, 30 * time.Minute},
		"idle":        {cfg.DatabaseMaxConnIdleTime, 5 * time.Minute},
		"health":      {cfg.DatabaseHealthCheckPeriod, time.Minute},
		"query range": {cfg.MaximumQueryRange, 720 * time.Hour},
		"read header": {cfg.HTTPReadHeaderTimeout, 5 * time.Second},
		"read":        {cfg.HTTPReadTimeout, 10 * time.Second},
		"write":       {cfg.HTTPWriteTimeout, 15 * time.Second},
		"http idle":   {cfg.HTTPIdleTimeout, 60 * time.Second},
		"shutdown":    {cfg.ShutdownTimeout, 10 * time.Second},
	}
	for name, tc := range wantDurations {
		if tc.got != tc.want {
			t.Errorf("%s duration = %s, want %s", name, tc.got, tc.want)
		}
	}
	if cfg.DatabasePoolMaxConns != 4 || cfg.DatabasePoolMinConns != 0 {
		t.Fatalf("pool defaults = min %d, max %d", cfg.DatabasePoolMinConns, cfg.DatabasePoolMaxConns)
	}
}

func TestValidateListenAddressRequiresMatchedAcknowledgement(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		acknowledge bool
		wantCode    string
	}{
		{name: "IPv4 loopback", address: "127.0.0.1:8090"},
		{name: "IPv6 loopback", address: "[::1]:8090"},
		{name: "empty host", address: ":8090", wantCode: "UV-BIND-001"},
		{name: "hostname", address: "localhost:8090", wantCode: "UV-BIND-001"},
		{name: "wildcard IPv4 without acknowledgement", address: "0.0.0.0:8090", wantCode: "UV-BIND-001"},
		{name: "wildcard IPv4 acknowledged", address: "0.0.0.0:8090", acknowledge: true},
		{name: "wildcard IPv6 without acknowledgement", address: "[::]:8090", wantCode: "UV-BIND-001"},
		{name: "wildcard IPv6 acknowledged", address: "[::]:8090", acknowledge: true},
		{name: "non-loopback IPv4 without acknowledgement", address: "192.0.2.10:8090", wantCode: "UV-BIND-001"},
		{name: "non-loopback IPv4 acknowledged", address: "192.0.2.10:8090", acknowledge: true},
		{name: "non-loopback IPv6 without acknowledgement", address: "[2001:db8::10]:8090", wantCode: "UV-BIND-001"},
		{name: "non-loopback IPv6 acknowledged", address: "[2001:db8::10]:8090", acknowledge: true},
		{name: "acknowledgement only IPv4", address: "127.0.0.1:8090", acknowledge: true, wantCode: "UV-BIND-001"},
		{name: "acknowledgement only IPv6", address: "[::1]:8090", acknowledge: true, wantCode: "UV-BIND-001"},
		{name: "zero port", address: "127.0.0.1:0", wantCode: "UV-BIND-001"},
		{name: "named port", address: "127.0.0.1:http", wantCode: "UV-BIND-001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateListenAddress(tt.address, tt.acknowledge)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("ValidateListenAddress() error = %v", err)
				}
				return
			}
			assertDiagnostic(t, err, tt.wantCode)
			assertAbsent(t, err.Error(), tt.address)
		})
	}
}

func TestLoadRejectsInvalidRuntimeLimitsWithoutEchoingInput(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "acknowledgement", key: "SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK", value: "TRUE-secret-sentinel"},
		{name: "connect timeout", key: "SUB2API_USAGE_VIEWER_DB_CONNECT_TIMEOUT", value: "connect-secret-sentinel"},
		{name: "acquire timeout", key: "SUB2API_USAGE_VIEWER_DB_ACQUIRE_TIMEOUT", value: "0s"},
		{name: "query timeout", key: "SUB2API_USAGE_VIEWER_DB_QUERY_TIMEOUT", value: "-1s"},
		{name: "maximum connections", key: "SUB2API_USAGE_VIEWER_DB_POOL_MAX_CONNS", value: "5"},
		{name: "minimum connections", key: "SUB2API_USAGE_VIEWER_DB_POOL_MIN_CONNS", value: "5"},
		{name: "maximum lifetime", key: "SUB2API_USAGE_VIEWER_DB_MAX_CONN_LIFETIME", value: "lifetime-secret-sentinel"},
		{name: "maximum idle time", key: "SUB2API_USAGE_VIEWER_DB_MAX_CONN_IDLE_TIME", value: "idle-secret-sentinel"},
		{name: "health period", key: "SUB2API_USAGE_VIEWER_DB_HEALTH_CHECK_PERIOD", value: "health-secret-sentinel"},
		{name: "query range too short", key: "SUB2API_USAGE_VIEWER_MAX_QUERY_RANGE", value: "59m"},
		{name: "query range too long", key: "SUB2API_USAGE_VIEWER_MAX_QUERY_RANGE", value: "721h"},
		{name: "read header timeout", key: "SUB2API_USAGE_VIEWER_HTTP_READ_HEADER_TIMEOUT", value: "header-secret-sentinel"},
		{name: "read timeout", key: "SUB2API_USAGE_VIEWER_HTTP_READ_TIMEOUT", value: "read-secret-sentinel"},
		{name: "write timeout", key: "SUB2API_USAGE_VIEWER_HTTP_WRITE_TIMEOUT", value: "write-secret-sentinel"},
		{name: "idle timeout", key: "SUB2API_USAGE_VIEWER_HTTP_IDLE_TIMEOUT", value: "http-idle-secret-sentinel"},
		{name: "shutdown timeout", key: "SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT", value: "shutdown-secret-sentinel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			environment := requiredEnvironment()
			environment[tt.key] = tt.value
			_, err := Load(mapLookup(environment))
			assertDiagnostic(t, err, "UV-CFG-001")
			assertAbsent(t, err.Error(), tt.value)
		})
	}
}

func TestLoadRejectsUnsafeDatabaseURLsWithoutEchoingInput(t *testing.T) {
	tests := []struct {
		name        string
		databaseURL string
	}{
		{name: "keyword DSN", databaseURL: "host=127.0.0.1 password=keyword-secret-sentinel"},
		{name: "wrong scheme", databaseURL: "mysql://user:scheme-secret-sentinel@127.0.0.1/sub2api"},
		{name: "unknown option", databaseURL: "postgres://user:unknown-secret-sentinel@127.0.0.1/sub2api?connect_timeout=3"},
		{name: "session options", databaseURL: "postgres://user:option-secret-sentinel@127.0.0.1/sub2api?options=-cstatement_timeout%3D0"},
		{name: "search path", databaseURL: "postgres://user:path-secret-sentinel@127.0.0.1/sub2api?search_path=private"},
		{name: "duplicate TLS option", databaseURL: "postgres://user:duplicate-secret-sentinel@127.0.0.1/sub2api?sslmode=disable&sslmode=verify-full"},
		{name: "remote without TLS", databaseURL: "postgres://user:remote-secret-sentinel@192.0.2.10/sub2api?sslmode=disable"},
		{name: "remote without root certificate", databaseURL: "postgres://user:root-secret-sentinel@db.example/sub2api?sslmode=verify-full"},
		{name: "remote with blank root certificate", databaseURL: "postgres://user:blank-root-secret-sentinel@db.example/sub2api?sslmode=verify-full&sslrootcert=%20"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			environment := requiredEnvironment()
			environment[databaseURLKey] = tt.databaseURL
			_, err := Load(mapLookup(environment))
			assertDiagnostic(t, err, "UV-CFG-001")
			assertAbsent(t, err.Error(), tt.databaseURL, "secret-sentinel", "db.example", "192.0.2.10")
		})
	}
}

func TestLoadAcceptsRemoteDatabaseOnlyWithVerifiedTLS(t *testing.T) {
	environment := requiredEnvironment()
	environment[databaseURLKey] = "postgresql://viewer:remote-password@db.example/sub2api?sslmode=verify-full&sslrootcert=%2Fetc%2Fviewer%2Froot.crt&sslcert=%2Fetc%2Fviewer%2Fclient.crt&sslkey=%2Fetc%2Fviewer%2Fclient.key"
	if _, err := Load(mapLookup(environment)); err != nil {
		t.Fatalf("Load() remote verified TLS error = %v", err)
	}
}

func TestLoadAcceptsUnixSocketDatabaseURL(t *testing.T) {
	environment := requiredEnvironment()
	environment[databaseURLKey] = "postgresql://%2Fvar%2Frun%2Fpostgresql/sub2api?sslmode=disable"
	if _, err := Load(mapLookup(environment)); err != nil {
		t.Fatalf("Load() Unix socket error = %v", err)
	}
}

func TestLoadReturnsBindDiagnosticForUnsafeListener(t *testing.T) {
	environment := requiredEnvironment()
	environment["SUB2API_USAGE_VIEWER_LISTEN_ADDR"] = "0.0.0.0:8090"
	_, err := Load(mapLookup(environment))
	assertDiagnostic(t, err, "UV-BIND-001")
}

func requiredEnvironment() map[string]string {
	return map[string]string{
		databaseURLKey:  "postgres://viewer@127.0.0.1/sub2api?sslmode=disable",
		databaseRoleKey: "viewer",
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func assertDiagnostic(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s diagnostic, got nil", code)
	}
	if !strings.HasPrefix(err.Error(), code+": ") {
		t.Fatalf("error = %q, want %s diagnostic", err.Error(), code)
	}
	var diagnostic *diagnostics.Diagnostic
	if !errors.As(err, &diagnostic) {
		t.Fatalf("error %T is not a typed diagnostic", err)
	}
	if got := string(diagnostic.Code()); got != code {
		t.Fatalf("diagnostic code = %q, want %q", got, code)
	}
}

func assertAbsent(t *testing.T, text string, sentinels ...string) {
	t.Helper()
	for _, sentinel := range sentinels {
		if strings.Contains(text, sentinel) {
			t.Errorf("output disclosed sentinel %q", sentinel)
		}
	}
}

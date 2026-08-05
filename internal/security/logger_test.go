package security

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestLoggerEmitsOnlyAllowlistedOperationalMetadata(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output)
	logger.Ready(AddressClassLoopback)
	logger.Stopping()
	logger.Failure(diagnostics.Wrap(
		diagnostics.CodeDatabaseConnectivity,
		diagnostics.CategoryDatabaseConnectivity,
		"caller-message-secret-sentinel",
		errors.New(strings.Join(logSentinels(), " ")),
	))

	records := decodeLogRecords(t, output.Bytes())
	if len(records) != 3 {
		t.Fatalf("log record count = %d, want 3", len(records))
	}
	allowedKeys := map[string]bool{
		"timestamp":     true,
		"level":         true,
		"event":         true,
		"address_class": true,
		"code":          true,
		"category":      true,
		"message":       true,
	}
	for _, record := range records {
		for key := range record {
			if !allowedKeys[key] {
				t.Errorf("log contains non-allowlisted key %q", key)
			}
		}
		timestamp, ok := record["timestamp"].(string)
		if !ok {
			t.Fatalf("timestamp is not a string: %#v", record["timestamp"])
		}
		parsed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil || parsed.Location() != time.UTC {
			t.Errorf("timestamp %q is not UTC RFC3339: %v", timestamp, err)
		}
	}

	ready := records[0]
	if ready["event"] != "ready" || ready["address_class"] != AddressClassLoopback {
		t.Errorf("ready record = %#v", ready)
	}
	failure := records[2]
	if failure["event"] != "failure" || failure["code"] != string(diagnostics.CodeDatabaseConnectivity) || failure["category"] != string(diagnostics.CategoryDatabaseConnectivity) || failure["message"] != "database connection could not be established" {
		t.Errorf("failure record = %#v", failure)
	}

	assertNoLogSentinel(t, output.String(), append(logSentinels(), "caller-message-secret-sentinel")...)
}

func TestLoggerRejectsArbitraryAddressClasses(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output)
	logger.Ready("address-class-secret-sentinel")
	if output.Len() != 0 {
		t.Fatalf("invalid address class produced output: %s", output.String())
	}

	logger.Ready(AddressClassAcknowledgedNonLoopback)
	records := decodeLogRecords(t, output.Bytes())
	if len(records) != 1 || records[0]["address_class"] != AddressClassAcknowledgedNonLoopback {
		t.Fatalf("acknowledged non-loopback record = %#v", records)
	}
}

func TestLoggerMapsNilDiagnosticToServerFailure(t *testing.T) {
	var output bytes.Buffer
	NewLogger(&output).Failure(nil)
	records := decodeLogRecords(t, output.Bytes())
	if len(records) != 1 || records[0]["code"] != string(diagnostics.CodeServer) || records[0]["message"] != "server could not start safely" {
		t.Fatalf("nil diagnostic record = %#v", records)
	}
}

func TestLoggerPublicMethodsHaveFixedShapes(t *testing.T) {
	typeOfLogger := reflect.TypeOf((*Logger)(nil))
	wantMethods := map[string]int{
		"Failure":  1,
		"Ready":    1,
		"Stopping": 0,
	}
	if typeOfLogger.NumMethod() != len(wantMethods) {
		t.Fatalf("Logger exported method count = %d, want %d", typeOfLogger.NumMethod(), len(wantMethods))
	}
	for name, argumentCount := range wantMethods {
		method, ok := typeOfLogger.MethodByName(name)
		if !ok {
			t.Errorf("Logger method %s is missing", name)
			continue
		}
		if method.Type.IsVariadic() {
			t.Errorf("Logger method %s must not be variadic", name)
		}
		if got := method.Type.NumIn() - 1; got != argumentCount {
			t.Errorf("Logger method %s argument count = %d, want %d", name, got, argumentCount)
		}
	}
}

func logSentinels() []string {
	return []string{
		"postgres://dsn-user-sentinel:dsn-password-secret-sentinel@db-host-sentinel/sub2api",
		"dsn-password-secret-sentinel",
		"pq: duplicate key value violates unique constraint pg-error-sentinel",
		"sql-argument-secret-sentinel",
		"search-term-secret-sentinel",
		`{"entity_record":"entity-record-secret-sentinel"}`,
	}
}

func decodeLogRecords(t *testing.T, output []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode log record %q: %v", scanner.Text(), err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log output: %v", err)
	}
	return records
}

func assertNoLogSentinel(t *testing.T, output string, sentinels ...string) {
	t.Helper()
	for _, sentinel := range sentinels {
		if strings.Contains(output, sentinel) {
			t.Errorf("log output disclosed sentinel %q", sentinel)
		}
	}
}

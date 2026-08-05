//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeServesExactReportProjection(t *testing.T) {
	harness := requireRuntimePostgres(t)
	const excludedSentinel = "runtime-report-excluded-sentinel"
	harness.execAdmin(t,
		`INSERT INTO public.usage_logs (
            id, user_id, account_id, request_id, model, requested_model,
            input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
            total_cost, actual_cost, account_stats_cost, account_rate_multiplier, created_at, runtime_object_secret
        ) VALUES
            (98400, 98451, 98452, NULL, 'fallback-model', NULL, 1, 2, 3, 4, 0.0000000001, 0.0000000001, NULL, NULL, pg_catalog.now() - interval '2 hours', '`+excludedSentinel+`'),
            (98401, 98451, 98452, 'request-98401', 'stored-model', 'requested-model', 10, 20, 30, 40, 1.2000000000, 1.1000000000, 0.5000000000, 2.0000, pg_catalog.now() - interval '1 hour', '`+excludedSentinel+`')`,
	)
	t.Cleanup(func() { harness.execAdmin(t, `DELETE FROM public.usage_logs WHERE id IN (98400, 98401)`) })

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

	request, err := http.NewRequest(http.MethodPost, "http://"+listenAddress+"/api/report", strings.NewReader(
		`{"targetType":"account","targetId":"98452","preset":"24h","start":"","end":""}`,
	))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "application/json; charset=utf-8", response.Header.Get("Content-Type"))
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	require.Empty(t, response.Header.Get("Access-Control-Allow-Origin"))
	require.NotContains(t, string(body), excludedSentinel)

	var payload runtimeReportResponse
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "account", payload.TargetType)
	require.Equal(t, "98452", payload.TargetID)
	require.Equal(t, "2", payload.Summary.Requests)
	require.Equal(t, "11", payload.Summary.InputTokens)
	require.Equal(t, "1.2000000001", payload.Summary.OriginalCost)
	require.Equal(t, "1.1000000001", payload.Summary.ActualCost)
	require.Equal(t, "1.00000000010000", payload.Summary.AccountCost)
	require.Len(t, payload.Details, 2)
	require.Equal(t, "requested-model", payload.Details[0].Model)
	require.Equal(t, "request-98401", payload.Details[0].RequestID)
	require.Equal(t, "fallback-model", payload.Details[1].Model)
	require.Empty(t, payload.Details[1].RequestID)
	require.Empty(t, payload.NextCursor)

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
	assertSentinelsAbsent(t, output.String(), excludedSentinel, databaseURL, runtimeRolePassword)
}

type runtimeReportResponse struct {
	TargetType string                `json:"targetType"`
	TargetID   string                `json:"targetId"`
	Start      string                `json:"start"`
	End        string                `json:"end"`
	Summary    runtimeReportSummary  `json:"summary"`
	Details    []runtimeReportDetail `json:"details"`
	NextCursor string                `json:"nextCursor"`
}

type runtimeReportSummary struct {
	Requests     string `json:"requests"`
	InputTokens  string `json:"inputTokens"`
	OriginalCost string `json:"originalCost"`
	ActualCost   string `json:"actualCost"`
	AccountCost  string `json:"accountCost"`
}

type runtimeReportDetail struct {
	Model     string `json:"model"`
	RequestID string `json:"requestId"`
}

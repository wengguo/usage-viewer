package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/app"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/concurrency"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/creds"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/httpapi"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/postgres"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/security"
)

type configLoader func(config.LookupEnv) (config.Config, error)

type applicationRunner func(
	context.Context,
	config.Config,
	*security.Logger,
	app.Dependencies,
) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.LookupEnv, os.Stderr))
}

func run(ctx context.Context, lookup config.LookupEnv, output io.Writer) int {
	return runWith(ctx, lookup, output, config.Load, app.Run)
}

func runWith(
	ctx context.Context,
	lookup config.LookupEnv,
	output io.Writer,
	load configLoader,
	runApplication applicationRunner,
) int {
	logger := security.NewLogger(output)
	if load == nil || runApplication == nil {
		diagnostic := serverFailure()
		logger.Failure(diagnostic)
		return exitCode(diagnostic)
	}

	cfg, err := load(lookup)
	if err != nil {
		diagnostic := publicDiagnostic(err)
		logger.Failure(diagnostic)
		return exitCode(diagnostic)
	}

	dependencies := app.Dependencies{
		OpenPool:       postgres.OpenPool,
		OpenLightPool:  postgres.OpenLightPool,
		Preflight:      postgres.RunPreflight,
		LightPreflight: postgres.RunLightPreflight,
		ClosePool: func(pool *pgxpool.Pool) {
			pool.Close()
		},
		Listen: net.Listen,
		Serve: func(server *http.Server, listener net.Listener) error {
			return server.Serve(listener)
		},
		Shutdown: func(ctx context.Context, server *http.Server) error {
			return server.Shutdown(ctx)
		},
		NewHandler: func(pool *pgxpool.Pool, cfg config.Config) http.Handler {
			resolver := concurrency.NewResolver(cfg)
			searchRepository := postgres.NewSearchRepository(pool, cfg.DatabaseQueryTimeout, resolver)
			dailyRepository := postgres.NewDailyUsageRepository(pool, cfg.DatabaseQueryTimeout)
			return httpapi.NewHandlerWithDailyUsage(searchRepository, dailyRepository)
		},
		NewCredentialHandler: func(cfg *config.Config, saveCreds func(string, creds.Entry) error) http.Handler {
			return httpapi.NewCredentialHandler(cfg, saveCreds)
		},
	}
	if err := runApplication(ctx, cfg, logger, dependencies); err != nil {
		diagnostic := publicDiagnostic(err)
		logger.Failure(diagnostic)
		return exitCode(diagnostic)
	}
	return 0
}

func exitCode(err error) int {
	switch diagnostics.CodeOf(err) {
	case diagnostics.CodeConfiguration, diagnostics.CodeUnsafeBind:
		return 2
	case diagnostics.CodeDatabaseConnectivity:
		return 3
	case diagnostics.CodeDatabasePrivilege:
		return 4
	case diagnostics.CodeDatabaseReadOnly:
		return 5
	case diagnostics.CodeSchemaCompatibility:
		return 6
	case diagnostics.CodeServer:
		return 7
	default:
		return 7
	}
}

func publicDiagnostic(err error) *diagnostics.Diagnostic {
	var diagnostic *diagnostics.Diagnostic
	if errors.As(err, &diagnostic) && diagnostic != nil {
		return diagnostic
	}
	return serverFailure()
}

func serverFailure() *diagnostics.Diagnostic {
	return diagnostics.New(diagnostics.CodeServer, diagnostics.CategoryServer, "server could not start safely")
}

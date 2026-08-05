package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/creds"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/security"
)

type Dependencies struct {
	OpenPool             func(context.Context, config.Config) (*pgxpool.Pool, error)
	OpenLightPool        func(context.Context, config.Config) (*pgxpool.Pool, error)
	Preflight            func(context.Context, *pgxpool.Pool, config.Config) error
	LightPreflight       func(context.Context, *pgxpool.Pool, config.Config) error
	ClosePool            func(*pgxpool.Pool)
	Listen               func(network, address string) (net.Listener, error)
	Serve                func(*http.Server, net.Listener) error
	Shutdown             func(context.Context, *http.Server) error
	NewHandler           func(*pgxpool.Pool, config.Config) http.Handler
	NewCredentialHandler func(*config.Config, func(string, creds.Entry) error) http.Handler
}

func Run(ctx context.Context, cfg config.Config, logger *security.Logger, deps Dependencies) error {
	if ctx == nil || !depsComplete(deps) {
		return serverDiagnostic(nil)
	}
	if ctx.Err() != nil {
		return nil
	}

	// Resolve credentials if not already set.
	pool, err := establishPool(ctx, &cfg, deps)
	if err != nil {
		return handleDBError(ctx, cfg, logger, deps, err, cfg.CredentialSource)
	}
	if pool == nil {
		return RunCredentialMode(ctx, cfg, logger, deps)
	}
	var closePool sync.Once
	defer closePool.Do(func() { deps.ClosePool(pool) })

	if ctx.Err() != nil {
		return nil
	}
	handler := deps.NewHandler(pool, cfg)
	if handler == nil {
		return serverDiagnostic(nil)
	}

	listener, err := deps.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return serverDiagnostic(err)
	}
	guardedListener := &onceListener{Listener: listener}
	defer guardedListener.Close()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}
	logger.Ready(addressClass(cfg))

	serveResult := make(chan error, 1)
	serveStarted := make(chan struct{})
	go func() {
		close(serveStarted)
		serveResult <- deps.Serve(server, guardedListener)
	}()
	<-serveStarted

	select {
	case serveErr := <-serveResult:
		if serveErr == nil || errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serverDiagnostic(serveErr)
	case <-ctx.Done():
		logger.Stopping()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		shutdownErr := deps.Shutdown(shutdownCtx, server)
		cancel()
		_ = guardedListener.Close()
		serveErr := <-serveResult
		if shutdownErr != nil {
			return serverDiagnostic(shutdownErr)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serverDiagnostic(serveErr)
		}
		return nil
	}
}

// establishPool opens a database pool and runs the appropriate preflight.
// When credentials come from auto-discovery (config/.env/saved), it tries
// localhost host fallbacks for Docker-internal service names. It returns a
// non-nil pool and nil error on success, nil pool and nil error when
// credentials are missing (credential mode), and an error on failure.
// The supplied cfg pointer is updated with resolved DatabaseURL and
// CredentialSource even on failure so the caller can route correctly.
func establishPool(ctx context.Context, cfg *config.Config, deps Dependencies) (*pgxpool.Pool, error) {
	if cfg.DatabaseURL != "" {
		// Explicit credentials from env or manual config: original behavior.
		return openAndPreflight(ctx, *cfg, deps, false)
	}

	results, err := creds.Discover(osLookup, cfg.DataDir)
	if err != nil {
		return nil, nil
	}

	// All auto-discovered credentials (config/.env/saved/env host params) use
	// the shared application account, so light preflight applies. Full role
	// admission is reserved for explicit SUB2API_USAGE_VIEWER_DATABASE_URL.
	var lastErr error
	for _, result := range results {
		cfg.CredentialSource = string(result.Source)
		for _, candidate := range creds.ConnectionCandidates(result.Entry) {
			candidateCfg := *cfg
			candidateCfg.DatabaseURL = candidate.DSN()
			pool, err := openAndPreflight(ctx, candidateCfg, deps, true)
			if err == nil && pool != nil {
				cfg.DatabaseURL = candidateCfg.DatabaseURL
				return pool, nil
			}
			if pool != nil {
				deps.ClosePool(pool)
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("database connection failed")
	}
	return nil, lastErr
}

func openAndPreflight(ctx context.Context, cfg config.Config, deps Dependencies, light bool) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	var err error
	if light && deps.OpenLightPool != nil {
		pool, err = deps.OpenLightPool(ctx, cfg)
	} else {
		pool, err = deps.OpenPool(ctx, cfg)
	}
	if err != nil || pool == nil {
		return nil, err
	}
	if light && deps.LightPreflight != nil {
		if err := deps.LightPreflight(ctx, pool, cfg); err != nil {
			deps.ClosePool(pool)
			return nil, err
		}
		return pool, nil
	}
	if err := deps.Preflight(ctx, pool, cfg); err != nil {
		deps.ClosePool(pool)
		return nil, err
	}
	return pool, nil
}

// RunCredentialMode starts the HTTP server in credential-collection mode.
// It serves only the credential form page and the /api/connect endpoint.
func RunCredentialMode(ctx context.Context, cfg config.Config, logger *security.Logger, deps Dependencies) error {
	if ctx == nil || deps.Listen == nil || deps.Serve == nil || deps.Shutdown == nil || deps.NewCredentialHandler == nil {
		return serverDiagnostic(nil)
	}
	if ctx.Err() != nil {
		return nil
	}

	// Save credentials callback for the credential handler.
	saveCreds := func(dataDir string, entry creds.Entry) error {
		cfg.DatabaseURL = entry.DSN()
		return creds.Save(dataDir, entry)
	}

	handler := deps.NewCredentialHandler(&cfg, saveCreds)
	if handler == nil {
		return serverDiagnostic(nil)
	}

	listener, err := deps.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return serverDiagnostic(err)
	}
	guardedListener := &onceListener{Listener: listener}
	defer guardedListener.Close()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}
	logger.Ready(addressClass(cfg))

	serveResult := make(chan error, 1)
	serveStarted := make(chan struct{})
	go func() {
		close(serveStarted)
		serveResult <- deps.Serve(server, guardedListener)
	}()
	<-serveStarted

	select {
	case serveErr := <-serveResult:
		if serveErr == nil || errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serverDiagnostic(serveErr)
	case <-ctx.Done():
		logger.Stopping()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		shutdownErr := deps.Shutdown(shutdownCtx, server)
		cancel()
		_ = guardedListener.Close()
		serveErr := <-serveResult
		if shutdownErr != nil {
			return serverDiagnostic(shutdownErr)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serverDiagnostic(serveErr)
		}
		return nil
	}
}

// handleDBError returns the diagnostic error when credentials came from env
// (consistent with the original fail-closed behavior), but falls back to
// credential mode when credentials were auto-discovered or saved (so the
// user can re-enter valid credentials).
func handleDBError(ctx context.Context, cfg config.Config, logger *security.Logger, deps Dependencies, cause error, source string) error {
	if source == string(creds.SourceEnv) || source == "" {
		return admittedDiagnostic(cause)
	}
	return RunCredentialMode(ctx, cfg, logger, deps)
}

func depsComplete(deps Dependencies) bool {
	return deps.OpenPool != nil && deps.OpenLightPool != nil && deps.Preflight != nil && deps.ClosePool != nil &&
		deps.Listen != nil && deps.Serve != nil && deps.Shutdown != nil &&
		deps.NewHandler != nil && deps.NewCredentialHandler != nil
}

func admittedDiagnostic(err error) error {
	var diagnostic *diagnostics.Diagnostic
	if errors.As(err, &diagnostic) && diagnostic != nil {
		return diagnostic
	}
	return serverDiagnostic(err)
}

func serverDiagnostic(cause error) *diagnostics.Diagnostic {
	return diagnostics.Wrap(
		diagnostics.CodeServer,
		diagnostics.CategoryServer,
		"server could not start safely",
		cause,
	)
}

func addressClass(cfg config.Config) string {
	if cfg.AcknowledgeNonLoopback {
		return security.AddressClassAcknowledgedNonLoopback
	}
	return security.AddressClassLoopback
}

// osLookup is a lookup function that reads from the OS environment.
func osLookup(key string) (string, bool) {
	val, ok := os.LookupEnv(key)
	return val, ok
}

type onceListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (listener *onceListener) Close() error {
	listener.once.Do(func() {
		if listener.Listener != nil {
			listener.err = listener.Listener.Close()
		}
	})
	return listener.err
}

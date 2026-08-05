package app

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/creds"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/security"
)

func TestRunOrdersPreflightBeforeListenAndServe(t *testing.T) {
	recorder := &lifecycleRecorder{}
	deps := recorder.dependencies()
	err := Run(context.Background(), appTestConfig(), security.NewLogger(ioDiscard{}), deps)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.Join(recorder.eventsSnapshot(), ","); got != "open,preflight,handler,listen,serve,listener-close,pool-close" {
		t.Fatalf("events = %q", got)
	}
	if recorder.listenNetwork != "tcp" || recorder.listenAddress != appTestConfig().ListenAddress {
		t.Fatalf("listen = %q %q", recorder.listenNetwork, recorder.listenAddress)
	}
	server := recorder.serverSnapshot()
	if server == nil || server.ReadHeaderTimeout != time.Second || server.ReadTimeout != 2*time.Second || server.WriteTimeout != 3*time.Second || server.IdleTimeout != 4*time.Second {
		t.Fatalf("server = %#v", server)
	}
}

func TestRunNeverListensWhenDatabaseGateFails(t *testing.T) {
	diagnosticsToTest := []*diagnostics.Diagnostic{
		diagnostics.New(diagnostics.CodeDatabaseConnectivity, diagnostics.CategoryDatabaseConnectivity, ""),
		diagnostics.New(diagnostics.CodeDatabasePrivilege, diagnostics.CategoryDatabasePrivilege, ""),
		diagnostics.New(diagnostics.CodeDatabaseReadOnly, diagnostics.CategoryDatabaseReadOnly, ""),
		diagnostics.New(diagnostics.CodeSchemaCompatibility, diagnostics.CategorySchemaCompatibility, ""),
	}
	for _, want := range diagnosticsToTest {
		t.Run(string(want.Code()), func(t *testing.T) {
			recorder := &lifecycleRecorder{preflightErr: want}
			deps := recorder.dependencies()
			deps.Listen = func(string, string) (net.Listener, error) {
				t.Fatal("Listen called before successful preflight")
				return nil, nil
			}
			err := Run(context.Background(), appTestConfig(), security.NewLogger(ioDiscard{}), deps)
			if diagnostics.CodeOf(err) != want.Code() {
				t.Fatalf("code = %q, want %q", diagnostics.CodeOf(err), want.Code())
			}
			if recorder.poolCloses != 1 {
				t.Fatalf("pool closes = %d", recorder.poolCloses)
			}
		})
	}

	t.Run("open pool", func(t *testing.T) {
		recorder := &lifecycleRecorder{openErr: diagnostics.New(diagnostics.CodeDatabaseConnectivity, diagnostics.CategoryDatabaseConnectivity, "")}
		deps := recorder.dependencies()
		deps.Listen = func(string, string) (net.Listener, error) {
			t.Fatal("Listen called after pool open failure")
			return nil, nil
		}
		err := Run(context.Background(), appTestConfig(), security.NewLogger(ioDiscard{}), deps)
		if diagnostics.CodeOf(err) != diagnostics.CodeDatabaseConnectivity || recorder.poolCloses != 0 {
			t.Fatalf("code=%q pool closes=%d", diagnostics.CodeOf(err), recorder.poolCloses)
		}
	})
}

func TestRunDoesNotListenWhenStartupIsCanceledAfterPreflight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &lifecycleRecorder{onPreflight: cancel}
	deps := recorder.dependencies()
	deps.Listen = func(string, string) (net.Listener, error) {
		t.Fatal("Listen called after startup cancellation")
		return nil, nil
	}
	if err := Run(ctx, appTestConfig(), security.NewLogger(ioDiscard{}), deps); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if recorder.poolCloses != 1 {
		t.Fatalf("pool closes = %d", recorder.poolCloses)
	}
}

func TestRunRefusesNilHandlerAfterSuccessfulPreflight(t *testing.T) {
	recorder := &lifecycleRecorder{}
	deps := recorder.dependencies()
	deps.NewHandler = func(*pgxpool.Pool, config.Config) http.Handler {
		recorder.record("handler")
		return nil
	}
	deps.Listen = func(string, string) (net.Listener, error) {
		t.Fatal("Listen called with nil handler")
		return nil, nil
	}
	err := Run(context.Background(), appTestConfig(), security.NewLogger(ioDiscard{}), deps)
	if diagnostics.CodeOf(err) != diagnostics.CodeServer || recorder.poolCloses != 1 {
		t.Fatalf("code=%q pool closes=%d", diagnostics.CodeOf(err), recorder.poolCloses)
	}
}

func TestRunSanitizesListenAndServeFailuresAndClosesResources(t *testing.T) {
	for _, stage := range []string{"listen", "serve"} {
		t.Run(stage, func(t *testing.T) {
			recorder := &lifecycleRecorder{}
			if stage == "listen" {
				recorder.listenErr = errors.New("listen-address-secret-sentinel")
			} else {
				recorder.serveErr = errors.New("server-raw-secret-sentinel")
			}
			err := Run(context.Background(), appTestConfig(), security.NewLogger(ioDiscard{}), recorder.dependencies())
			if diagnostics.CodeOf(err) != diagnostics.CodeServer {
				t.Fatalf("code = %q", diagnostics.CodeOf(err))
			}
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatalf("error disclosed raw cause: %v", err)
			}
			if recorder.poolCloses != 1 {
				t.Fatalf("pool closes = %d", recorder.poolCloses)
			}
			wantListenerCloses := 1
			if stage == "listen" {
				wantListenerCloses = 0
			}
			if recorder.listener.closes != wantListenerCloses {
				t.Fatalf("listener closes = %d, want %d", recorder.listener.closes, wantListenerCloses)
			}
		})
	}
}

func TestRunCancellationShutsDownAndClosesExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logs := &bytes.Buffer{}
	recorder := &lifecycleRecorder{serveUntilClosed: true}
	recorder.onServe = cancel

	err := Run(ctx, appTestConfig(), security.NewLogger(logs), recorder.dependencies())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if recorder.shutdownCalls != 1 || recorder.listener.closes != 1 || recorder.poolCloses != 1 {
		t.Fatalf("shutdown=%d listener closes=%d pool closes=%d", recorder.shutdownCalls, recorder.listener.closes, recorder.poolCloses)
	}
	if recorder.shutdownDeadline <= 0 || recorder.shutdownDeadline > appTestConfig().ShutdownTimeout {
		t.Fatalf("shutdown deadline = %s", recorder.shutdownDeadline)
	}
	output := logs.String()
	if strings.Count(output, `"event":"ready"`) != 1 || strings.Count(output, `"event":"stopping"`) != 1 {
		t.Fatalf("lifecycle logs = %q", output)
	}
	for _, sentinel := range []string{appTestConfig().ListenAddress, appTestConfig().ExpectedDatabaseRole, appTestConfig().DatabaseURL} {
		if strings.Contains(output, sentinel) {
			t.Fatalf("logs disclosed %q", sentinel)
		}
	}
}

func TestRunTreatsHTTPServerClosedAsNormal(t *testing.T) {
	recorder := &lifecycleRecorder{serveErr: http.ErrServerClosed}
	if err := Run(context.Background(), appTestConfig(), security.NewLogger(ioDiscard{}), recorder.dependencies()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func appTestConfig() config.Config {
	return config.Config{
		DatabaseURL:           "postgres://viewer:database-secret-sentinel@127.0.0.1/sub2api?sslmode=disable",
		ExpectedDatabaseRole:  "role-secret-sentinel",
		ListenAddress:         "127.0.0.1:18090",
		HTTPReadHeaderTimeout: time.Second,
		HTTPReadTimeout:       2 * time.Second,
		HTTPWriteTimeout:      3 * time.Second,
		HTTPIdleTimeout:       4 * time.Second,
		ShutdownTimeout:       250 * time.Millisecond,
	}
}

type lifecycleRecorder struct {
	mu               sync.Mutex
	events           []string
	openErr          error
	preflightErr     error
	listenErr        error
	serveErr         error
	serveUntilClosed bool
	onServe          func()
	onPreflight      func()
	poolCloses       int
	shutdownCalls    int
	shutdownDeadline time.Duration
	listenNetwork    string
	listenAddress    string
	server           *http.Server
	listener         *recordingListener
}

func (r *lifecycleRecorder) dependencies() Dependencies {
	r.listener = newRecordingListener(r.record)
	return Dependencies{
		OpenPool: func(context.Context, config.Config) (*pgxpool.Pool, error) {
			r.record("open")
			if r.openErr != nil {
				return nil, r.openErr
			}
			return &pgxpool.Pool{}, nil
		},
		OpenLightPool: func(context.Context, config.Config) (*pgxpool.Pool, error) {
			r.record("open-light")
			if r.openErr != nil {
				return nil, r.openErr
			}
			return &pgxpool.Pool{}, nil
		},
		Preflight: func(context.Context, *pgxpool.Pool, config.Config) error {
			r.record("preflight")
			if r.onPreflight != nil {
				r.onPreflight()
			}
			return r.preflightErr
		},
		ClosePool: func(*pgxpool.Pool) {
			r.record("pool-close")
			r.mu.Lock()
			r.poolCloses++
			r.mu.Unlock()
		},
		Listen: func(network, address string) (net.Listener, error) {
			r.record("listen")
			r.listenNetwork = network
			r.listenAddress = address
			if r.listenErr != nil {
				return nil, r.listenErr
			}
			return r.listener, nil
		},
		Serve: func(server *http.Server, listener net.Listener) error {
			r.record("serve")
			r.mu.Lock()
			r.server = server
			r.mu.Unlock()
			if r.onServe != nil {
				r.onServe()
			}
			if r.serveUntilClosed {
				<-r.listener.closed
				return http.ErrServerClosed
			}
			return r.serveErr
		},
		Shutdown: func(ctx context.Context, _ *http.Server) error {
			r.record("shutdown")
			r.mu.Lock()
			r.shutdownCalls++
			if deadline, ok := ctx.Deadline(); ok {
				r.shutdownDeadline = time.Until(deadline)
			}
			r.mu.Unlock()
			return nil
		},
		LightPreflight: func(context.Context, *pgxpool.Pool, config.Config) error {
			r.record("light-preflight")
			return r.preflightErr
		},
		NewHandler: func(*pgxpool.Pool, config.Config) http.Handler {
			r.record("handler")
			return http.NotFoundHandler()
		},
		NewCredentialHandler: func(*config.Config, func(string, creds.Entry) error) http.Handler {
			return http.NotFoundHandler()
		},
	}
}

func (r *lifecycleRecorder) record(event string) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *lifecycleRecorder) eventsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *lifecycleRecorder) serverSnapshot() *http.Server {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.server
}

type recordingListener struct {
	mu      sync.Mutex
	closes  int
	closed  chan struct{}
	onClose func(string)
}

func newRecordingListener(onClose func(string)) *recordingListener {
	return &recordingListener{closed: make(chan struct{}), onClose: onClose}
}

func (l *recordingListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *recordingListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closes++
	if l.closes == 1 {
		close(l.closed)
		l.onClose("listener-close")
	}
	return nil
}

func (*recordingListener) Addr() net.Addr {
	return testAddress("127.0.0.1:18090")
}

type testAddress string

func (address testAddress) Network() string { return "tcp" }
func (address testAddress) String() string  { return string(address) }

type ioDiscard struct{}

func (ioDiscard) Write(buffer []byte) (int, error) { return len(buffer), nil }

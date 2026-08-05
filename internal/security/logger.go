package security

import (
	"io"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

const (
	AddressClassLoopback                = "loopback"
	AddressClassAcknowledgedNonLoopback = "acknowledged_non_loopback"
)

type Logger struct {
	logger *slog.Logger
}

func NewLogger(output io.Writer) *Logger {
	if output == nil {
		output = io.Discard
	}
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{ReplaceAttr: replaceAttribute})
	return &Logger{logger: slog.New(handler)}
}

func (l *Logger) Ready(addressClass string) {
	if l == nil || l.logger == nil || !validAddressClass(addressClass) {
		return
	}
	l.logger.Info("ready", slog.String("address_class", addressClass))
}

func (l *Logger) Stopping() {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Info("stopping")
}

func (l *Logger) Failure(diagnostic *diagnostics.Diagnostic) {
	if l == nil || l.logger == nil {
		return
	}
	if diagnostic == nil {
		diagnostic = diagnostics.New(diagnostics.CodeServer, diagnostics.CategoryServer, "")
	}
	l.logger.Error(
		"failure",
		slog.String("code", string(diagnostic.Code())),
		slog.String("category", string(diagnostic.Category())),
		slog.String("message", diagnostic.Message()),
	)
}

func validAddressClass(addressClass string) bool {
	return addressClass == AddressClassLoopback || addressClass == AddressClassAcknowledgedNonLoopback
}

func replaceAttribute(_ []string, attribute slog.Attr) slog.Attr {
	switch attribute.Key {
	case slog.TimeKey:
		attribute.Key = "timestamp"
		attribute.Value = slog.TimeValue(attribute.Value.Time().UTC())
	case slog.MessageKey:
		attribute.Key = "event"
	}
	return attribute
}

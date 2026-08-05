package diagnostics

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDiagnosticsExposeOnlyCanonicalPublicValues(t *testing.T) {
	tests := []struct {
		name     string
		code     Code
		category Category
		message  string
	}{
		{name: "configuration", code: CodeConfiguration, category: CategoryConfiguration, message: "runtime configuration is invalid"},
		{name: "connectivity", code: CodeDatabaseConnectivity, category: CategoryDatabaseConnectivity, message: "database connection could not be established"},
		{name: "privilege", code: CodeDatabasePrivilege, category: CategoryDatabasePrivilege, message: "database role is not admitted"},
		{name: "read only", code: CodeDatabaseReadOnly, category: CategoryDatabaseReadOnly, message: "database read-only verification failed"},
		{name: "schema", code: CodeSchemaCompatibility, category: CategorySchemaCompatibility, message: "database schema is not compatible"},
		{name: "bind", code: CodeUnsafeBind, category: CategoryUnsafeBind, message: "listen address is not safely acknowledged"},
		{name: "server", code: CodeServer, category: CategoryServer, message: "server could not start safely"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diagnostic := New(tt.code, Category("caller-category-secret"), "caller-message-secret")
			if diagnostic.Code() != tt.code {
				t.Errorf("Code() = %q, want %q", diagnostic.Code(), tt.code)
			}
			if diagnostic.Category() != tt.category {
				t.Errorf("Category() = %q, want %q", diagnostic.Category(), tt.category)
			}
			if diagnostic.Message() != tt.message {
				t.Errorf("Message() = %q, want %q", diagnostic.Message(), tt.message)
			}
			if got, want := diagnostic.Error(), string(tt.code)+": "+tt.message; got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
			assertNoDiagnosticSentinel(t, diagnostic.Error(), "caller-category-secret", "caller-message-secret")
		})
	}
}

func TestWrappedCauseCannotEscapePublicFormatting(t *testing.T) {
	const causeSentinel = "postgres-raw-cause-secret-sentinel"
	diagnostic := Wrap(CodeDatabaseConnectivity, CategoryServer, "caller-message-secret", errors.New(causeSentinel))

	outputs := []string{
		diagnostic.Error(),
		diagnostic.Message(),
		fmt.Sprintf("%s", diagnostic),
		fmt.Sprintf("%v", diagnostic),
		fmt.Sprintf("%+v", diagnostic),
		fmt.Sprintf("%q", diagnostic),
	}
	for _, output := range outputs {
		assertNoDiagnosticSentinel(t, output, causeSentinel, "caller-message-secret")
	}

	if _, ok := any(diagnostic).(interface{ Unwrap() error }); ok {
		t.Fatal("Diagnostic must not expose its private cause through Unwrap")
	}
}

func TestCodeAndCategoryHelpersDefaultUnknownErrorsToServer(t *testing.T) {
	wrapped := fmt.Errorf("outer wrapper: %w", New(CodeDatabasePrivilege, CategoryServer, "ignored"))
	if got := CodeOf(wrapped); got != CodeDatabasePrivilege {
		t.Errorf("CodeOf(wrapped) = %q", got)
	}
	if got := CategoryOf(wrapped); got != CategoryDatabasePrivilege {
		t.Errorf("CategoryOf(wrapped) = %q", got)
	}

	for _, err := range []error{nil, errors.New("unknown-error-secret-sentinel")} {
		if got := CodeOf(err); got != CodeServer {
			t.Errorf("CodeOf(%v) = %q, want %q", err, got, CodeServer)
		}
		if got := CategoryOf(err); got != CategoryServer {
			t.Errorf("CategoryOf(%v) = %q, want %q", err, got, CategoryServer)
		}
	}
}

func TestUnknownDiagnosticCodeFailsClosedToServer(t *testing.T) {
	diagnostic := Wrap(Code("unknown-code-secret"), Category("unknown-category-secret"), "unknown-message-secret", errors.New("unknown-cause-secret"))
	if diagnostic.Code() != CodeServer || diagnostic.Category() != CategoryServer {
		t.Fatalf("unknown diagnostic = %q/%q", diagnostic.Code(), diagnostic.Category())
	}
	assertNoDiagnosticSentinel(t, diagnostic.Error(), "unknown-code-secret", "unknown-category-secret", "unknown-message-secret", "unknown-cause-secret")
}

func assertNoDiagnosticSentinel(t *testing.T, output string, sentinels ...string) {
	t.Helper()
	for _, sentinel := range sentinels {
		if strings.Contains(output, sentinel) {
			t.Errorf("diagnostic output disclosed sentinel %q", sentinel)
		}
	}
}

package diagnostics

import (
	"errors"
	"fmt"
	"strconv"
)

type Code string

type Category string

const (
	CodeConfiguration        Code = "UV-CFG-001"
	CodeDatabaseConnectivity Code = "UV-DB-001"
	CodeDatabasePrivilege    Code = "UV-ROLE-001"
	CodeDatabaseReadOnly     Code = "UV-RO-001"
	CodeSchemaCompatibility  Code = "UV-SCHEMA-001"
	CodeUnsafeBind           Code = "UV-BIND-001"
	CodeServer               Code = "UV-SERVER-001"
)

const (
	CategoryConfiguration        Category = "configuration"
	CategoryDatabaseConnectivity Category = "database_connectivity"
	CategoryDatabasePrivilege    Category = "database_privilege"
	CategoryDatabaseReadOnly     Category = "database_read_only"
	CategorySchemaCompatibility  Category = "schema_compatibility"
	CategoryUnsafeBind           Category = "unsafe_bind"
	CategoryServer               Category = "server"
)

const (
	messageConfiguration        = "runtime configuration is invalid"
	messageDatabaseConnectivity = "database connection could not be established"
	messageDatabasePrivilege    = "database role is not admitted"
	messageDatabaseReadOnly     = "database read-only verification failed"
	messageSchemaCompatibility  = "database schema is not compatible"
	messageUnsafeBind           = "listen address is not safely acknowledged"
	messageServer               = "server could not start safely"
)

type Diagnostic struct {
	code     Code
	category Category
	message  string
	cause    error
}

// New canonicalizes the supplied code. Category and message arguments remain in
// the contract for call-site clarity but can never introduce public text.
func New(code Code, _ Category, _ string) *Diagnostic {
	canonicalCode, category, message := canonicalValues(code)
	return &Diagnostic{code: canonicalCode, category: category, message: message}
}

// Wrap retains cause for internal classification without exposing or unwrapping it.
func Wrap(code Code, category Category, message string, cause error) *Diagnostic {
	diagnostic := New(code, category, message)
	diagnostic.cause = cause
	return diagnostic
}

func (d *Diagnostic) Error() string {
	return string(d.Code()) + ": " + d.Message()
}

func (d *Diagnostic) Code() Code {
	if d == nil {
		return CodeServer
	}
	code, _, _ := canonicalValues(d.code)
	return code
}

func (d *Diagnostic) Category() Category {
	if d == nil {
		return CategoryServer
	}
	_, category, _ := canonicalValues(d.code)
	return category
}

func (d *Diagnostic) Message() string {
	if d == nil {
		return messageServer
	}
	_, _, message := canonicalValues(d.code)
	return message
}

// Format prevents debug-style formatting from reflecting the private cause.
func (d *Diagnostic) Format(state fmt.State, verb rune) {
	public := d.Error()
	if verb == 'q' {
		public = strconv.Quote(public)
	}
	_, _ = state.Write([]byte(public))
}

func CodeOf(err error) Code {
	var diagnostic *Diagnostic
	if errors.As(err, &diagnostic) && diagnostic != nil {
		return diagnostic.Code()
	}
	return CodeServer
}

func CategoryOf(err error) Category {
	var diagnostic *Diagnostic
	if errors.As(err, &diagnostic) && diagnostic != nil {
		return diagnostic.Category()
	}
	return CategoryServer
}

func canonicalValues(code Code) (Code, Category, string) {
	switch code {
	case CodeConfiguration:
		return CodeConfiguration, CategoryConfiguration, messageConfiguration
	case CodeDatabaseConnectivity:
		return CodeDatabaseConnectivity, CategoryDatabaseConnectivity, messageDatabaseConnectivity
	case CodeDatabasePrivilege:
		return CodeDatabasePrivilege, CategoryDatabasePrivilege, messageDatabasePrivilege
	case CodeDatabaseReadOnly:
		return CodeDatabaseReadOnly, CategoryDatabaseReadOnly, messageDatabaseReadOnly
	case CodeSchemaCompatibility:
		return CodeSchemaCompatibility, CategorySchemaCompatibility, messageSchemaCompatibility
	case CodeUnsafeBind:
		return CodeUnsafeBind, CategoryUnsafeBind, messageUnsafeBind
	case CodeServer:
		return CodeServer, CategoryServer, messageServer
	default:
		return CodeServer, CategoryServer, messageServer
	}
}

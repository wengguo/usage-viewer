package tests

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	viewerdiagnostics "github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
	viewerpostgres "github.com/Wei-Shaw/sub2api/usage-viewer/internal/postgres"
)

func TestOperatorReadmeMatchesRuntimeContract(t *testing.T) {
	readme := readOperationFile(t, "../README.md")

	environmentKeys := []string{
		"SUB2API_USAGE_VIEWER_DATABASE_URL",
		"SUB2API_USAGE_VIEWER_DATABASE_ROLE",
		"SUB2API_USAGE_VIEWER_DATA_DIR",
		"SUB2API_USAGE_VIEWER_LISTEN_ADDR",
		"SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK",
		"SUB2API_USAGE_VIEWER_DB_CONNECT_TIMEOUT",
		"SUB2API_USAGE_VIEWER_DB_ACQUIRE_TIMEOUT",
		"SUB2API_USAGE_VIEWER_DB_QUERY_TIMEOUT",
		"SUB2API_USAGE_VIEWER_DB_POOL_MAX_CONNS",
		"SUB2API_USAGE_VIEWER_DB_POOL_MIN_CONNS",
		"SUB2API_USAGE_VIEWER_DB_MAX_CONN_LIFETIME",
		"SUB2API_USAGE_VIEWER_DB_MAX_CONN_IDLE_TIME",
		"SUB2API_USAGE_VIEWER_DB_HEALTH_CHECK_PERIOD",
		"SUB2API_USAGE_VIEWER_MAX_QUERY_RANGE",
		"SUB2API_USAGE_VIEWER_HTTP_READ_HEADER_TIMEOUT",
		"SUB2API_USAGE_VIEWER_HTTP_READ_TIMEOUT",
		"SUB2API_USAGE_VIEWER_HTTP_WRITE_TIMEOUT",
		"SUB2API_USAGE_VIEWER_HTTP_IDLE_TIMEOUT",
		"SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT",
		"SUB2API_USAGE_VIEWER_AUTH_USERNAME",
		"SUB2API_USAGE_VIEWER_AUTH_PASSWORD",
	}
	for _, key := range environmentKeys {
		if !strings.Contains(readme, key) {
			t.Errorf("README missing environment variable %q", key)
		}
	}
	assertExactSourceStrings(t, "../internal/config/config.go", "SUB2API_USAGE_VIEWER_", environmentKeys)

	for _, required := range []string{
		"go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer",
		"http://127.0.0.1:8081/",
		"http://127.0.0.1:8081/livez",
		"http://127.0.0.1:8081/readyz",
		"SIGINT", "SIGTERM", `{"status":"ok"}`,
		`"address_class"`, `"loopback"`, `"acknowledged_non_loopback"`,
		"go test ./... -count=1",
		"go test -race ./... -count=1",
		"go vet ./...",
		"go test -tags=integration ./... -count=1 -timeout=120s",
		"not a production runtime dependency",
		"no production Node.js runtime, Redis, Docker",
		"A skipped database or browser check is not a pass.",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README missing runtime instruction %q", required)
		}
	}

	assertConfigDocumentation(t, readme)
	assertDiagnosticDocumentation(t, readme)
	assertHealthAndAddressDocumentation(t, readme)

	for _, unsafe := range []string{
		"password-secret-sentinel",
		"postgresql://postgres:postgres@",
		"Docker is required to run the viewer",
		"Redis is required",
		"npm run",
	} {
		if strings.Contains(readme, unsafe) {
			t.Errorf("README contains unsafe or unsupported instruction %q", unsafe)
		}
	}
}

func TestRoleGuideMatchesCurrentColumnContract(t *testing.T) {
	guide := readOperationFile(t, "../docs/least-privilege-role.sql")
	contract := viewerpostgres.CurrentContract()
	columnsByTable := make(map[string][]string)
	tableOrder := make([]string, 0, len(contract.Relations))
	for _, relation := range contract.Relations {
		tableOrder = append(tableOrder, relation.Name)
	}
	for _, column := range contract.Columns {
		columnsByTable[column.Table] = append(columnsByTable[column.Table], column.Column)
	}
	if len(contract.Columns) != 17 {
		t.Fatalf("live contract has %d columns, documentation must be reviewed", len(contract.Columns))
	}
	expectedStatements := []string{
		"CREATE ROLE sub2api_usage_viewer LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;",
		"ALTER ROLE sub2api_usage_viewer SET default_transaction_read_only = on;",
		"GRANT CONNECT ON DATABASE <sub2api_database> TO sub2api_usage_viewer;",
		"GRANT USAGE ON SCHEMA public TO sub2api_usage_viewer;",
	}
	for _, table := range tableOrder {
		expectedStatements = append(expectedStatements, fmt.Sprintf(
			"GRANT SELECT (%s) ON TABLE public.%s TO sub2api_usage_viewer;",
			strings.Join(columnsByTable[table], ", "), table,
		))
	}
	assertExactSQLStatements(t, guide, expectedStatements)

	for _, required := range []string{
		"CREATE ROLE sub2api_usage_viewer",
		"LOGIN", "NOSUPERUSER", "NOCREATEDB", "NOCREATEROLE", "NOINHERIT", "NOREPLICATION", "NOBYPASSRLS",
		"ALTER ROLE sub2api_usage_viewer SET default_transaction_read_only = on;",
		"GRANT CONNECT ON DATABASE <sub2api_database> TO sub2api_usage_viewer;",
		"GRANT USAGE ON SCHEMA public TO sub2api_usage_viewer;",
		"PUBLIC privileges apply to every role",
		"does not change application tables, columns, indexes, policies, or rows",
	} {
		if !strings.Contains(guide, required) {
			t.Errorf("role guide missing safety contract %q", required)
		}
	}

}

func readOperationFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func sourceConfigSettings(t *testing.T, path string) map[string]documentedSetting {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	constants := stringConstants(parsed)
	settings := make(map[string]documentedSetting)
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		function, ok := call.Fun.(*ast.Ident)
		if !ok || (function.Name != "valueOrDefault" && function.Name != "positiveDuration" && function.Name != "boundedInt32") || len(call.Args) < 3 {
			return true
		}
		keyIdentifier, keyOK := call.Args[1].(*ast.Ident)
		defaultLiteral, defaultOK := call.Args[2].(*ast.BasicLit)
		key, constantOK := constants[keyIdentifier.Name]
		if !keyOK || !defaultOK || !constantOK || !strings.HasPrefix(key, "SUB2API_USAGE_VIEWER_") {
			return true
		}
		defaultValue, err := strconv.Unquote(defaultLiteral.Value)
		if err != nil {
			t.Fatalf("unquote default for %s: %v", key, err)
		}
		setting := documentedSetting{defaultValue: defaultValue, validator: function.Name}
		if function.Name == "boundedInt32" && len(call.Args) == 5 {
			setting.minimum = expressionText(t, fileSet, call.Args[3])
			setting.maximum = expressionText(t, fileSet, call.Args[4])
		}
		settings[key] = setting
		return true
	})
	return settings
}

func sourceDiagnosticExitCodes(t *testing.T, path string, diagnosticConstants map[string]string) map[string]int {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	exits := make(map[string]int)
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "exitCode" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok || len(clause.List) == 0 || len(clause.Body) != 1 {
				return true
			}
			result, ok := clause.Body[0].(*ast.ReturnStmt)
			if !ok || len(result.Results) != 1 {
				return true
			}
			literal, ok := result.Results[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.INT {
				return true
			}
			exit, err := strconv.Atoi(literal.Value)
			if err != nil {
				t.Fatalf("parse exit code %q: %v", literal.Value, err)
			}
			for _, expression := range clause.List {
				selector, ok := expression.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if code, ok := diagnosticConstants[selector.Sel.Name]; ok {
					exits[code] = exit
				}
			}
			return true
		})
	}
	return exits
}

func sourceHealthRoutes(t *testing.T, path string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var routes []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		selector, selectorOK := call.Fun.(*ast.SelectorExpr)
		handler, handlerOK := call.Args[1].(*ast.Ident)
		pathLiteral, pathOK := call.Args[0].(*ast.BasicLit)
		if !selectorOK || selector.Sel.Name != "HandleFunc" || !handlerOK || handler.Name != "serveHealth" || !pathOK {
			return true
		}
		route, err := strconv.Unquote(pathLiteral.Value)
		if err != nil {
			t.Fatalf("unquote health route: %v", err)
		}
		routes = append(routes, route)
		return true
	})
	if len(routes) != 2 {
		t.Errorf("runtime registers %d health routes; documentation test requires explicit review", len(routes))
	}
	return routes
}

func sourceStringConstants(t *testing.T, path string) map[string]string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return stringConstants(parsed)
}

func stringConstants(parsed *ast.File) map[string]string {
	constants := make(map[string]string)
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range valueSpec.Names {
				if index >= len(valueSpec.Values) {
					continue
				}
				literal, ok := valueSpec.Values[index].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				if value, err := strconv.Unquote(literal.Value); err == nil {
					constants[name.Name] = value
				}
			}
		}
	}
	return constants
}

func expressionText(t *testing.T, fileSet *token.FileSet, expression ast.Expr) string {
	t.Helper()
	var output bytes.Buffer
	if err := printer.Fprint(&output, fileSet, expression); err != nil {
		t.Fatalf("format source expression: %v", err)
	}
	return output.String()
}

func assertExactSourceStrings(t *testing.T, path, prefix string, expected []string) {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	actual := make(map[string]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.HasPrefix(value, prefix) {
			actual[value] = struct{}{}
		}
		return true
	})
	want := make(map[string]struct{}, len(expected))
	for _, value := range expected {
		want[value] = struct{}{}
	}
	for value := range actual {
		if _, ok := want[value]; !ok {
			t.Errorf("%s defines undocumented %s value %q", path, prefix, value)
		}
	}
	for value := range want {
		if _, ok := actual[value]; !ok {
			t.Errorf("documentation expects missing %s value %q in %s", prefix, value, path)
		}
	}
}

type documentedSetting struct {
	defaultValue string
	validator    string
	minimum      string
	maximum      string
	description  string
}

func assertConfigDocumentation(t *testing.T, readme string) {
	t.Helper()
	path := "../internal/config/config.go"
	actual := sourceConfigSettings(t, path)
	expected := map[string]documentedSetting{
		"SUB2API_USAGE_VIEWER_LISTEN_ADDR":              {"127.0.0.1:8081", "valueOrDefault", "", "", "numeric IP and port only"},
		"SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK": {"false", "valueOrDefault", "", "", "exactly `true` or `false`"},
		"SUB2API_USAGE_VIEWER_DB_CONNECT_TIMEOUT":       {"5s", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_DB_ACQUIRE_TIMEOUT":       {"2s", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_DB_QUERY_TIMEOUT":         {"5s", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_DB_POOL_MAX_CONNS":        {"4", "boundedInt32", "1", "4", "integer from 1 through 4"},
		"SUB2API_USAGE_VIEWER_DB_POOL_MIN_CONNS":        {"0", "boundedInt32", "0", "databasePoolMaxConns", "integer from 0 through the configured maximum"},
		"SUB2API_USAGE_VIEWER_DB_MAX_CONN_LIFETIME":     {"30m", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_DB_MAX_CONN_IDLE_TIME":    {"5m", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_DB_HEALTH_CHECK_PERIOD":   {"1m", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_MAX_QUERY_RANGE":          {"720h", "positiveDuration", "", "", "from `1h` through `720h`"},
		"SUB2API_USAGE_VIEWER_HTTP_READ_HEADER_TIMEOUT": {"5s", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_HTTP_READ_TIMEOUT":        {"10s", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_HTTP_WRITE_TIMEOUT":       {"15s", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_HTTP_IDLE_TIMEOUT":        {"60s", "positiveDuration", "", "", "positive Go duration"},
		"SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT":         {"10s", "positiveDuration", "", "", "positive Go duration"},
	}
	if len(actual) != len(expected) {
		t.Errorf("runtime defines %d optional settings, documentation expects %d", len(actual), len(expected))
	}
	for key, want := range expected {
		got, ok := actual[key]
		if !ok {
			t.Errorf("runtime optional setting missing for documented key %q", key)
			continue
		}
		if got.defaultValue != want.defaultValue || got.validator != want.validator || got.minimum != want.minimum || got.maximum != want.maximum {
			t.Errorf("runtime contract for %s = %#v, documentation contract = %#v", key, got, want)
		}
		row := fmt.Sprintf("| `%s` | `%s`; %s |", key, want.defaultValue, want.description)
		if strings.Count(readme, row) != 1 {
			t.Errorf("README must contain exactly one keyed configuration row %q", row)
		}
	}
	configSource := readOperationFile(t, path)
	for _, required := range []string{
		`parseStrictBool(valueOrDefault(lookup, acknowledgeNonLoopbackKey, "false"))`,
		`ValidateListenAddress(listenAddress, acknowledgeNonLoopback)`,
		`maximumQueryRange < time.Hour || maximumQueryRange > 720*time.Hour`,
	} {
		if !strings.Contains(configSource, required) {
			t.Errorf("runtime validation semantics changed; review documentation contract %q", required)
		}
	}
}

func assertDiagnosticDocumentation(t *testing.T, readme string) {
	t.Helper()
	diagnosticConstants := sourceStringConstants(t, "../internal/diagnostics/diagnostics.go")
	exitCodes := sourceDiagnosticExitCodes(t, "../cmd/viewer/main.go", diagnosticConstants)
	codeCount := 0
	for name, code := range diagnosticConstants {
		if !strings.HasPrefix(name, "Code") || !strings.HasPrefix(code, "UV-") {
			continue
		}
		codeCount++
		exit, ok := exitCodes[code]
		if !ok {
			t.Errorf("runtime diagnostic %s (%s) has no explicit exit mapping", name, code)
			continue
		}
		message := viewerdiagnostics.New(viewerdiagnostics.Code(code), viewerdiagnostics.Category(""), "").Message()
		message = strings.ToUpper(message[:1]) + message[1:]
		row := fmt.Sprintf("| %d | `%s` | %s |", exit, code, message)
		if strings.Count(readme, row) != 1 {
			t.Errorf("README must contain exactly one runtime-derived diagnostic row %q", row)
		}
	}
	if codeCount != len(exitCodes) {
		t.Errorf("runtime has %d diagnostics but %d explicit exit mappings", codeCount, len(exitCodes))
	}
}

func assertHealthAndAddressDocumentation(t *testing.T, readme string) {
	t.Helper()
	for _, route := range sourceHealthRoutes(t, "../internal/httpapi/handler.go") {
		url := "http://127.0.0.1:8081" + route
		if strings.Count(readme, url) != 1 {
			t.Errorf("README must contain exactly one runtime health URL %q", url)
		}
	}
	healthConstants := sourceStringConstants(t, "../internal/httpapi/health.go")
	response, ok := healthConstants["healthResponse"]
	if !ok || strings.Count(readme, response) != 1 {
		t.Errorf("README health response does not match runtime value %q", response)
	}
	addressConstants := sourceStringConstants(t, "../internal/security/logger.go")
	addressCount := 0
	for name, value := range addressConstants {
		if !strings.HasPrefix(name, "AddressClass") {
			continue
		}
		addressCount++
		if strings.Count(readme, `"address_class":"`+value+`"`) != 1 {
			t.Errorf("README must contain exactly one runtime address class %q", value)
		}
	}
	if addressCount != 2 {
		t.Errorf("runtime defines %d address classes; documentation test requires explicit review", addressCount)
	}
}

func assertExactSQLStatements(t *testing.T, guide string, expected []string) {
	t.Helper()
	var executableLines []string
	for _, line := range strings.Split(guide, "\n") {
		if comment := strings.Index(line, "--"); comment >= 0 {
			line = line[:comment]
		}
		if line = strings.TrimSpace(line); line != "" {
			executableLines = append(executableLines, line)
		}
	}
	normalized := strings.Join(strings.Fields(strings.Join(executableLines, " ")), " ")
	actual := make(map[string]int)
	for _, statement := range strings.Split(normalized, ";") {
		if statement = strings.TrimSpace(statement); statement != "" {
			actual[statement+";"]++
		}
	}
	want := make(map[string]struct{}, len(expected))
	for _, statement := range expected {
		want[statement] = struct{}{}
	}
	for statement, count := range actual {
		if _, ok := want[statement]; !ok {
			t.Errorf("role guide contains unapproved executable statement %q", statement)
		}
		if count != 1 {
			t.Errorf("role guide contains executable statement %q %d times", statement, count)
		}
	}
	for statement := range want {
		if actual[statement] != 1 {
			t.Errorf("role guide missing exact executable statement %q", statement)
		}
	}
}

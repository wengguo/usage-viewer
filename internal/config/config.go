package config

import (
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

const (
	databaseURLKey               = "SUB2API_USAGE_VIEWER_DATABASE_URL"
	databaseRoleKey              = "SUB2API_USAGE_VIEWER_DATABASE_ROLE"
	listenAddressKey             = "SUB2API_USAGE_VIEWER_LISTEN_ADDR"
	acknowledgeNonLoopbackKey    = "SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK"
	databaseConnectTimeoutKey    = "SUB2API_USAGE_VIEWER_DB_CONNECT_TIMEOUT"
	databaseAcquireTimeoutKey    = "SUB2API_USAGE_VIEWER_DB_ACQUIRE_TIMEOUT"
	databaseQueryTimeoutKey      = "SUB2API_USAGE_VIEWER_DB_QUERY_TIMEOUT"
	databasePoolMaxConnsKey      = "SUB2API_USAGE_VIEWER_DB_POOL_MAX_CONNS"
	databasePoolMinConnsKey      = "SUB2API_USAGE_VIEWER_DB_POOL_MIN_CONNS"
	databaseMaxConnLifetimeKey   = "SUB2API_USAGE_VIEWER_DB_MAX_CONN_LIFETIME"
	databaseMaxConnIdleTimeKey   = "SUB2API_USAGE_VIEWER_DB_MAX_CONN_IDLE_TIME"
	databaseHealthCheckPeriodKey = "SUB2API_USAGE_VIEWER_DB_HEALTH_CHECK_PERIOD"
	maximumQueryRangeKey         = "SUB2API_USAGE_VIEWER_MAX_QUERY_RANGE"
	httpReadHeaderTimeoutKey     = "SUB2API_USAGE_VIEWER_HTTP_READ_HEADER_TIMEOUT"
	httpReadTimeoutKey           = "SUB2API_USAGE_VIEWER_HTTP_READ_TIMEOUT"
	httpWriteTimeoutKey          = "SUB2API_USAGE_VIEWER_HTTP_WRITE_TIMEOUT"
	httpIdleTimeoutKey           = "SUB2API_USAGE_VIEWER_HTTP_IDLE_TIMEOUT"
	shutdownTimeoutKey           = "SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT"
	dataDirKey                   = "SUB2API_USAGE_VIEWER_DATA_DIR"

	// Redis connection (reuses the Sub2API environment variable names).
	redisHostKey     = "REDIS_HOST"
	redisPortKey     = "REDIS_PORT"
	redisPasswordKey = "REDIS_PASSWORD"
	redisDBKey       = "REDIS_DB"
)

var allowedDatabaseURLParameters = map[string]struct{}{
	"sslmode":     {},
	"sslrootcert": {},
	"sslcert":     {},
	"sslkey":      {},
}

type LookupEnv func(string) (string, bool)

type Config struct {
	DatabaseURL               string
	ExpectedDatabaseRole      string
	CredentialSource          string
	DataDir                   string
	ListenAddress             string
	AcknowledgeNonLoopback    bool
	DatabaseConnectTimeout    time.Duration
	DatabaseAcquireTimeout    time.Duration
	DatabaseQueryTimeout      time.Duration
	DatabasePoolMaxConns      int32
	DatabasePoolMinConns      int32
	DatabaseMaxConnLifetime   time.Duration
	DatabaseMaxConnIdleTime   time.Duration
	DatabaseHealthCheckPeriod time.Duration
	MaximumQueryRange         time.Duration
	HTTPReadHeaderTimeout     time.Duration
	HTTPReadTimeout           time.Duration
	HTTPWriteTimeout          time.Duration
	HTTPIdleTimeout           time.Duration
	ShutdownTimeout           time.Duration
	RedisHost                 string
	RedisPort                 int
	RedisPassword             string
	RedisDB                   int
}

func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, configurationError()
	}

	databaseURL, _ := requiredValue(lookup, databaseURLKey)
	if databaseURL != "" && !validateDatabaseURL(databaseURL) {
		return Config{}, configurationError()
	}
	databaseRole, _ := requiredValue(lookup, databaseRoleKey)
	dataDir := resolveDataDir(lookup)

	listenAddress := valueOrDefault(lookup, listenAddressKey, "127.0.0.1:8081")
	acknowledgeNonLoopback, ok := parseStrictBool(valueOrDefault(lookup, acknowledgeNonLoopbackKey, "false"))
	if !ok {
		return Config{}, configurationError()
	}
	if err := ValidateListenAddress(listenAddress, acknowledgeNonLoopback); err != nil {
		return Config{}, err
	}

	databaseConnectTimeout, ok := positiveDuration(lookup, databaseConnectTimeoutKey, "5s")
	if !ok {
		return Config{}, configurationError()
	}
	databaseAcquireTimeout, ok := positiveDuration(lookup, databaseAcquireTimeoutKey, "2s")
	if !ok {
		return Config{}, configurationError()
	}
	databaseQueryTimeout, ok := positiveDuration(lookup, databaseQueryTimeoutKey, "5s")
	if !ok {
		return Config{}, configurationError()
	}
	databasePoolMaxConns, ok := boundedInt32(lookup, databasePoolMaxConnsKey, "4", 1, 4)
	if !ok {
		return Config{}, configurationError()
	}
	databasePoolMinConns, ok := boundedInt32(lookup, databasePoolMinConnsKey, "0", 0, databasePoolMaxConns)
	if !ok {
		return Config{}, configurationError()
	}
	databaseMaxConnLifetime, ok := positiveDuration(lookup, databaseMaxConnLifetimeKey, "30m")
	if !ok {
		return Config{}, configurationError()
	}
	databaseMaxConnIdleTime, ok := positiveDuration(lookup, databaseMaxConnIdleTimeKey, "5m")
	if !ok {
		return Config{}, configurationError()
	}
	databaseHealthCheckPeriod, ok := positiveDuration(lookup, databaseHealthCheckPeriodKey, "1m")
	if !ok {
		return Config{}, configurationError()
	}
	maximumQueryRange, ok := positiveDuration(lookup, maximumQueryRangeKey, "720h")
	if !ok || maximumQueryRange < time.Hour || maximumQueryRange > 720*time.Hour {
		return Config{}, configurationError()
	}
	httpReadHeaderTimeout, ok := positiveDuration(lookup, httpReadHeaderTimeoutKey, "5s")
	if !ok {
		return Config{}, configurationError()
	}
	httpReadTimeout, ok := positiveDuration(lookup, httpReadTimeoutKey, "10s")
	if !ok {
		return Config{}, configurationError()
	}
	httpWriteTimeout, ok := positiveDuration(lookup, httpWriteTimeoutKey, "15s")
	if !ok {
		return Config{}, configurationError()
	}
	httpIdleTimeout, ok := positiveDuration(lookup, httpIdleTimeoutKey, "60s")
	if !ok {
		return Config{}, configurationError()
	}
	shutdownTimeout, ok := positiveDuration(lookup, shutdownTimeoutKey, "10s")
	if !ok {
		return Config{}, configurationError()
	}

	// Redis is optional; it is used only to resolve current concurrency.
	redisHost := valueOrDefault(lookup, redisHostKey, "")
	redisPort := 6379
	if redisPortText, ok := lookup(redisPortKey); ok {
		if parsed, err := strconv.Atoi(redisPortText); err == nil && parsed > 0 {
			redisPort = parsed
		}
	}
	redisPassword, _ := lookup(redisPasswordKey)
	redisDB := 0
	if redisDBText, ok := lookup(redisDBKey); ok {
		if parsed, err := strconv.Atoi(redisDBText); err == nil && parsed >= 0 {
			redisDB = parsed
		}
	}

	return Config{
		DatabaseURL:               databaseURL,
		ExpectedDatabaseRole:      databaseRole,
		CredentialSource:          "",
		DataDir:                   dataDir,
		ListenAddress:             listenAddress,
		AcknowledgeNonLoopback:    acknowledgeNonLoopback,
		DatabaseConnectTimeout:    databaseConnectTimeout,
		DatabaseAcquireTimeout:    databaseAcquireTimeout,
		DatabaseQueryTimeout:      databaseQueryTimeout,
		DatabasePoolMaxConns:      databasePoolMaxConns,
		DatabasePoolMinConns:      databasePoolMinConns,
		DatabaseMaxConnLifetime:   databaseMaxConnLifetime,
		DatabaseMaxConnIdleTime:   databaseMaxConnIdleTime,
		DatabaseHealthCheckPeriod: databaseHealthCheckPeriod,
		MaximumQueryRange:         maximumQueryRange,
		HTTPReadHeaderTimeout:     httpReadHeaderTimeout,
		HTTPReadTimeout:           httpReadTimeout,
		HTTPWriteTimeout:          httpWriteTimeout,
		HTTPIdleTimeout:           httpIdleTimeout,
		ShutdownTimeout:           shutdownTimeout,
		RedisHost:                 redisHost,
		RedisPort:                 redisPort,
		RedisPassword:             redisPassword,
		RedisDB:                   redisDB,
	}, nil
}

func ValidateListenAddress(address string, acknowledgeNonLoopback bool) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return bindError()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return bindError()
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return bindError()
	}

	if ip.IsLoopback() == acknowledgeNonLoopback {
		return bindError()
	}
	return nil
}

func requiredValue(lookup LookupEnv, key string) (string, bool) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func valueOrDefault(lookup LookupEnv, key, defaultValue string) string {
	if value, ok := lookup(key); ok {
		return value
	}
	return defaultValue
}

func parseStrictBool(value string) (bool, bool) {
	switch value {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func positiveDuration(lookup LookupEnv, key, defaultValue string) (time.Duration, bool) {
	value := valueOrDefault(lookup, key, defaultValue)
	duration, err := time.ParseDuration(value)
	return duration, err == nil && duration > 0
}

func boundedInt32(lookup LookupEnv, key, defaultValue string, minimum, maximum int32) (int32, bool) {
	value := valueOrDefault(lookup, key, defaultValue)
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, false
	}
	result := int32(parsed)
	return result, result >= minimum && result <= maximum
}

func validateDatabaseURL(databaseURL string) bool {
	parsed, host, ok := parseDatabaseURL(databaseURL)
	if !ok || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.Fragment != "" {
		return false
	}

	parameters, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range parameters {
		if _, allowed := allowedDatabaseURLParameters[key]; !allowed || len(values) != 1 || values[0] == "" {
			return false
		}
	}

	if host == "" {
		return false
	}
	if strings.HasPrefix(host, "/") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return parameters.Get("sslmode") == "verify-full" && strings.TrimSpace(parameters.Get("sslrootcert")) != ""
}

func parseDatabaseURL(databaseURL string) (*url.URL, string, bool) {
	parsed, err := url.Parse(databaseURL)
	if err == nil {
		return parsed, parsed.Hostname(), true
	}

	schemeEnd := strings.Index(databaseURL, "://")
	if schemeEnd < 0 {
		return nil, "", false
	}
	authorityStart := schemeEnd + len("://")
	authorityEnd := strings.IndexAny(databaseURL[authorityStart:], "/?#")
	if authorityEnd < 0 {
		return nil, "", false
	}
	authorityEnd += authorityStart
	authority := databaseURL[authorityStart:authorityEnd]
	hostStart := strings.LastIndex(authority, "@") + 1
	decodedHost, decodeErr := url.PathUnescape(authority[hostStart:])
	if decodeErr != nil || !strings.HasPrefix(decodedHost, "/") {
		return nil, "", false
	}

	// Go's URL parser rejects an escaped slash in an authority. Substitute a
	// validation-only literal after independently proving this is a socket path.
	normalized := databaseURL[:authorityStart] + authority[:hostStart] + "127.0.0.1" + databaseURL[authorityEnd:]
	parsed, err = url.Parse(normalized)
	if err != nil {
		return nil, "", false
	}
	return parsed, decodedHost, true
}

func resolveDataDir(lookup LookupEnv) string {
	if dir, ok := lookup(dataDirKey); ok && strings.TrimSpace(dir) != "" {
		return dir
	}
	if _, err := os.Stat("/app/data"); err == nil {
		return "/app/data"
	}
	return "."
}

func configurationError() error {
	return diagnostics.New(
		diagnostics.CodeConfiguration,
		diagnostics.CategoryConfiguration,
		"runtime configuration is invalid",
	)
}

func bindError() error {
	return diagnostics.New(
		diagnostics.CodeUnsafeBind,
		diagnostics.CategoryUnsafeBind,
		"listen address is not safely acknowledged",
	)
}

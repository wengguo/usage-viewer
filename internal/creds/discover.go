package creds

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConnectionCandidates returns the supplied entry plus localhost fallbacks
// when the host looks like a Docker-internal service name (e.g. "postgres")
// that does not resolve from a host machine.
func ConnectionCandidates(entry Entry) []Entry {
	candidates := []Entry{entry}
	host := strings.ToLower(strings.TrimSpace(entry.Host))
	if host != "localhost" && host != "127.0.0.1" && host != "::1" && net.ParseIP(host) == nil {
		for _, fallback := range []string{"127.0.0.1", "localhost"} {
			clone := entry
			clone.Host = fallback
			candidates = append(candidates, clone)
		}
	}
	return candidates
}

// Source indicates where credentials were obtained.
type Source string

const (
	SourceEnv    Source = "env"
	SourceConfig Source = "config"
	SourceSaved  Source = "saved"
	SourceManual Source = "manual"
)

// Result holds discovered credentials and their source.
type Result struct {
	Entry  Entry
	Source Source
}

// sub2apiDBConfig matches the database section of Sub2API's config.yaml.
type sub2apiDBConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

type sub2apiConfig struct {
	Database sub2apiDBConfig `yaml:"database"`
}

// Discover returns all candidate database credentials in priority order:
// 1. Environment variable DATABASE_URL
// 2. Sub2API's config.yaml (looked up in common locations)
// 3. Sub2API .env file (docker-compose environment)
// 4. Previously saved credentials file in dataDir
// Callers should try each candidate (and its host fallbacks) until one
// successfully connects.
func Discover(lookup func(string) (string, bool), dataDir string) ([]Result, error) {
	var results []Result
	if result, ok := fromEnv(lookup); ok {
		results = append(results, result)
	}
	if result, ok := fromSub2APIConfig(dataDir); ok {
		results = append(results, result)
	}
	if result, ok := fromDotEnvFile(dataDir); ok {
		results = append(results, result)
	}
	if result, ok := fromSavedFile(dataDir); ok {
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no database credentials found")
	}
	return results, nil
}

func fromEnv(lookup func(string) (string, bool)) (Result, bool) {
	// Try DATABASE_URL first
	if dbURL, ok := lookup("SUB2API_USAGE_VIEWER_DATABASE_URL"); ok && strings.TrimSpace(dbURL) != "" {
		entry, err := parseDatabaseURL(dbURL)
		if err != nil {
			return Result{}, false
		}
		return Result{Entry: entry, Source: SourceEnv}, true
	}

	// Try individual connection parameters
	host, ok := lookup("DATABASE_HOST")
	if !ok {
		return Result{}, false
	}
	user, ok := lookup("DATABASE_USER")
	if !ok {
		return Result{}, false
	}
	password, _ := lookup("DATABASE_PASSWORD")
	dbName, _ := lookup("DATABASE_DBNAME")
	if dbName == "" {
		dbName = "sub2api"
	}
	sslMode, _ := lookup("DATABASE_SSLMODE")
	if sslMode == "" {
		sslMode = "disable"
	}
	port := 5432
	if portStr, ok := lookup("DATABASE_PORT"); ok {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}
	return Result{
		Entry:  Entry{Host: host, Port: port, User: user, Password: password, DBName: dbName, SSLMode: sslMode},
		Source: SourceEnv,
	}, true
}

func fromSub2APIConfig(dataDir string) (Result, bool) {
	// Look in common locations relative to dataDir
	candidates := []string{
		filepath.Join(dataDir, "config.yaml"),
		filepath.Join(dataDir, "..", "data", "config.yaml"),
		filepath.Join(dataDir, "..", "config.yaml"),
		"/app/data/config.yaml",
		"data/config.yaml",
		"../data/config.yaml",
	}
	for _, path := range candidates {
		result, ok := trySub2APIConfig(path)
		if ok {
			return result, true
		}
	}
	return Result{}, false
}

func trySub2APIConfig(path string) (Result, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, false
	}
	var cfg sub2apiConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Result{}, false
	}
	db := cfg.Database
	if db.Host == "" || db.User == "" || db.DBName == "" {
		return Result{}, false
	}
	if db.Port <= 0 {
		db.Port = 5432
	}
	if db.SSLMode == "" {
		db.SSLMode = "disable"
	}
	return Result{
		Entry: Entry{
			Host:     db.Host,
			Port:     db.Port,
			User:     db.User,
			Password: db.Password,
			DBName:   db.DBName,
			SSLMode:  db.SSLMode,
		},
		Source: SourceConfig,
	}, true
}

func fromSavedFile(dataDir string) (Result, bool) {
	entry, err := LoadSaved(dataDir)
	if err != nil {
		return Result{}, false
	}
	return Result{Entry: entry, Source: SourceSaved}, true
}

func fromDotEnvFile(dataDir string) (Result, bool) {
	candidates := []string{
		filepath.Join(dataDir, ".env"),
		filepath.Join(dataDir, "..", ".env"),
		filepath.Join("/app", ".env"),
	}
	for _, path := range candidates {
		result, ok := tryDotEnvFile(path)
		if ok {
			return result, true
		}
	}
	return Result{}, false
}

func tryDotEnvFile(path string) (Result, bool) {
	params, err := readDotEnv(path)
	if err != nil {
		return Result{}, false
	}
	user := params["POSTGRES_USER"]
	password := params["POSTGRES_PASSWORD"]
	dbName := params["POSTGRES_DB"]
	if user == "" || dbName == "" {
		return Result{}, false
	}
	if dbName == "" {
		dbName = "sub2api"
	}
	host := params["DATABASE_HOST"]
	if host == "" {
		host = "127.0.0.1"
	}
	port := 5432
	if portStr := params["DATABASE_PORT"]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}
	sslMode := params["DATABASE_SSLMODE"]
	if sslMode == "" {
		sslMode = "disable"
	}
	return Result{
		Entry: Entry{
			Host: host, Port: port, User: user, Password: password,
			DBName: dbName, SSLMode: sslMode,
		},
		Source: SourceConfig,
	}, true
}

func readDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	params := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if value == "" {
			continue
		}
		params[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return params, nil
}

func parseDatabaseURL(dbURL string) (Entry, error) {
	parsed, err := url.Parse(dbURL)
	if err != nil {
		return Entry{}, fmt.Errorf("parse database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return Entry{}, fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	host := parsed.Hostname()
	port := 5432
	if p := parsed.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil {
			return Entry{}, fmt.Errorf("invalid port: %w", err)
		}
	}
	user := parsed.User.Username()
	password, _ := parsed.User.Password()
	dbName := strings.TrimPrefix(parsed.Path, "/")
	sslMode := parsed.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}
	return Entry{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		DBName:   dbName,
		SSLMode:  sslMode,
	}, nil
}

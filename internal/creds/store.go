package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const storeFileName = ".usage-viewer-creds.json"

// Entry holds database connection details.
type Entry struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

// DSN returns a PostgreSQL connection string.
func (e Entry) DSN() string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=%s",
		e.User, e.Password, e.Host, e.Port, e.DBName, e.SSLMode,
	)
}

// Save persists credentials to dataDir.
func Save(dataDir string, entry Entry) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dataDir, storeFileName)
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}
	return nil
}

// LoadSaved reads previously saved credentials from dataDir.
// Returns the zero Entry and an error if the file does not exist or is invalid.
func LoadSaved(dataDir string) (Entry, error) {
	path := filepath.Join(dataDir, storeFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("read credentials file: %w", err)
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("unmarshal credentials: %w", err)
	}
	if entry.Host == "" || entry.User == "" || entry.DBName == "" {
		return Entry{}, fmt.Errorf("saved credentials are incomplete")
	}
	if entry.Port <= 0 || entry.Port > 65535 {
		entry.Port = 5432
	}
	if entry.SSLMode == "" {
		entry.SSLMode = "disable"
	}
	return entry, nil
}

// StoreFilePath returns the full path to the credentials store file.
func StoreFilePath(dataDir string) string {
	return filepath.Join(dataDir, storeFileName)
}

// FromURL parses a PostgreSQL connection URL into an Entry.
func FromURL(databaseURL string) (Entry, error) {
	return parseDatabaseURL(databaseURL)
}

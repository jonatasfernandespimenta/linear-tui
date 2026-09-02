package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrCredentialsNotFound indicates there is no credentials file on disk.
var ErrCredentialsNotFound = errors.New("credentials not found")

// CredentialsPath returns the default path for stored OAuth credentials.
func CredentialsPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".linear-tui", "credentials.json"), nil
}

// LoadCredentials reads OAuth credentials from path.
func LoadCredentials(path string) (Credentials, error) {
	if path == "" {
		return Credentials{}, fmt.Errorf("credentials path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, ErrCredentialsNotFound
		}
		return Credentials{}, fmt.Errorf("read credentials: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("parse credentials: %w", err)
	}
	if creds.AccessToken == "" {
		return Credentials{}, fmt.Errorf("credentials missing access_token")
	}
	return creds, nil
}

// SaveCredentials writes credentials to path with mode 0600.
func SaveCredentials(path string, creds Credentials) error {
	if path == "" {
		return fmt.Errorf("credentials path is empty")
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// writeFileAtomic writes data to path with mode 0600 through a temporary file
// in the same directory, so a crash mid-write cannot truncate credentials.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp credentials file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best effort cleanup; a no-op once the rename succeeded.
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod credentials: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close credentials: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace credentials: %w", err)
	}
	return nil
}

// DeleteCredentials removes the credentials file. Missing files are ignored.
func DeleteCredentials(path string) error {
	if path == "" {
		return fmt.Errorf("credentials path is empty")
	}
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete credentials: %w", err)
	}
	return nil
}

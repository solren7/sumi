package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localConfig is the on-disk credential store written by `sumi auth`. It holds
// secrets, so the file is created 0600 and never printed back in full.
type localConfig struct {
	APIURL       string `json:"api_url,omitempty"`
	Email        string `json:"email,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// configPath resolves the credential file location: SUMI_CONFIG wins, then
// XDG_CONFIG_HOME, then ~/.config.
func configPath() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("SUMI_CONFIG")); custom != "" {
		return custom, nil
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "sumi", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory, set SUMI_CONFIG to a file path: %w", err)
	}
	return filepath.Join(home, ".config", "sumi", "config.json"), nil
}

// loadConfig returns an empty config when no file exists yet, so every command
// works before the first login.
func loadConfig() (*localConfig, string, error) {
	path, err := configPath()
	if err != nil {
		return nil, "", err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &localConfig{}, path, nil
		}
		return nil, path, err
	}

	cfg := new(localConfig)
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, path, fmt.Errorf("%s is not valid JSON; fix or delete it: %w", path, err)
	}
	return cfg, path, nil
}

// saveConfig writes atomically through a temp file in the same directory, so an
// interrupted write cannot leave a half-written credential file behind.
func saveConfig(cfg *localConfig, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// maskSecret keeps enough of a token to identify it without exposing it.
func maskSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 12 {
		return "****"
	}
	return secret[:12] + "…"
}

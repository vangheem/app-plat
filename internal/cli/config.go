package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultAPIURL = "http://127.0.0.1:30080"

type fileConfig struct {
	APIURL string `json:"api_url"`
	APIKey string `json:"api_key"`
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, ".app-plat", "config.json"), nil
}

func loadFile() (fileConfig, error) {
	path, err := configPath()
	if err != nil {
		return fileConfig{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, err
	}
	var cfg fileConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fileConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func saveFile(cfg fileConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cfg.APIURL = strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// resolve applies flag > env > config file > default. A missing file is fine.
// A corrupt file is an error so a bad config is not silently ignored.
func resolve(flagURL string) (fileConfig, error) {
	cfg, err := loadFile()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fileConfig{}, err
	}
	if env := strings.TrimSpace(os.Getenv("APP_PLAT_API_URL")); env != "" {
		cfg.APIURL = env
	}
	if env := strings.TrimSpace(os.Getenv("APP_PLAT_API_KEY")); env != "" {
		cfg.APIKey = env
	}
	if strings.TrimSpace(flagURL) != "" {
		cfg.APIURL = strings.TrimSpace(flagURL)
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		cfg.APIURL = defaultAPIURL
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	return cfg, nil
}

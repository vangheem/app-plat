package appyaml

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the optional app.yaml users may ship in an archive root.
//
//	name: My App
//	port: 8080
//	persistence:
//	  - postgresql
//	  - blob
//	  - keyvalue
type File struct {
	Name        string   `yaml:"name"`
	Port        int      `yaml:"port"`
	Persistence []string `yaml:"persistence"`
}

var allowedPersistence = map[string]string{
	"postgresql": "postgresql",
	"postgres":   "postgresql",
	"blob":       "blob",
	"s3":         "blob",
	"keyvalue":   "keyvalue",
	"kv":         "keyvalue",
	"redis":      "keyvalue",
	"valkey":     "keyvalue",
}

func Parse(raw string) (File, error) {
	var f File
	if strings.TrimSpace(raw) == "" {
		return f, nil
	}
	if err := yaml.Unmarshal([]byte(raw), &f); err != nil {
		return f, fmt.Errorf("parse app.yaml: %w", err)
	}
	if f.Port < 0 || f.Port > 65535 {
		return f, fmt.Errorf("port out of range")
	}
	norm, err := NormalizePersistence(f.Persistence)
	if err != nil {
		return f, err
	}
	f.Persistence = norm
	return f, nil
}

func NormalizePersistence(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" {
			continue
		}
		canon, ok := allowedPersistence[key]
		if !ok {
			return nil, fmt.Errorf("unknown persistence %q (want postgresql, blob, keyvalue)", item)
		}
		if seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, canon)
	}
	return out, nil
}

func Has(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

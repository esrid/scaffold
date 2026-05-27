package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const manifestPath = ".scaffold/models.json"

// Manifest is the source of truth for all scaffolded models.
type Manifest struct {
	Module string                   `json:"module"`
	DB     string                   `json:"db"` // "sqlite" | "postgres", default "sqlite"
	Models map[string]ManifestModel `json:"models"`
}

// IsPostgres reports whether the project uses Postgres. Empty DB defaults to sqlite.
func (m *Manifest) IsPostgres() bool { return m.DB == "postgres" }

// ManifestModel stores the field snapshot and metadata for a model.
type ManifestModel struct {
	Fields       []ManifestField `json:"fields"`
	TableName    string          `json:"tableName"`
	ScaffoldedAt time.Time       `json:"scaffoldedAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	// MigrationVersion tracks the last goose version number used.
	MigrationVersion int `json:"migrationVersion"`
}

// ManifestField is a serializable snapshot of a Field.
type ManifestField struct {
	Name      string   `json:"name"`
	GoType    string   `json:"goType"`
	SQLType   string   `json:"sqlType"`
	NotNull   bool     `json:"notNull"`
	Modifiers []string `json:"modifiers"`
}

// LoadManifest reads .scaffold/models.json, returning an empty manifest if absent.
func LoadManifest(root string) (*Manifest, error) {
	path := filepath.Join(root, manifestPath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Manifest{Models: map[string]ManifestModel{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("manifest: read: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: parse: %w", err)
	}
	if m.Models == nil {
		m.Models = map[string]ManifestModel{}
	}
	return &m, nil
}

// SaveManifest writes the manifest to .scaffold/models.json.
func SaveManifest(root string, m *Manifest) error {
	dir := filepath.Join(root, ".scaffold")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("manifest: mkdir: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("manifest: marshal: %w", err)
	}

	path := filepath.Join(root, manifestPath)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("manifest: write: %w", err)
	}
	return nil
}

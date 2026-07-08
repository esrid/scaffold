package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const manifestPath = ".scaffold/models.json"

// migrationsDir is where goose .sql files live in a scaffolded project.
const migrationsDir = "internal/adapters/store/migrations"

// Manifest is the source of truth for all scaffolded models.
type Manifest struct {
	Module  string `json:"module"`
	DB      string `json:"db"`       // "sqlite" | "postgres", default "sqlite"
	GRPC    bool   `json:"grpc"`     // true if gRPC support is enabled (legacy flag)
	APIMode string `json:"api_mode"` // "rest" | "ssr" | "grpc"
	// SSREngine selects the SSR view engine: "templ" (default, compiled
	// components) or "html" (html/template + real HTMX, no compile step).
	// Ignored outside SSR mode.
	SSREngine string                   `json:"ssr_engine,omitempty"`
	Models    map[string]ManifestModel `json:"models"`

	// migrationFloor is the highest migration version found on disk at load time.
	// Unexported → never serialized. It keeps newly numbered migrations ahead of
	// files created outside the manifest counter (e.g. hand-written or from the
	// initial boilerplate), preventing duplicate-version collisions in goose.
	migrationFloor int
}

// IsPostgres reports whether the project uses Postgres. Empty DB defaults to sqlite.
func (m *Manifest) IsPostgres() bool { return m.DB == "postgres" }

// IsGRPC reports whether gRPC support is enabled for this project.
func (m *Manifest) IsGRPC() bool { return m.APIMode == "grpc" || m.GRPC }

// IsSSR reports whether the project uses SSR (server-rendered HTML) mode.
func (m *Manifest) IsSSR() bool { return m.APIMode == "ssr" }

// IsREST reports whether the project uses REST (JSON API) mode.
func (m *Manifest) IsREST() bool { return m.APIMode == "rest" || (m.APIMode == "" && !m.GRPC) }

// IsHTMLEngine reports whether SSR mode uses the html/template+HTMX engine
// rather than the default templ engine. Meaningless outside SSR mode.
func (m *Manifest) IsHTMLEngine() bool { return m.SSREngine == "html" }

// ManifestModel stores the field snapshot and metadata for a model.
type ManifestModel struct {
	Fields           []ManifestField `json:"fields"`
	TableName        string          `json:"tableName"`
	ScaffoldedAt     time.Time       `json:"scaffoldedAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	MigrationVersion int             `json:"migrationVersion"`
	NoHandler        bool            `json:"noHandler,omitempty"`
	SkippedOps       []string        `json:"skippedOps,omitempty"`
	SoftDelete       bool            `json:"softDelete,omitempty"`
	UniqueTogether   [][]string      `json:"uniqueTogether,omitempty"`
	// Middleware maps an op name (list/read/create/update/delete) to the
	// ordered list of middleware function names to wrap that op's route
	// with. Sticky across regeneration like fields — see --middleware /
	// --remove-middleware in `scaffold gen --help`.
	Middleware map[string][]string `json:"middleware,omitempty"`
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
		return &Manifest{Models: map[string]ManifestModel{}, migrationFloor: highestMigrationOnDisk(root)}, nil
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
	m.migrationFloor = highestMigrationOnDisk(root)
	return &m, nil
}

// highestMigrationOnDisk returns the largest leading version number among the
// .sql files in the migrations directory, or 0 if it is absent/empty. It makes
// version numbering robust to migrations that exist outside the manifest counter.
func highestMigrationOnDisk(root string) int {
	entries, err := os.ReadDir(filepath.Join(root, migrationsDir))
	if err != nil {
		return 0
	}
	maxVer := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		i := strings.IndexByte(name, '_')
		if i <= 0 {
			continue
		}
		n, err := strconv.Atoi(name[:i])
		if err != nil {
			continue
		}
		if n > maxVer {
			maxVer = n
		}
	}
	return maxVer
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

	// Write atomically (temp file + rename) so a crash mid-write can't leave a
	// truncated models.json behind — the manifest is the source of truth.
	path := filepath.Join(root, manifestPath)
	tmp, err := os.CreateTemp(dir, "models-*.json.tmp")
	if err != nil {
		return fmt.Errorf("manifest: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("manifest: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("manifest: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("manifest: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("manifest: rename: %w", err)
	}
	return nil
}

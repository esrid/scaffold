package parser

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/gertd/go-pluralize"
)

// Model is the fully resolved definition used by generators.
type Model struct {
	Name      string // "Product"
	Fields    []Field
	TableName string // "products"
	IsNew     bool   // false if already in manifest (UPDATE mode)

	// Previous fields from manifest — used to diff for migrations.
	PrevFields []Field
	// Next goose migration version number.
	MigrationVersion int
}

var pluralizeClient = pluralize.NewClient()

// BuildModel validates the model name, resolves the table name,
// and determines CREATE vs UPDATE mode from the manifest.
func BuildModel(name string, fields []Field, manifest *Manifest, tableName string) (*Model, error) {
	if err := validateModelName(name); err != nil {
		return nil, err
	}

	if tableName == "" {
		tableName = pluralizeClient.Plural(strings.ToLower(name))
	}

	m := &Model{
		Name:      name,
		Fields:    fields,
		TableName: tableName,
	}

	existing, exists := manifest.Models[name]
	if exists {
		m.IsNew = false
		m.PrevFields = manifestFieldsToFields(existing.Fields)
		m.MigrationVersion = existing.MigrationVersion + 1
	} else {
		m.IsNew = true
		m.MigrationVersion = nextMigrationVersion(manifest)
	}

	return m, nil
}

// ModelFromEntry rebuilds a Model from a manifest entry (used by destroy).
func ModelFromEntry(name string, entry ManifestModel) (*Model, error) {
	return &Model{
		Name:             name,
		Fields:           manifestFieldsToFields(entry.Fields),
		TableName:        entry.TableName,
		IsNew:            false,
		MigrationVersion: entry.MigrationVersion + 1,
	}, nil
}

// ManifestEntry converts the model back to a manifest-storable entry.
func (m *Model) ManifestEntry() ManifestModel {
	fields := make([]ManifestField, len(m.Fields))
	for i, f := range m.Fields {
		fields[i] = ManifestField{
			Name:      f.Name,
			GoType:    f.GoType,
			SQLType:   f.SQLType,
			NotNull:   f.NotNull,
			Modifiers: f.Modifiers,
		}
	}
	now := time.Now()
	return ManifestModel{
		Fields:           fields,
		TableName:        m.TableName,
		UpdatedAt:        now,
		MigrationVersion: m.MigrationVersion,
	}
}

// Snake returns the snake_case model name (e.g. "ProductVariant" → "product_variant").
func (m *Model) Snake() string {
	return toSnakeCase(m.Name)
}

// Lower returns the lowercase model name (e.g. "Product" → "product").
func (m *Model) Lower() string {
	return strings.ToLower(m.Name)
}

// Receiver returns a one-letter Go receiver (e.g. "Product" → "p").
func (m *Model) Receiver() string {
	return strings.ToLower(string(m.Name[0]))
}

// DiffFields returns added and removed fields between PrevFields and Fields.
func (m *Model) DiffFields() (added, removed []Field) {
	prev := map[string]Field{}
	for _, f := range m.PrevFields {
		prev[f.Name] = f
	}
	curr := map[string]Field{}
	for _, f := range m.Fields {
		curr[f.Name] = f
	}

	for _, f := range m.Fields {
		if _, existed := prev[f.Name]; !existed {
			added = append(added, f)
		}
	}
	for _, f := range m.PrevFields {
		if _, exists := curr[f.Name]; !exists {
			removed = append(removed, f)
		}
	}
	return
}

func validateModelName(name string) error {
	if name == "" {
		return fmt.Errorf("model name cannot be empty")
	}
	if !unicode.IsUpper(rune(name[0])) {
		return fmt.Errorf("model name %q must start with an uppercase letter", name)
	}
	if goKeywords[strings.ToLower(name)] {
		return fmt.Errorf("model name %q is a reserved Go keyword", name)
	}
	return nil
}

func manifestFieldsToFields(mf []ManifestField) []Field {
	fields := make([]Field, len(mf))
	for i, f := range mf {
		fields[i] = Field{
			Name:      f.Name,
			GoType:    f.GoType,
			SQLType:   f.SQLType,
			NotNull:   f.NotNull,
			Modifiers: f.Modifiers,
		}
	}
	return fields
}

func nextMigrationVersion(manifest *Manifest) int {
	// Version 1 is reserved for the boilerplate initial schema on projects created with `scaffold init`.
	// Start model migrations at 2 for those projects (manifest.DB is set), at 1 otherwise (legacy projects).
	max := 0
	if manifest.DB != "" {
		max = 1
	}
	for _, m := range manifest.Models {
		if m.MigrationVersion > max {
			max = m.MigrationVersion
		}
	}
	return max + 1
}

func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteRune('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

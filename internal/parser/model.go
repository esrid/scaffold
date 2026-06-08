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
//
// For an existing model, fields are MERGED into the stored set: a passed field
// whose name already exists updates that field in place, a new name is appended,
// and any field whose name appears in removeFields is dropped. Fields that are
// neither passed nor removed are preserved — passing a subset never silently
// drops columns.
func BuildModel(name string, fields []Field, removeFields []string, manifest *Manifest, tableName string) (*Model, error) {
	if err := validateModelName(name); err != nil {
		return nil, err
	}

	if tableName == "" {
		tableName = pluralizeClient.Plural(strings.ToLower(name))
	}

	m := &Model{
		Name:      name,
		TableName: tableName,
	}

	existing, exists := manifest.Models[name]
	if exists {
		m.IsNew = false
		m.PrevFields = manifestFieldsToFields(existing.Fields)
		merged, err := mergeFields(m.PrevFields, fields, removeFields)
		if err != nil {
			return nil, err
		}
		m.Fields = merged
		added, removed := m.DiffFields()
		if len(added) > 0 || len(removed) > 0 {
			m.MigrationVersion = nextMigrationVersion(manifest)
		} else {
			m.MigrationVersion = existing.MigrationVersion
		}
	} else {
		if len(removeFields) > 0 {
			return nil, fmt.Errorf("cannot use --remove on new model %q", name)
		}
		m.IsNew = true
		if len(fields) == 0 {
			return nil, fmt.Errorf("new model %q requires at least one field", name)
		}
		m.Fields = fields
		m.MigrationVersion = nextMigrationVersion(manifest)
	}

	return m, nil
}

// mergeFields merges passed fields and removals into the previous field set.
// Existing fields keep their order; updated fields are replaced in place; new
// fields are appended; removed fields are dropped. The result must be non-empty.
func mergeFields(prev, passed []Field, removeFields []string) ([]Field, error) {
	remove := map[string]bool{}
	for _, name := range removeFields {
		remove[name] = true
	}

	// Index passed fields by name for in-place updates.
	updates := map[string]Field{}
	var appends []Field
	prevByName := map[string]bool{}
	for _, f := range prev {
		prevByName[f.Name] = true
	}
	for _, f := range passed {
		if remove[f.Name] {
			return nil, fmt.Errorf("field %q is given both as an update and to --remove", f.Name)
		}
		if prevByName[f.Name] {
			updates[f.Name] = f
		} else {
			appends = append(appends, f)
		}
	}

	// Verify every removal targets an existing field.
	for _, name := range removeFields {
		if !prevByName[name] {
			return nil, fmt.Errorf("cannot remove field %q: not present on this model", name)
		}
	}

	merged := make([]Field, 0, len(prev)+len(appends))
	for _, f := range prev {
		if remove[f.Name] {
			continue
		}
		if upd, ok := updates[f.Name]; ok {
			merged = append(merged, upd)
		} else {
			merged = append(merged, f)
		}
	}
	merged = append(merged, appends...)

	if len(merged) == 0 {
		return nil, fmt.Errorf("model would have no fields left after removals")
	}
	return merged, nil
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

// Plural returns the plural lowercase name (e.g. "Product" → "products").
// It matches the table name used in the database.
func (m *Model) Plural() string {
	return m.TableName
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
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return fmt.Errorf("model name %q must contain only alphanumeric characters and underscores", name)
		}
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

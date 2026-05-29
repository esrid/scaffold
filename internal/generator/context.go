package generator

import (
	"fmt"
	"strings"

	"github.com/esrid/scaffold/internal/parser"
)

// templateField is the field view passed to templates.
type templateField struct {
	Name         string // snake_case column name
	GoName       string // PascalCase Go field name
	GoType       string // e.g. "string", "*string", "time.Time"
	SQLType      string
	NotNull      bool
	IsJSON       bool
	IsTime       bool
	HasIndex     bool
	Mods         []string
	SQLModifiers string
}

// domainCtx is the data passed to domainTmpl.
type domainCtx struct {
	Name            string
	Receiver        string
	Fields          []templateField
	NeedsTimeImport bool
	NeedsJSONImport bool
}

// storeGenCtx is the data passed to storeGenTmpl.
type storeGenCtx struct {
	Name               string
	ModulePath         string
	TableName          string
	Fields             []templateField
	SelectCols         string
	InsertCols         string
	InsertPlaceholders string
	UpdateSet          string
	CreateArgs         string
	UpdateArgs         string
	ScanArgs           string
	UpdateIDIdx        int
	NeedsJSON          bool
}

// serviceGenCtx is the data passed to serviceGenTmpl.
type serviceGenCtx struct {
	Name       string
	Lower      string
	ModulePath string
}

// registryModel is one model entry in the registry template.
type registryModel struct {
	Name string
}

// registryCtx is the data passed to registryTmpl.
type registryCtx struct {
	ModulePath string
	Models     []registryModel
}

// migrationCtx is shared by migration templates.
type migrationCtx struct {
	Name      string
	TableName string
	Fields    []templateField
	Added     []templateField
	Removed   []templateField
	IDDef     string // full id column definition, e.g. "id TEXT PRIMARY KEY DEFAULT (uuid7())"
}

// buildTemplateFields converts parser.Field slice to templateField slice, fully DB-aware.
func buildTemplateFields(fields []parser.Field, db string) []templateField {
	out := make([]templateField, len(fields))
	for i, f := range fields {
		sqlType := f.SQLType
		if db == "postgres" {
			sqlType = pgSQLType(f)
		}

		out[i] = templateField{
			Name:         f.Name,
			GoName:       toPascalCase(f.Name),
			GoType:       f.GoType,
			SQLType:      sqlType,
			NotNull:      f.NotNull,
			IsJSON:       strings.Contains(f.GoType, "RawMessage"),
			IsTime:       strings.Contains(f.GoType, "time.Time"),
			HasIndex:     hasIndexModifier(f.Modifiers),
			Mods:         f.Modifiers,
			SQLModifiers: buildSQLModifiers(f.Modifiers, db),
		}
	}
	return out
}

// pgSQLType translates SQLite SQL types to native Postgres types.
func pgSQLType(f parser.Field) string {
	goType := f.GoType
	switch {
	case strings.Contains(goType, "int64"):
		return "BIGINT"
	case goType == "int" || goType == "*int":
		return "INTEGER"
	case strings.Contains(goType, "float64"):
		return "DOUBLE PRECISION"
	case strings.Contains(goType, "bool"):
		return "BOOLEAN"
	case strings.Contains(goType, "time.Time"):
		return "TIMESTAMPTZ"
	case strings.Contains(goType, "RawMessage"):
		return "JSONB"
	default:
		return f.SQLType // default fallback (e.g. TEXT)
	}
}

// buildSQLModifiers resolves field modifiers (like fk, default, unique, check, cascade, setnull) per database.
func buildSQLModifiers(mods []string, db string) string {
	hasCascade := containsMod(mods, "cascade")
	hasSetNull := containsMod(mods, "setnull")

	var parts []string
	for _, m := range mods {
		switch {
		case m == "unique":
			parts = append(parts, "UNIQUE")
		case m == "cascade" || m == "setnull" || m == "index":
			// cascade/setnull consumed by fk= below; index handled separately
		case strings.HasPrefix(m, "default="):
			val := strings.TrimPrefix(m, "default=")
			parts = append(parts, fmt.Sprintf("DEFAULT '%s'", val))
		case strings.HasPrefix(m, "fk="):
			table := strings.ToLower(strings.TrimPrefix(m, "fk="))
			var onDelete string
			switch {
			case hasCascade:
				onDelete = "CASCADE"
			case hasSetNull:
				onDelete = "SET NULL"
			case db == "postgres":
				onDelete = "RESTRICT"
			}
			if onDelete != "" {
				parts = append(parts, fmt.Sprintf("REFERENCES %s(id) ON DELETE %s", table, onDelete))
			} else {
				parts = append(parts, fmt.Sprintf("REFERENCES %s(id)", table))
			}
		case strings.HasPrefix(m, "check="):
			expr := strings.TrimPrefix(m, "check=")
			parts = append(parts, fmt.Sprintf("CHECK (%s)", expr))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

// containsMod reports whether mods contains the exact modifier m.
func containsMod(mods []string, m string) bool {
	for _, mod := range mods {
		if mod == m {
			return true
		}
	}
	return false
}

// hasIndexModifier helper checks if an index should be created.
func hasIndexModifier(mods []string) bool {
	for _, m := range mods {
		if m == "index" {
			return true
		}
	}
	return false
}

// buildDomainCtx builds the context for the domain template.
func buildDomainCtx(model *parser.Model, modulePath string, db string) domainCtx {
	fields := buildTemplateFields(model.Fields, db)
	needsTime := true // always needed for created_at/updated_at
	needsJSON := false
	for _, f := range fields {
		if f.IsJSON {
			needsJSON = true
		}
	}
	return domainCtx{
		Name:            model.Name,
		Receiver:        model.Receiver(),
		Fields:          fields,
		NeedsTimeImport: needsTime,
		NeedsJSONImport: needsJSON,
	}
}

// buildStoreGenCtx builds the context for the store _gen template.
func buildStoreGenCtx(model *parser.Model, modulePath string, db string) storeGenCtx {
	fields := buildTemplateFields(model.Fields, db)

	// SELECT: user fields + id, created_at, updated_at
	allCols := make([]string, 0, len(fields)+3)
	allCols = append(allCols, "id")
	for _, f := range fields {
		allCols = append(allCols, f.Name)
	}
	allCols = append(allCols, "created_at", "updated_at")
	selectCols := strings.Join(allCols, ", ")

	// INSERT: id is always generated by the DB via DEFAULT (uuid7()).
	var insertColsList []string
	var placeholders []string
	for i, f := range fields {
		insertColsList = append(insertColsList, f.Name)
		if db == "postgres" {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		} else {
			placeholders = append(placeholders, "?")
		}
	}
	insertCols := strings.Join(insertColsList, ", ")
	insertPlaceholders := strings.Join(placeholders, ", ")

	// UPDATE SET: user fields only
	updateSetParts := make([]string, len(fields))
	for i, f := range fields {
		if db == "postgres" {
			updateSetParts[i] = fmt.Sprintf("%s = $%d", f.Name, i+1)
		} else {
			updateSetParts[i] = f.Name + " = ?"
		}
	}
	updateSet := strings.Join(updateSetParts, ", ")

	// Create args: id is DB-generated, pass only user fields.
	var createArgParts []string
	for _, f := range fields {
		if f.IsJSON && db != "postgres" {
			createArgParts = append(createArgParts, fmt.Sprintf("func() []byte { b, _ := json.Marshal(p.%s); return b }()", f.GoName))
		} else {
			createArgParts = append(createArgParts, "p."+f.GoName)
		}
	}
	createArgs := strings.Join(createArgParts, ", ")

	// Update args: user fields + p.ID last
	updateArgParts := make([]string, 0, len(fields)+1)
	for _, f := range fields {
		if f.IsJSON && db != "postgres" {
			updateArgParts = append(updateArgParts, fmt.Sprintf("func() []byte { b, _ := json.Marshal(p.%s); return b }()", f.GoName))
		} else {
			updateArgParts = append(updateArgParts, "p."+f.GoName)
		}
	}
	updateArgParts = append(updateArgParts, "p.ID")
	updateArgs := strings.Join(updateArgParts, ", ")

	// Scan args: &p.ID, &p.UserField..., &p.CreatedAt, &p.UpdatedAt
	scanArgParts := make([]string, 0, len(fields)+3)
	scanArgParts = append(scanArgParts, "&p.ID")
	for _, f := range fields {
		if f.IsJSON {
			scanArgParts = append(scanArgParts, "&"+f.GoName+"Bytes")
		} else {
			scanArgParts = append(scanArgParts, "&p."+f.GoName)
		}
	}
	scanArgParts = append(scanArgParts, "&p.CreatedAt", "&p.UpdatedAt")
	scanArgs := strings.Join(scanArgParts, ", ")

	needsJSON := false
	for _, f := range fields {
		if f.IsJSON {
			needsJSON = true
		}
	}

	return storeGenCtx{
		Name:               model.Name,
		ModulePath:         modulePath,
		TableName:          model.TableName,
		Fields:             fields,
		SelectCols:         selectCols,
		InsertCols:         insertCols,
		InsertPlaceholders: insertPlaceholders,
		UpdateSet:          updateSet,
		CreateArgs:         createArgs,
		UpdateArgs:         updateArgs,
		ScanArgs:           scanArgs,
		UpdateIDIdx:        len(fields) + 1,
		NeedsJSON:          needsJSON,
	}
}

// goInitialisms are common abbreviations that should stay all-caps in Go identifiers.
var goInitialisms = map[string]string{
	"acl": "ACL", "api": "API", "ascii": "ASCII", "cpu": "CPU", "css": "CSS",
	"dns": "DNS", "eof": "EOF", "guid": "GUID", "html": "HTML", "http": "HTTP",
	"https": "HTTPS", "id": "ID", "ip": "IP", "json": "JSON", "qps": "QPS",
	"ram": "RAM", "rpc": "RPC", "sku": "SKU", "sla": "SLA", "smtp": "SMTP",
	"sql": "SQL", "ssh": "SSH", "tcp": "TCP", "tls": "TLS", "ttl": "TTL",
	"udp": "UDP", "ui": "UI", "uid": "UID", "uuid": "UUID", "uri": "URI",
	"url": "URL", "utf8": "UTF8", "vm": "VM", "xml": "XML", "xss": "XSS",
}

// toPascalCase converts snake_case to PascalCase, respecting Go initialisms.
func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		lower := strings.ToLower(p)
		if initialism, ok := goInitialisms[lower]; ok {
			b.WriteString(initialism)
		} else {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

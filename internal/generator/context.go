package generator

import (
	"fmt"
	"strings"

	"github.com/esrid/scaffold/internal/parser"
)

// templateField is the field view passed to templates.
type templateField struct {
	Name         string // snake_case column name
	GoName       string // PascalCase Go field name (idiomatic, acronym-aware: sku -> SKU)
	ProtoGoName  string // Go name as protoc-gen-go derives it from the proto field (sku -> Sku)
	GoType       string // e.g. "string", "*string", "time.Time"
	SQLType      string
	NotNull      bool
	IsJSON       bool
	IsTime       bool
	IsPointer    bool   // true when GoType starts with "*"
	ProtoType    string // e.g. "string", "optional int32", "google.protobuf.Timestamp"
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
	GRPC       bool
	IsSSR      bool
}

// ssrHandlerCtx is the data passed to ssrHandlerTmpl.
type ssrHandlerCtx struct {
	ModulePath   string
	Name         string
	Lower        string
	Plural       string
	Fields       []templateField
	NeedsStrconv bool // true when any field requires strconv (int/float/bool)
}

// protoCtx is the data passed to protoTmpl.
type protoCtx struct {
	ModulePath   string
	Name         string
	Lower        string
	Fields       []templateField
	CreatedAtIdx int
	UpdatedAtIdx int
}

// grpcHandlerCtx is the data passed to grpcHandlerTmpl.
type grpcHandlerCtx struct {
	ModulePath string
	Name       string
	Lower      string
	Fields     []templateField
	NeedsTime  bool // true when a nullable time field needs the "time" import
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

		isPointer := strings.HasPrefix(f.GoType, "*")
		out[i] = templateField{
			Name:         f.Name,
			GoName:       toPascalCase(f.Name),
			ProtoGoName:  protoGoCamelCase(f.Name),
			GoType:       f.GoType,
			SQLType:      sqlType,
			NotNull:      f.NotNull,
			IsJSON:       strings.Contains(f.GoType, "RawMessage"),
			IsTime:       strings.Contains(f.GoType, "time.Time"),
			IsPointer:    isPointer,
			ProtoType:    protoType(f.GoType),
			HasIndex:     hasIndexModifier(f.Modifiers),
			Mods:         f.Modifiers,
			SQLModifiers: buildSQLModifiers(f.Modifiers, db),
		}
	}
	return out
}

// protoType maps a Go type string to the corresponding proto3 type declaration.
func protoType(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "*string":
		return "optional string"
	case "int":
		return "int32"
	case "*int":
		return "optional int32"
	case "int64":
		return "int64"
	case "*int64":
		return "optional int64"
	case "float64":
		return "double"
	case "*float64":
		return "optional double"
	case "bool":
		return "bool"
	case "*bool":
		return "optional bool"
	case "time.Time", "*time.Time":
		return "google.protobuf.Timestamp"
	case "json.RawMessage":
		return "bytes"
	default:
		return "string"
	}
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

// protoGoCamelCase mirrors protoc-gen-go's strs.GoCamelCase so generated gRPC
// code references the exact field/getter names the protobuf compiler emits.
// Unlike toPascalCase it is NOT acronym-aware: "sku" -> "Sku", "user_id" -> "UserId".
// Keep this in sync with google.golang.org/protobuf/internal/strs.GoCamelCase.
func protoGoCamelCase(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.' && i+1 < len(s) && isASCIILower(s[i+1]):
			// Skip over '.' in ".{{lowercase}}".
		case c == '.':
			b = append(b, '_') // convert '.' to '_'
		case c == '_' && (i == 0 || s[i-1] == '.'):
			b = append(b, 'X') // convert leading '_' to "X"
		case c == '_' && i+1 < len(s) && isASCIILower(s[i+1]):
			// Skip over '_' in "_{{lowercase}}".
		case isASCIIDigit(c):
			b = append(b, c)
		default:
			if isASCIILower(c) {
				c -= 'a' - 'A' // uppercase the first letter of the word
			}
			b = append(b, c)
			// Append the rest of the lowercase run as-is.
			for ; i+1 < len(s) && isASCIILower(s[i+1]); i++ {
				b = append(b, s[i+1])
			}
		}
	}
	return string(b)
}

func isASCIILower(c byte) bool { return 'a' <= c && c <= 'z' }
func isASCIIDigit(c byte) bool { return '0' <= c && c <= '9' }

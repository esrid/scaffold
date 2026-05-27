package parser

import (
	"fmt"
	"strings"
)

// Field represents a parsed model field from CLI args.
type Field struct {
	Name      string
	GoType    string // e.g. "string", "*string", "time.Time"
	SQLType   string // e.g. "TEXT", "INTEGER", "REAL", "DATETIME"
	NotNull   bool
	Modifiers []string // "unique", "index", "default=pending", "fk=users"
}

// goKeywords is the set of reserved Go identifiers.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// autoManagedFields are always injected — users must not declare them.
var autoManagedFields = map[string]bool{
	"id": true, "created_at": true, "updated_at": true,
}

// typeMap maps CLI type aliases to canonical Go and SQL types.
var typeMap = map[string][2]string{
	"string":   {"string", "TEXT"},
	"text":     {"string", "TEXT"},
	"int":      {"int", "INTEGER"},
	"int64":    {"int64", "INTEGER"},
	"float":    {"float64", "REAL"},
	"float64":  {"float64", "REAL"},
	"bool":     {"bool", "INTEGER"},
	"time":     {"time.Time", "DATETIME"},
	"datetime": {"time.Time", "DATETIME"},
	"json":     {"json.RawMessage", "TEXT"},
}

// ParseFields parses a slice of "name:type{mod}!" strings into Fields.
func ParseFields(args []string) ([]Field, error) {
	seen := map[string]bool{}
	fields := make([]Field, 0, len(args))

	for _, arg := range args {
		f, err := parseField(arg)
		if err != nil {
			return nil, err
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("duplicate field %q", f.Name)
		}
		seen[f.Name] = true
		fields = append(fields, f)
	}
	return fields, nil
}

// parseField parses a single "name:type{mod,mod}!" token.
func parseField(arg string) (Field, error) {
	notNull := strings.HasSuffix(arg, "!")
	arg = strings.TrimSuffix(arg, "!")

	// Split name from type+modifiers
	colonIdx := strings.Index(arg, ":")
	if colonIdx < 0 {
		return Field{}, fmt.Errorf("invalid field %q: expected format name:type", arg)
	}
	name := arg[:colonIdx]
	rest := arg[colonIdx+1:]

	if err := validateFieldName(name); err != nil {
		return Field{}, err
	}

	// Extract modifiers from {mod,mod}
	var modifiers []string
	if braceStart := strings.Index(rest, "{"); braceStart >= 0 {
		braceEnd := strings.Index(rest, "}")
		if braceEnd < 0 {
			return Field{}, fmt.Errorf("field %q: unclosed modifier brace", name)
		}
		modStr := rest[braceStart+1 : braceEnd]
		rest = rest[:braceStart]
		for _, m := range strings.Split(modStr, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				modifiers = append(modifiers, m)
			}
		}
	}

	typeName := strings.ToLower(strings.TrimSpace(rest))
	types, ok := typeMap[typeName]
	if !ok {
		return Field{}, fmt.Errorf("unknown type %q for field %q — valid types: %s",
			typeName, name, validTypes())
	}

	goType := types[0]
	// nullable fields use pointer types (except json.RawMessage)
	if !notNull && goType != "json.RawMessage" {
		goType = "*" + goType
	}

	return Field{
		Name:      name,
		GoType:    goType,
		SQLType:   types[1],
		NotNull:   notNull,
		Modifiers: modifiers,
	}, nil
}

func validateFieldName(name string) error {
	if autoManagedFields[name] {
		return fmt.Errorf("field %q is auto-managed (id, created_at, updated_at) — remove it", name)
	}
	if goKeywords[name] {
		return fmt.Errorf("field %q is a reserved Go keyword", name)
	}
	if name == "" {
		return fmt.Errorf("field name cannot be empty")
	}
	return nil
}

func validTypes() string {
	types := []string{"string", "text", "int", "int64", "float", "float64", "bool", "time", "datetime", "json"}
	return strings.Join(types, ", ")
}

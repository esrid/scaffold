package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
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

// arrayElementTypes is the set of CLI types permitted as array elements.
// time and json are excluded: a JSON column already stores arbitrary structures.
var arrayElementTypes = map[string]bool{
	"string": true, "text": true, "int": true, "int64": true,
	"float": true, "float64": true, "bool": true,
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

	// Detect "[]base" array syntax (e.g. "[]string", "[]int").
	isArray := strings.HasPrefix(typeName, "[]")
	if isArray {
		typeName = strings.TrimSpace(typeName[2:])
		if !arrayElementTypes[typeName] {
			return Field{}, fmt.Errorf("type %q cannot be used as an array element for field %q — valid element types: %s",
				typeName, name, validArrayElementTypes())
		}
	}

	types, ok := typeMap[typeName]
	if !ok {
		return Field{}, fmt.Errorf("unknown type %q for field %q — valid types: %s",
			typeName, name, validTypes())
	}

	goType := types[0]
	sqlType := types[1]

	if isArray {
		// Slice Go type; stored as a JSON-encoded TEXT column on SQLite and a
		// native array on Postgres (resolved per-DB in the generator).
		goType = "[]" + goType
		sqlType = "TEXT"
	}

	// Process special modifiers: nn (notnull alias), numeric size (VARCHAR), others pass through.
	filtered := make([]string, 0, len(modifiers))
	varcharLen := 0
	for _, m := range modifiers {
		switch {
		case m == "nn":
			notNull = true
		case isNumeric(m):
			if varcharLen != 0 {
				return Field{}, fmt.Errorf("field %q: multiple size modifiers", name)
			}
			n, _ := strconv.Atoi(m)
			if n <= 0 {
				return Field{}, fmt.Errorf("field %q: size modifier must be positive", name)
			}
			varcharLen = n
		case m == "unique" || m == "index" || m == "cascade" || m == "setnull":
			filtered = append(filtered, m)
		case strings.HasPrefix(m, "default="):
			filtered = append(filtered, m)
		case strings.HasPrefix(m, "fk="):
			filtered = append(filtered, m)
		case strings.HasPrefix(m, "check="):
			filtered = append(filtered, m)
		default:
			return Field{}, fmt.Errorf("field %q: unknown modifier %q", name, m)
		}
	}
	if varcharLen > 0 {
		if isArray || (typeName != "string" && typeName != "text") {
			return Field{}, fmt.Errorf("field %q: size modifier only valid for string/text types", name)
		}
		sqlType = fmt.Sprintf("VARCHAR(%d)", varcharLen)
	}
	modifiers = filtered

	// Validate FK-dependent modifiers.
	hasCascade, hasSetNull, hasFK := false, false, false
	for _, m := range modifiers {
		switch {
		case m == "cascade":
			hasCascade = true
		case m == "setnull":
			hasSetNull = true
		case strings.HasPrefix(m, "fk="):
			hasFK = true
		}
	}
	if (hasCascade || hasSetNull) && !hasFK {
		return Field{}, fmt.Errorf("field %q: cascade/setnull requires fk= modifier", name)
	}
	if hasCascade && hasSetNull {
		return Field{}, fmt.Errorf("field %q: cascade and setnull are mutually exclusive", name)
	}

	// nullable fields use pointer types (except json.RawMessage and slices,
	// which are already nilable).
	if !notNull && goType != "json.RawMessage" && !isArray {
		goType = "*" + goType
	}

	return Field{
		Name:      name,
		GoType:    goType,
		SQLType:   sqlType,
		NotNull:   notNull,
		Modifiers: modifiers,
	}, nil
}

func validateFieldName(name string) error {
	if name == "" {
		return fmt.Errorf("field name cannot be empty")
	}
	if autoManagedFields[name] {
		return fmt.Errorf("field %q is auto-managed (id, created_at, updated_at) — remove it", name)
	}
	if goKeywords[name] {
		return fmt.Errorf("field %q is a reserved Go keyword", name)
	}
	if !unicode.IsLetter(rune(name[0])) && rune(name[0]) != '_' {
		return fmt.Errorf("field %q must start with a letter or underscore", name)
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return fmt.Errorf("field %q must contain only alphanumeric characters and underscores", name)
		}
	}
	return nil
}

func validTypes() string {
	types := []string{"string", "text", "int", "int64", "float", "float64", "bool", "time", "datetime", "json"}
	return strings.Join(types, ", ")
}

func validArrayElementTypes() string {
	return "string, text, int, int64, float, float64, bool"
}

// isNumeric reports whether s is a non-empty string of ASCII digits.
func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

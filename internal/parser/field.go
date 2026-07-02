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
// Checked against the field name normalized to lowercase with underscores
// removed, so spellings like "Id", "ID" or "createdAt" are caught too: they
// would produce the same Go struct field (ID/CreatedAt/UpdatedAt) or, on
// SQLite's case-insensitive columns, a duplicate column.
var autoManagedFields = map[string]bool{
	"id": true, "createdat": true, "updatedat": true,
}

// sqlReservedWords are identifiers that cannot be used unquoted as column or
// table names in SQLite and/or Postgres. The generated SQL never quotes
// identifiers, so these are rejected with a clear error instead of producing
// migrations and queries that fail at runtime.
var sqlReservedWords = map[string]bool{
	"abort": true, "action": true, "add": true, "after": true, "all": true,
	"alter": true, "analyze": true, "and": true, "any": true, "as": true,
	"asc": true, "attach": true, "autoincrement": true, "before": true,
	"begin": true, "between": true, "both": true, "by": true, "cascade": true,
	"case": true, "cast": true, "check": true, "collate": true, "column": true,
	"commit": true, "conflict": true, "constraint": true, "create": true,
	"cross": true, "current_date": true, "current_time": true,
	"current_timestamp": true, "current_user": true, "database": true,
	"default": true, "deferrable": true, "deferred": true, "delete": true,
	"desc": true, "detach": true, "distinct": true, "do": true, "drop": true,
	"each": true, "else": true, "end": true, "escape": true, "except": true,
	"exclusive": true, "exists": true, "explain": true, "fail": true,
	"filter": true, "for": true, "foreign": true, "from": true, "full": true,
	"glob": true, "group": true, "having": true, "ignore": true,
	"immediate": true, "in": true, "index": true, "indexed": true,
	"initially": true, "inner": true, "insert": true, "instead": true,
	"intersect": true, "into": true, "is": true, "isnull": true, "join": true,
	"key": true, "lateral": true, "leading": true, "left": true, "like": true,
	"limit": true, "localtime": true, "localtimestamp": true, "match": true,
	"natural": true, "no": true, "not": true, "notnull": true, "null": true,
	"of": true, "offset": true, "on": true, "only": true, "or": true,
	"order": true, "outer": true, "over": true, "partition": true,
	"plan": true, "pragma": true, "primary": true, "query": true,
	"raise": true, "recursive": true, "references": true, "regexp": true,
	"reindex": true, "release": true, "rename": true, "replace": true,
	"restrict": true, "right": true, "rollback": true, "row": true,
	"rows": true, "savepoint": true, "session_user": true, "set": true,
	"some": true, "table": true, "temp": true, "temporary": true,
	"then": true, "to": true, "trailing": true, "transaction": true,
	"trigger": true, "union": true, "unique": true, "update": true,
	"user": true, "using": true, "vacuum": true, "values": true, "view": true,
	"virtual": true, "when": true, "where": true, "window": true, "with": true,
	"without": true,
}

// typeMap maps CLI type aliases to canonical Go and SQL types.
var typeMap = map[string][2]string{
	"string":   {"string", "TEXT"},
	"text":     {"string", "TEXT"},
	"uuid":     {"string", "UUID"},
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

	// Extract modifiers. Two accepted forms, both reducing to a list of tokens:
	//   legacy brace form (needs shell quoting in zsh):  type{mod,mod}
	//   shell-safe comma form:                           type,mod,mod
	var modifiers []string
	if braceStart := strings.Index(rest, "{"); braceStart >= 0 {
		braceEnd := strings.Index(rest, "}")
		if braceEnd < 0 {
			return Field{}, fmt.Errorf("field %q: unclosed modifier brace", name)
		}
		modifiers = splitModifiers(rest[braceStart+1 : braceEnd])
		rest = rest[:braceStart]
	} else if commaIdx := strings.Index(rest, ","); commaIdx >= 0 {
		modifiers = splitModifiers(rest[commaIdx+1:])
		rest = rest[:commaIdx]
	}

	typeName := strings.ToLower(strings.TrimSpace(rest))

	// Detect arrays via either the "[]base" prefix (legacy; needs quoting in zsh)
	// or an "array" modifier (shell-safe). Both produce a Go slice type.
	isArray := strings.HasPrefix(typeName, "[]")
	if isArray {
		typeName = strings.TrimSpace(typeName[2:])
	}
	var hasArrayMod bool
	modifiers, hasArrayMod = extractArrayModifier(modifiers)
	isArray = isArray || hasArrayMod
	if isArray && !arrayElementTypes[typeName] {
		return Field{}, fmt.Errorf("type %q cannot be used as an array element for field %q — valid element types: %s",
			typeName, name, validArrayElementTypes())
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
			if err := validateSQLIdentifier("fk= target table", strings.TrimPrefix(m, "fk=")); err != nil {
				return Field{}, fmt.Errorf("field %q: %w", name, err)
			}
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

// splitModifiers splits a comma-separated modifier list, trimming blanks.
// Commas inside parentheses do not split, so expressions like
// check=status IN ('a','b') survive as a single modifier.
func splitModifiers(s string) []string {
	var mods []string
	depth, start := 0, 0
	flush := func(end int) {
		if m := strings.TrimSpace(s[start:end]); m != "" {
			mods = append(mods, m)
		}
	}
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return mods
}

// extractArrayModifier removes the "array"/"arr" marker from the modifier list,
// reporting whether it was present. It is a type-shaping flag, not a DB modifier.
func extractArrayModifier(mods []string) ([]string, bool) {
	out := mods[:0]
	found := false
	for _, m := range mods {
		if m == "array" || m == "arr" {
			found = true
			continue
		}
		out = append(out, m)
	}
	return out, found
}

func validateFieldName(name string) error {
	if name == "" {
		return fmt.Errorf("field name cannot be empty")
	}
	normalized := strings.ReplaceAll(strings.ToLower(name), "_", "")
	if autoManagedFields[normalized] {
		return fmt.Errorf("field %q collides with an auto-managed column (id, created_at, updated_at) — remove it", name)
	}
	if goKeywords[name] {
		return fmt.Errorf("field %q is a reserved Go keyword", name)
	}
	if sqlReservedWords[strings.ToLower(name)] {
		return fmt.Errorf("field %q is a reserved SQL keyword and would generate invalid SQL — pick another name (e.g. %s_value)", name, name)
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return fmt.Errorf("field %q must start with a letter or underscore", name)
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return fmt.Errorf("field %q must contain only alphanumeric characters and underscores", name)
		}
	}
	return nil
}

// validateSQLIdentifier checks a user-supplied SQL identifier (fk= target,
// --table-name) that is interpolated unquoted into generated SQL.
func validateSQLIdentifier(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s cannot be empty", kind)
	}
	if sqlReservedWords[strings.ToLower(name)] {
		return fmt.Errorf("%s %q is a reserved SQL keyword", kind, name)
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return fmt.Errorf("%s %q must start with a letter or underscore", kind, name)
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return fmt.Errorf("%s %q must contain only alphanumeric characters and underscores", kind, name)
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

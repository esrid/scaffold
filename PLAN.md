# Plan: `scaffold init` — Multi-DB Boilerplate Generator

## Context

The scaffold tool generates CRUD code for a Go hexagonal-architecture boilerplate. Currently both are SQLite-only. The goal is to add a `scaffold init` command that bootstraps a complete new project with a choice of **SQLite** or **Postgres 18**. All subsequent `scaffold gen` commands adapt SQL, types, placeholders, and templates to the chosen DB stored in `.scaffold/models.json`.

Postgres variant uses **pgx/v5 native** (`pgxpool.Pool`), not the `database/sql` compat layer, to enable `pgx.RowToStructByName` scanning, native JSONB/UUID types, idiomatic PG error handling, and connection pool tuning.

---

## User flow

```bash
scaffold init myapp --module github.com/user/myapp --db postgres
cd myapp
scaffold gen Order customer_id:string! total:float! status:string{default=pending} meta:json{index}
# → BIGINT/BOOLEAN/DOUBLE PRECISION/JSONB/TIMESTAMPTZ types
# → $N placeholders, RETURNING *, queryOne/queryMany helpers
# → GIN index on meta, uuidv7() for id (PG 18 built-in)
```

---

## Implementation steps

### Step 1 — Manifest: add `DB` field
**File:** `internal/parser/manifest.go`

```go
type Manifest struct {
    Module string                   `json:"module"`
    DB     string                   `json:"db"`     // "sqlite" | "postgres", default "sqlite"
    Models map[string]ManifestModel `json:"models"`
}

func (m *Manifest) IsPostgres() bool { return m.DB == "postgres" }
```

Backward compat: empty `DB` → treated as `"sqlite"`.

---

### Step 2 — New CLI command: `scaffold init`
**File:** `cmd/scaffold/init.go` (new)

```
scaffold init [dir] --module <path> --db <sqlite|postgres>
```

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--module` | Yes | — | Go module path e.g. `github.com/user/myapp` |
| `--db` | No | interactive prompt | `sqlite` or `postgres` |
| `[dir]` | No | `./` | Target directory |

**Flow:**
1. Resolve + create target directory
2. Prompt for DB if `--db` not set: `Which database? (sqlite/postgres) [sqlite]:`
3. Call `boilerplate.Generate(dir, module, db)`
4. Write `.scaffold/models.json` with `{db, module, models: {}}`
5. Run `exec.Command("go", "mod", "tidy")` in target dir
6. Print: `✓ Ready. cd myapp && make run`

Wire into `cmd/scaffold/root.go`.

---

### Step 3 — Boilerplate generator
**File:** `internal/generator/boilerplate/generate.go` (new package)

```go
func Generate(dir, module, db string) error
```

Walks two embedded trees: `static/` (same for both DBs) and `sqlite/` or `postgres/`. For each file:
- Ends in `.tmpl` → render with `text/template` using `BoilerplateData{Module, DB}`
- Otherwise → copy verbatim
- **Strip the top-level prefix** when computing the destination path:
  ```go
  // "static/internal/app/app.go.tmpl" → "internal/app/app.go"
  relPath, _ := filepath.Rel(srcPrefix, path) // srcPrefix = "static", "sqlite", or "postgres"
  dest := strings.TrimSuffix(relPath, ".tmpl")
  ```

**Embedded directory tree** (`//go:embed static sqlite postgres`):

```
boilerplate/
├── static/                                         # same for both DBs
│   ├── main.go.tmpl
│   ├── Makefile
│   ├── .env.example.tmpl
│   ├── internal/
│   │   ├── app/config.go.tmpl                      # DATABASE_URL default set by DB type
│   │   ├── app/static.go
│   │   ├── core/domain/identifiable.go
│   │   ├── core/domain/errors.go
│   │   ├── core/ports/crud.go.tmpl
│   │   ├── core/services/crud_service.go.tmpl
│   │   ├── adapters/http/crud_handler.go.tmpl
│   │   ├── adapters/http/middleware.go
│   │   └── adapters/store/schema.sql
│   └── web/                                        # full web/src/ + dist/placeholder.txt
├── sqlite/
│   ├── go.mod.tmpl
│   ├── internal/app/app.go.tmpl                    # db *sql.DB, store.Open()
│   └── internal/adapters/store/
│       ├── store.go                                # sql.Open sqlite_ext, goose sqlite3
│       ├── store_helper.go                         # sql.Tx, IsUniqueViolation via sqlite3 codes
│       └── migrations/00001_initial_schema.sql     # PRAGMA journal_mode=WAL, foreign_keys=ON
└── postgres/
    ├── go.mod.tmpl
    ├── internal/app/app.go.tmpl                    # pool *pgxpool.Pool, pgxpool.NewWithConfig()
    └── internal/adapters/store/
        ├── store.go                                # pgxpool + AfterConnect UUID codec + goose
        ├── store_helper.go                         # pgx native: queryOne, queryMany, DecorateError
        └── migrations/00001_initial_schema.sql     # empty — PG 18 needs no extensions
```

---

#### Postgres boilerplate: `store.go`

```go
//go:embed migrations/*.sql
var embedMigrations embed.FS

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Open creates the pool — does NOT run migrations, call s.Migrate() after.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
    config, err := pgxpool.ParseConfig(dsn)
    if err != nil {
        return nil, fmt.Errorf("store: parse config: %w", err)
    }
    // Register UUID as TextCodec so uuid columns scan into plain Go strings.
    config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
        conn.TypeMap().RegisterType(&pgtype.Type{
            Name:  "uuid",
            OID:   pgtype.UUIDOID,
            Codec: pgtype.TextCodec{},
        })
        return nil
    }
    pool, err := pgxpool.NewWithConfig(ctx, config)
    if err != nil {
        return nil, fmt.Errorf("store: connect: %w", err)
    }
    if err := pool.Ping(ctx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("store: ping: %w", err)
    }
    return pool, nil
}

// Migrate runs goose via a stdlib wrapper — no second connection opened.
func (s *Store) Migrate(ctx context.Context, logger *slog.Logger) error {
    goose.SetBaseFS(embedMigrations)
    goose.SetLogger(slog.NewLogLogger(logger.Handler(), slog.LevelInfo))
    if err := goose.SetDialect("postgres"); err != nil { return err }
    db := stdlib.OpenDBFromPool(s.pool)
    defer func() { _ = db.Close() }()
    if err := goose.UpContext(ctx, db, "migrations"); err != nil {
        return fmt.Errorf("store: migrate: %w", err)
    }
    return nil
}
```

**`app.go` postgres wiring:**
```go
pool, err := store.Open(ctx, cfg.DatabaseURL)
if err != nil { return nil, err }

st := store.NewStore(pool)
if err := st.Migrate(ctx, logger); err != nil {
    pool.Close()
    return nil, err
}
// pool passed to NewRegistry
```

**Imports** (all `jackc/pgx/v5` — single `go get`):
`pgx/v5`, `pgx/v5/pgxpool`, `pgx/v5/pgtype`, `pgx/v5/stdlib`

---

#### Postgres boilerplate: `store_helper.go`

```go
import "github.com/jackc/pgx/v5/pgconn" // explicit — pgconn is a sub-package

func queryOne[T any](ctx context.Context, pool querier, op, query string, notFound error, args ...any) (*T, error)
func queryMany[T any](ctx context.Context, pool querier, op, query string, args ...any) ([]T, error)
func (s *Store) WithinTransaction(ctx context.Context, fn func(pgx.Tx) error) error
func DecorateError(err error, op string) error       // wraps pgconn.PgError with structured fields
func IsUniqueViolation(err error) bool               // 23505
func IsForeignKeyViolation(err error) bool           // 23503
func IsNotNullViolation(err error) bool              // 23502
func IsCheckViolation(err error) bool                // 23514
```

---

#### Postgres `app.go` vs SQLite

| | SQLite | Postgres |
|--|--------|----------|
| Field | `db *sql.DB` | `pool *pgxpool.Pool` |
| Open | `store.Open(dsn)` | `store.Open(ctx, dsn)` + `st.Migrate(ctx, logger)` |
| Health | `db.PingContext(r.Context())` | `pool.Ping(r.Context())` |
| Shutdown | `db.Close()` (returns error) | `pool.Close()` (no error) |
| Registry | `NewRegistry(db, logger)` | `NewRegistry(pool, logger)` |

---

### Step 4 — Postgres type system + `buildTemplateFields` becomes DB-aware
**File:** `internal/generator/context.go`

Type translations happen **in Go at context-build time**, not in templates. This keeps templates using plain `{{.SQLType}}` and `{{.SQLModifiers}}` — no FuncMap needed.

**`buildTemplateFields(fields []parser.Field, db string) []templateField`** — new `db` param:

```go
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
            SQLType:      sqlType,                          // already translated
            NotNull:      f.NotNull,
            IsJSON:       strings.Contains(f.GoType, "RawMessage"),
            IsTime:       strings.Contains(f.GoType, "time.Time"),
            HasIndex:     hasIndexModifier(f.Modifiers),   // new flag
            Mods:         f.Modifiers,
            SQLModifiers: buildSQLModifiers(f.Modifiers, db), // pre-computed string
        }
    }
    return out
}
```

**`SQLModifiers` becomes a pre-computed field** (not a method). Remove the `func (f templateField) SQLModifiers() string` method. Templates use `{{.SQLModifiers}}` either way — no template change needed.

**`pgSQLType` translation table:**

| Go type | Postgres SQL |
|---------|--------------|
| `string` / `*string` | `TEXT` |
| `int` / `*int` | `INTEGER` |
| `int64` / `*int64` | `BIGINT` |
| `float64` / `*float64` | `DOUBLE PRECISION` |
| `bool` / `*bool` | `BOOLEAN` |
| `time.Time` / `*time.Time` | `TIMESTAMPTZ` |
| `json.RawMessage` | `JSONB` |

**`buildSQLModifiers(mods, db)`** — DB-aware modifiers:
- `fk=users` → SQLite: `REFERENCES users(id)` / Postgres: `REFERENCES users(id) ON DELETE RESTRICT`
- `unique` → `UNIQUE` (both)
- `default=val` → `DEFAULT 'val'` (both)

**New fields on `templateField`:**
- `HasIndex bool` — true if `"index"` in modifiers (drives separate `CREATE INDEX` in PG migrations)
- `SQLModifiers string` — pre-computed (replaces method)

**`id` column:**
- SQLite: `id TEXT PRIMARY KEY DEFAULT (uuid7())`
- Postgres: `id UUID PRIMARY KEY DEFAULT uuidv7()` — PG 18 built-in, no extension

**Timestamps:**
- SQLite: `DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`
- Postgres: `TIMESTAMPTZ NOT NULL DEFAULT NOW()`

---

### Step 5 — Postgres migration templates
**File:** `internal/generator/templates.go`

#### 5a. `migrationCreateTmplPostgres`

Uses pre-translated `{{.SQLType}}` and `{{.SQLModifiers}}` — no FuncMap required.

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS {{.TableName}} (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
{{- range .Fields}}
    {{.Name}} {{.SQLType}}{{if .NotNull}} NOT NULL{{end}}{{.SQLModifiers}},
{{- end}}
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
{{range .Fields}}{{if .HasIndex}}
CREATE INDEX{{if .IsJSON}} idx_{{$.TableName}}_{{.Name}} ON {{$.TableName}} USING GIN ({{.Name}});
{{- else}} idx_{{$.TableName}}_{{.Name}} ON {{$.TableName}}({{.Name}});
{{- end}}{{end}}{{end}}
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION trg_set_updated_at()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trg_{{.TableName}}_updated_at
    BEFORE UPDATE ON {{.TableName}}
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS trg_{{.TableName}}_updated_at ON {{.TableName}};
DROP TABLE IF EXISTS {{.TableName}};
```

`-- +goose StatementBegin/End` required for the `$$` function body.
`trg_set_updated_at()` is shared — `CREATE OR REPLACE` is idempotent across tables.

#### 5b. `migrationDropTableTmplPostgres`

Down block fully reverses the Up — recreates table + indexes + trigger:

```sql
-- +goose Up
DROP TRIGGER IF EXISTS trg_{{.TableName}}_updated_at ON {{.TableName}};
DROP TABLE IF EXISTS {{.TableName}};

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.TableName}} (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
{{- range .Fields}}
    {{.Name}} {{.SQLType}}{{if .NotNull}} NOT NULL{{end}}{{.SQLModifiers}},
{{- end}}
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
{{range .Fields}}{{if .HasIndex}}
CREATE INDEX{{if .IsJSON}} idx_{{$.TableName}}_{{.Name}} ON {{$.TableName}} USING GIN ({{.Name}});
{{- else}} idx_{{$.TableName}}_{{.Name}} ON {{$.TableName}}({{.Name}});
{{- end}}{{end}}{{end}}
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION trg_set_updated_at()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trg_{{.TableName}}_updated_at
    BEFORE UPDATE ON {{.TableName}}
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
```

#### 5c. `schemaTmplPostgres`

Same PG syntax as `migrationCreateTmplPostgres` but without goose directives — for `schema.sql` reference file.

---

### Step 6 — CRUD generation: Postgres template variants
**File:** `internal/generator/templates.go`

#### 6a. `domainTmplPostgres`

Adds `db` struct tags (required for `pgx.RowToStructByName`):

```go
type {{.Name}} struct {
    // scaffold:fields:start
{{- range .Fields}}
    {{.GoName}} {{.GoType}} `json:"{{.Name}}" db:"{{.Name}}"`
{{- end}}
    ID        string    `json:"id"         db:"id"`
    CreatedAt time.Time `json:"created_at" db:"created_at"`
    UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
    // scaffold:fields:end
}
```

`id UUID` in PG scans into `string` via the `pgtype.TextCodec` registered in `AfterConnect`.

#### 6b. `storeGenTmplPostgres`

No `scanFunc` — `pgx.RowToStructByName` reads `db` tags automatically:

```go
const (
    sql{{.Name}}Get    = `SELECT * FROM {{.TableName}} WHERE id = $1`
    sql{{.Name}}List   = `SELECT * FROM {{.TableName}} LIMIT $1 OFFSET $2`
    sql{{.Name}}Create = `INSERT INTO {{.TableName}} ({{.InsertCols}}) VALUES ({{.InsertPlaceholders}}) RETURNING *`
    sql{{.Name}}Update = `UPDATE {{.TableName}} SET {{.UpdateSet}}, updated_at = NOW() WHERE id = ${{.UpdateIDIdx}} RETURNING *`
    sql{{.Name}}Delete = `DELETE FROM {{.TableName}} WHERE id = $1`
)
```

Get/List use `queryOne`/`queryMany`. Create/Update use `pool.Query` + `pgx.CollectOneRow(rows, pgx.RowToStructByName[domain.X])`. Delete uses `pool.Exec` + `tag.RowsAffected()`.

#### 6c. `registryTmplPostgres`

`func NewRegistry(pool *pgxpool.Pool, logger *slog.Logger) *Registry`

---

### Step 7 — Context: Postgres `$N` placeholders
**File:** `internal/generator/context.go`

`buildStoreGenCtx(model, modulePath, db string)` — new `db` param. Add `UpdateIDIdx int` to `storeGenCtx`.

| Computed field | SQLite (name, price) | Postgres (name, price) |
|----------------|----------------------|------------------------|
| `InsertPlaceholders` | `?, ?` | `$1, $2` |
| `UpdateSet` | `name = ?, price = ?` | `name = $1, price = $2` |
| `UpdateIDIdx` | — | `3` (len(fields) + 1) |
| `CreateArgs` | `p.Name, json.Marshal(p.Meta)` | `p.Name, p.Meta` (pgx handles JSONB) |
| `ScanArgs` | `&p.ID, &p.Name, ...` | not used |

---

### Step 8 — `buildFieldLines` becomes DB-aware
**File:** `internal/generator/generator.go`

`buildFieldLines(fields []templateField, db string) string` — adds `db` tag when postgres:
```go
// SQLite: Name string `json:"name"`
// Postgres: Name string `json:"name" db:"name"`
// System fields also get db tags when postgres: db:"id", db:"created_at", db:"updated_at"
```

`patchDomainMarkers` passes `g.manifest.DB` to `buildFieldLines`.

---

### Step 9 — Generator: route to Postgres templates
**File:** `internal/generator/generator.go`

```go
func (g *Generator) isPostgres() bool { return g.manifest.IsPostgres() }
```

| Call site | SQLite | Postgres |
|-----------|--------|----------|
| `writeDomain` | `domainTmpl` | `domainTmplPostgres` |
| `writeStoreGen` | `scanFuncTmpl + storeGenTmpl` | `storeGenTmplPostgres` |
| `writeCreateMigration` | `migrationCreateTmpl` | `migrationCreateTmplPostgres` |
| `writeDropMigration` | `migrationDropTableTmpl` | `migrationDropTableTmplPostgres` |
| `addSchemaBlock` / `replaceSchemaBlock` | `schemaTmpl` | `schemaTmplPostgres` |
| `writeRegistry` | `registryTmpl` | `registryTmplPostgres` |
| `buildStoreGenCtx` | `db="sqlite"` | `db="postgres"` |
| `buildTemplateFields` | `db="sqlite"` | `db="postgres"` |

ALTER migrations (`migrationAddColTmpl`, `migrationDropColTmpl`) reuse the same templates — types are already translated in `buildTemplateFields`.

---

## Files changed in scaffold

| File | Action |
|------|--------|
| `internal/parser/manifest.go` | Add `DB string` + `IsPostgres()` |
| `cmd/scaffold/init.go` | New — init command |
| `cmd/scaffold/root.go` | Register init subcommand |
| `internal/generator/boilerplate/` | New package — all embedded templates |
| `internal/generator/boilerplate/generate.go` | New — `Generate(dir, module, db)` with prefix stripping |
| `internal/generator/templates.go` | Add postgres variants: domain, storeGen, schema, 2× migrations, registry |
| `internal/generator/context.go` | `buildTemplateFields(fields, db)`, `buildStoreGenCtx(model, module, db)`, `pgSQLType()`, `buildSQLModifiers()`, `hasIndexModifier()`, `UpdateIDIdx`, `HasIndex`+`SQLModifiers` fields on templateField |
| `internal/generator/generator.go` | Route all calls by DB; `buildFieldLines(fields, db)`; remove `SQLModifiers()` method |
| `go.mod` (scaffold) | No new deps needed |

---

## Verification

```bash
cd /Users/ap/dev/scaffold && go build -o scaffold .

# SQLite init + compile
./scaffold init /tmp/test-sqlite --module github.com/test/app --db sqlite
cd /tmp/test-sqlite && go build ./...

# Postgres init + compile
./scaffold init /tmp/test-pg --module github.com/test/app --db postgres
cd /tmp/test-pg && go build ./...

# SQLite CRUD gen — verify ? placeholders, scanFunc, INTEGER for bool, no db tags
cd /tmp/test-sqlite && scaffold gen Order name:string total:float! active:bool! meta:json{index}
go build ./...

# Postgres CRUD gen — verify:
# $N placeholders, RowToStructByName, db tags on struct
# BOOLEAN/DOUBLE PRECISION/JSONB/TIMESTAMPTZ types
# GIN index for meta, uuidv7() in migration, RETURNING * in INSERT/UPDATE
# no scanFunc in generated store
cd /tmp/test-pg && scaffold gen Order name:string total:float! active:bool! meta:json{index}
go build ./...

# Backward compat (no DB field in manifest → defaults to sqlite)
cd /Users/ap/dev/bb && scaffold gen Widget title:string
go build ./...
```

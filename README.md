# scaffold

A CLI that bootstraps production-ready Go apps with hexagonal architecture. Choose your API mode — **SSR** (templ + HTMX, default), **REST** (JSON API), or **gRPC** — then add, update, and remove models without touching boilerplate.

## Prerequisites

- Go 1.26+
- PostgreSQL (if using `--db postgres`)
- [`buf`](https://buf.build/docs/installation) (if using `--api grpc`, for proto code generation)

## Installation

```bash
go install github.com/esrid/scaffold@latest
```

Or build from source:

```bash
git clone https://github.com/esrid/scaffold
cd scaffold
go build -o scaffold .
```

## Quick start

```bash
# SSR project (default — templ + HTMX + plain CSS)
scaffold init myapp --module github.com/you/myapp --db sqlite
cd myapp && make run

scaffold gen Product name:string! price:float! description:string
# → generates CRUD pages at /products (list, show, create, edit)

# REST JSON API
scaffold init myapp --module github.com/you/myapp --db sqlite --api rest
scaffold gen Product name:string! price:float!
# → generates JSON CRUD at /api/products

# gRPC + REST dual-stack
scaffold init myapp --module github.com/you/myapp --db postgres --api grpc
scaffold gen Order status:string! total:float!
# → generates proto + gRPC handler + REST JSON routes
```

---

## Commands

### `scaffold init`

Bootstrap a complete project from the built-in boilerplate.

```
scaffold init [dir] --module <path> [--db sqlite|postgres] [--api ssr|rest|grpc]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--module` | — | Go module path **(required)** |
| `--db` | prompted | `sqlite` or `postgres` |
| `--api` | `ssr` | API mode: `ssr`, `rest`, or `grpc` |

`dir` defaults to the last segment of `--module`. Runs `go mod tidy` automatically.
Refuses to scaffold into a non-empty directory (a lone `.git/` or other dotfiles are fine).

| Mode | What you get |
|------|-------------|
| `ssr` | templ-rendered pages, HTMX, plain CSS stylesheet, per-model templ components |
| `rest` | JSON CRUD API, generic `CRUDHandler[T]`, TypeScript/esbuild frontend scaffold |
| `grpc` | REST + gRPC dual-stack, health check, per-model `.proto` + handler |

---

### `scaffold gen`

Generate or update the full CRUD scaffold for a model. Routes are mounted automatically in `app.go`.

```
scaffold gen <Model> [field:type{modifier}!...] [--remove <field>...] [--dry-run] [--table-name <name>] [--no-handler] [--soft-delete] [--unique-together <fields>]
```

Running `gen` again on an existing model **merges** the fields you pass into the stored set: a field name that already exists is updated in place, a new name is **added**, and fields you don't mention are **kept**. Passing a subset never drops columns. To drop a field, name it with `--remove`. Adding/removing fields writes a diff migration; your hand-written files are never overwritten.

> [!WARNING]
> Changing the **type or modifiers** of an existing field updates the generated code and `schema.sql` but writes **no migration** (column type changes are DB-specific and lossy). scaffold prints a warning listing the changed fields — write the `ALTER TABLE` yourself in `internal/adapters/store/migrations/`.

| Flag | Description |
|------|-------------|
| `--remove` | Drop field(s) from an existing model (comma-separated or repeated). Writes a `DROP COLUMN` migration |
| `--dry-run` | Preview changes without writing files |
| `--table-name` | Override the auto-pluralized table name (e.g. `people` for `Person`) |
| `--no-handler` | Skip generating HTTP/gRPC handlers and routes (allows pure service/domain models) |
| `--soft-delete` | Enable soft deletion by tracking deletion timestamp in a `deleted_at` field |
| `--unique-together` | Define compound unique constraint(s) (comma-separated fields, e.g. `name,category`, can be repeated) |

#### Field syntax

```
name:type              nullable field (Go pointer, e.g. *string)
name:type!             NOT NULL field

# Modifiers (comma form is shell-safe in zsh/bash):
name:type,mod          field with modifier (e.g. email:string,unique)
name:type,mod,mod!     multiple modifiers, NOT NULL

# Array fields (array/arr modifier is shell-safe):
name:type,array        array field (e.g. tags:string,array)
name:type,array!       NOT NULL array field

# Legacy brace & bracket syntax (requires quotes in zsh/bash):
name:type{mod,mod}!    brace-wrapped modifiers
name:[]type!           prefix-bracket arrays
```

`!` and `nn` are equivalent.

> **Shell-safe vs Legacy Syntax:** A modifier list with a comma inside braces (`{255,unique}`) triggers shell brace expansion in `zsh`, and brackets (`[]`) or operators like `>` can be intercepted by the shell. 
> To avoid quoting, use the comma modifier syntax (`email:string,255,unique!`) and the `,array` suffix (`tags:string,array!`). If you use the legacy `{...}` or `[]` syntax, wrap the entire argument in single quotes: `'email:string{255,unique}!'`, `'tags:[]string!'`.

#### Types

| Alias | Go type | SQLite | Postgres |
|-------|---------|--------|----------|
| `string`, `text` | `string` | `TEXT` | `TEXT` |
| `string{n}` | `string` | `VARCHAR(n)` | `VARCHAR(n)` |
| `int` | `int` | `INTEGER` | `INTEGER` |
| `int64` | `int64` | `INTEGER` | `BIGINT` |
| `float`, `float64` | `float64` | `REAL` | `DOUBLE PRECISION` |
| `bool` | `bool` | `INTEGER` | `BOOLEAN` |
| `time`, `datetime` | `time.Time` | `DATETIME` | `TIMESTAMPTZ` |
| `json` | `json.RawMessage` | `TEXT` | `JSONB` |
| `[]<type>` (or `<type>,array`) | Go slice (e.g. `[]string`) | JSON-encoded `TEXT` | native array (e.g. `TEXT[]`) |

`id`, `created_at`, `updated_at` are auto-managed — do not declare them
(variant spellings like `Id` or `createdAt` are rejected too). Field names that
are SQL reserved words (`order`, `user`, `group`, `to`, …) are rejected with a
clear error, since the generated SQL does not quote identifiers.

**Array fields** can be declared using either the shell-safe `<type>,array` suffix (e.g. `tags:string,array!`, `scores:int,array`) or the legacy `[]<type>` prefix (e.g. `tags:[]string!`, `scores:[]int`).
Valid element types: `string`, `text`, `int`, `int64`, `float`, `float64`, `bool` (not `time` or `json`). Stored as a native array on Postgres and a JSON-encoded `TEXT` column on SQLite.

> [!NOTE]
> **Struct Field Packing:** To optimize memory alignment and minimize padding, `scaffold` automatically sorts all generated struct fields descending by their byte size in `domain/{model}_gen.go`.

#### Modifiers

| Modifier | SQL emitted |
|----------|-------------|
| `nn` | `NOT NULL` — alias for `!` |
| `unique` | `UNIQUE` (separate `CREATE UNIQUE INDEX` in SQLite ALTER migrations, which reject inline UNIQUE) |
| `index` | separate `CREATE INDEX` (GIN index on Postgres for json/array fields) |
| `<n>` | `VARCHAR(n)` (string/text only) |
| `default=val` | `DEFAULT 'val'` — numbers, `true`/`false`, `null` and `CURRENT_*` are emitted unquoted; embedded `'` are escaped |
| `fk=table` | `REFERENCES table(id)` — target validated as a SQL identifier |
| `cascade` | `ON DELETE CASCADE` (used with `fk=`) |
| `setnull` | `ON DELETE SET NULL` (used with `fk=`) |
| `check=expr` | `CHECK (expr)` — commas inside parens are safe: `check=status IN ('a','b')` |

**Migration safety:** adding a NOT NULL field to an existing model emits
`ALTER TABLE … ADD COLUMN … DEFAULT <zero>` (`''`, `0`, `FALSE`,
`CURRENT_TIMESTAMP`, `'[]'`/`'{}'`) so the migration applies cleanly on both
SQLite and Postgres even with existing rows. Indexes for added columns are
created (and dropped before `DROP COLUMN`) in the same migration.

#### Examples

```bash
scaffold gen Product name:string! price:float! sku:string,unique
scaffold gen Article title:string! body:string views:int
scaffold gen Order status:string,default=pending,nn
scaffold gen User username:string,92! email:string,255,unique!
scaffold gen Post user_id:string,fk=users,cascade,index! title:string!
scaffold gen Event payload:json! metadata:json
scaffold gen Post title:string! tags:string,array! scores:int,array  # shell-safe array fields (TEXT[] on postgres, JSON TEXT on sqlite)
scaffold gen Product stock:int                                      # add stock to an existing model (name, price kept)
scaffold gen Product --remove stock                                 # drop the stock column
scaffold gen Product name:string! price:float! --dry-run
scaffold gen Person name:string! --table-name people
```

---

### `scaffold destroy`

Remove all scaffold files for a model and create a `DROP TABLE` migration.

```
scaffold destroy <Model> [--keep-custom] [--force]
```

Prompts for confirmation. By default, it deletes both generated and hand-written files for the model, but automatically creates a backup of any custom hand-written files in `.scaffold/backups/<timestamp>/` before deleting.

| Flag | Description |
|------|-------------|
| `--keep-custom` | Only delete generated files (`*_gen.go` and templates), keeping your hand-written logic (`{model}.go`, `_service.go`, `_store.go`, `_handler.go`, and `.templ` views). |
| `--force` | Force model destruction even if other scaffolded models have active foreign key references (`fk=`) targeting this model's table. |

---

## Generated project structure

### SSR mode (default)

```
myapp/
├── main.go
├── Makefile                           # make run / make build
├── .env.example
├── .scaffold/models.json              # manifest
├── internal/
│   ├── app/
│   │   ├── app.go                     # loads templates, mounts routes
│   │   ├── config.go
│   │   └── registry.go                # auto-regenerated
│   ├── core/
│   │   ├── domain/{model}.go          # struct + Validate()
│   │   ├── ports/{model}.go           # interfaces
│   │   └── services/
│   │       ├── {model}_service_gen.go # CRUD delegation (regenerated)
│   │       └── {model}_service.go     # your logic (never touched)
│   └── adapters/
│       ├── http/
│       │   ├── {model}_handler_gen.go # SSR handler + bindForm (regenerated)
│       │   ├── {model}_handler.go     # your extensions (never touched)
│       │   └── middleware.go
│       └── store/
│           ├── {model}_store_gen.go
│           └── {model}_store.go
└── web/
    ├── static/
    │   └── app.css                        # plain CSS stylesheet (served at /static/)
    ├── static.go                          # //go:embed for static assets
    └── views/
        ├── layout.templ                   # shared page layout component
        ├── home.templ
        ├── helpers.go                     # display/truthy template helpers
        └── {model}.templ                  # List/Form/Show components (write-once — yours)
```

> templ compiles `.templ` files to Go. Run `make generate` (or `templ generate`)
> after `scaffold gen` and before building.

### REST mode

Same as SSR, except:
- `internal/adapters/http/crud_handler.go` — generic `CRUDHandler[T]` (no per-model handler)
- `web/src/` + `web/dist/` — TypeScript/esbuild frontend scaffold instead of templates
- Routes: `GET /api/{plural}`, `POST /api/{plural}`, `PUT /api/{plural}/{id}`, etc.
- `GET /api/{plural}` returns newest-first and accepts `?limit=` (default 20, max 100) and `?offset=`

### gRPC mode (REST + gRPC)

Adds on top of REST:
- `internal/adapters/grpc/pb/{model}.proto`
- `internal/adapters/grpc/{model}_handler_gen.go`
- `internal/adapters/grpc/shared.go` — error translation (written once)
- `buf.yaml` + `buf.gen.yaml` — proto code generation config

Run `make proto` after `scaffold gen` to compile `.proto` → Go pb package.

---

## What's regenerated vs. what's yours

| File | Behaviour |
|------|-----------|
| `domain/{model}_gen.go` | Always regenerated — struct (sorted descending by byte size for struct packing) + `GetID`/`WithID` |
| `domain/{model}.go` | Yours — `Validate()` and custom methods, written once |
| `ports/{model}.go` | Written once, never touched |
| `services/{model}_service_gen.go` | Always regenerated |
| `services/{model}_service.go` | Yours — never overwritten |
| `store/{model}_store_gen.go` | Always regenerated |
| `store/{model}_store.go` | Yours — never overwritten |
| `app/registry.go` | Always regenerated (models sorted — output is deterministic) |
| `app/routes_gen.go` | Always regenerated — `app.go` itself is yours |
| `app/custom.go` | Yours — wire custom services with access to `cfg` + registry |
| `http/{model}_handler_gen.go` | SSR only — always regenerated |
| `http/{model}_handler.go` | SSR only — yours |
| `web/views/{model}.templ` | SSR only — written once, yours (`--regen-views` to refresh) |
| `adapters/grpc/{model}_handler_gen.go` | gRPC only — always regenerated |
| `adapters/grpc/shared.go` | gRPC only — written once |
| `internal/adapters/grpc/pb/{model}.proto` | gRPC only — always regenerated |

---

## Makefile targets

**SSR and gRPC modes:**
```bash
make run     # go run main.go
make build   # go build -o bin/server
make proto   # buf generate --path internal/adapters/grpc/pb  (gRPC only)
```

**REST mode:**
```bash
make run     # build TypeScript + go run
make build   # build TypeScript + go build
make build-fe  # esbuild TypeScript + CSS only
make proto   # buf generate --path internal/adapters/grpc/pb  (gRPC only)
```

---

## Adding custom logic

**`internal/core/domain/{model}.go`** — field validation:

```go
func (p Product) Validate() error {
    errs := domain.ValidationError{Entity: "Product", Errors: map[string]string{}}
    if p.Price <= 0 {
        errs.Errors["price"] = "must be greater than zero"
    }
    if len(errs.Errors) > 0 {
        return &errs
    }
    return nil
}
```

**`internal/adapters/store/{model}_store.go`** — custom SQL queries:

For both Postgres (`pgx`) and SQLite (`database/sql` via adapted helpers), you can use struct-mapping helpers like `CollectRows` and `RowToStructByName` to map query results without writing manual scanner code:

```go
func (s *ProductStore) FindBySKU(ctx context.Context, sku string) (*domain.Product, error) {
	// Example using SQLite adapted helpers:
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, price, active, created_at, updated_at FROM products WHERE sku = ?", sku)
	if err != nil {
		return nil, err
	}
	return store.CollectOneRow(rows, store.RowToAddrOfStructByName[domain.Product])
}
```

**`internal/core/services/{model}_service.go`** — business logic:

```go
func (s *ProductService) Publish(ctx context.Context, id string) error {
    // your logic here
}
```

**SSR: `internal/adapters/http/{model}_handler.go`** — extra routes:

```go
func (h *ProductHandler) RegisterExtraRoutes(r chi.Router) {
    r.Get("/featured", h.Featured)
}
```

---

## Running tests

```bash
# Fast unit tests (no network, no Docker)
go test ./... -short

# Compilation tests (downloads deps, ~10s)
go test ./internal/generator/boilerplate/...

# Full integration tests with Postgres container (requires Docker)
go test ./internal/generator/boilerplate/... -tags integration
```

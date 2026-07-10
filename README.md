# scaffold

A CLI that bootstraps production-ready Go apps with hexagonal architecture. Choose your API mode — **SSR** (server-rendered HTML, default), **REST** (JSON API), or **gRPC** — then add, update, and remove models without touching hand-written code.

## Prerequisites

- Go 1.26+ to build/install the `scaffold` CLI itself
- **Go 1.27 for every generated project** — IDs are UUIDv7, generated via the stdlib `uuid` package introduced in Go 1.27 (currently a release candidate; generated `go.mod` files pin `toolchain go1.27rc1`)
- PostgreSQL (if using `--db postgres`)
- [`templ`](https://templ.guide) (if using `--ssr-engine templ`, the default for `--api ssr`), for compiling `.templ` views
- [`buf`](https://buf.build/docs/installation) (if using `--api grpc`), for proto code generation

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
# SSR project (default — templ + HTMX, plain CSS)
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
scaffold init [dir] --module <path> [--db sqlite|postgres] [--api ssr|rest|grpc] [--ssr-engine templ|html]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--module` | — | Go module path **(required)** |
| `--db` | prompted | `sqlite` or `postgres` |
| `--api` | `ssr` | API mode: `ssr`, `rest`, or `grpc` |
| `--ssr-engine` | `templ` | SSR view engine: `templ` (compiled components) or `html` (`html/template`, no compile step). Ignored outside `--api ssr` |
| `--grpc` | `false` | Deprecated alias for `--api grpc` |

`dir` defaults to the last segment of `--module`. Runs `go mod tidy` automatically.
Refuses to scaffold into a non-empty directory (a lone `.git/` or other dotfiles are fine).

| Mode | What you get |
|------|-------------|
| `ssr` + `templ` | templ-rendered pages (compiled `.templ` components), plain CSS, HTMX wired into generated views (e.g. delete uses `hx-delete`/`hx-swap`), loaded from CDN in the layout |
| `ssr` + `html` | `html/template`-rendered pages, no compile step, pure SSR by default — `htmx.min.js` is vendored and loaded in the layout, but generated views use plain `<form>` submits; add `hx-*` attributes yourself where you want AJAX behavior |
| `rest` | JSON CRUD API, generic `CRUDHandler[T]`, TypeScript/esbuild frontend scaffold |
| `grpc` | REST + gRPC dual-stack, health check, per-model `.proto` + handler |

All modes route on the stdlib `net/http.ServeMux` (Go 1.22+ method+pattern syntax, e.g. `GET /products/{id}`) — there is no router dependency (chi was removed).

---

### `scaffold gen`

Generate or update the full CRUD scaffold for a model. Routes are mounted automatically.

```
scaffold gen <Model> [field:type{modifier}!...] [flags]
```

Running `gen` again on an existing model **merges** the fields you pass into the stored set: a field name that already exists is updated in place, a new name is **added**, and fields you don't mention are **kept**. Passing a subset never drops columns. To drop a field, name it with `--remove`. Adding/removing fields writes a diff migration; your hand-written files are never overwritten.

> [!WARNING]
> Changing the **type or modifiers** of an existing field updates the generated code and `schema.sql` but writes **no migration** (column type changes are DB-specific and lossy — the same reason Rails/Ecto never auto-generate them either). Instead of a warning that scrolls by unnoticed, `scaffold gen` **fails the command** (exit 1) listing the changed fields, so CI/pre-commit catches a code/database mismatch before it ships. Write the `ALTER TABLE` yourself in `internal/adapters/store/migrations/`, then re-run with `--force` to acknowledge and let the command succeed.

Every generated migration file also opens with an `-- Action: …` comment stating what happened (created table, added/dropped column(s), soft delete toggled, unique-together constraint added/dropped) — so the file is self-describing without needing to diff it against the manifest.

| Flag | Description |
|------|-------------|
| `--remove` | Drop field(s) from an existing model (comma-separated or repeated). Writes a `DROP COLUMN` migration |
| `--dry-run` | Preview changes without writing files |
| `--diff` | Show a unified diff of pending changes (implies `--dry-run`, writes nothing) |
| `--table-name` | Override the auto-pluralized table name (e.g. `people` for `Person`) |
| `--no-handler` | Skip generating HTTP/gRPC handlers and routes (allows pure service/domain models). Sticky across regeneration |
| `--soft-delete` | Enable soft deletion by tracking deletion timestamp in a `deleted_at` field |
| `--unique-together` | Define compound unique constraint(s) (comma-separated fields, e.g. `name,category`, can be repeated) |
| `--skip <ops>` | CRUD ops to **not** generate — comma list of `list,read,create,update,delete`. Mutually exclusive with `--only` |
| `--only <ops>` | Generate **only** these CRUD ops |
| `--middleware op:Func1,Func2` | Wrap an op's route (`op` ∈ `list,read,create,update,delete,all`) with named middleware functions. Repeatable. Sticky across regeneration — stored in the manifest and merged, not replaced, on the next `gen` |
| `--remove-middleware <op>` | Clear middleware from named op(s), or `all` |
| `--regen-views` | Overwrite SSR views (default: write-once — `.templ`/`.html` views are never clobbered once created) |
| `--force` | Acknowledge an in-place field change (type/nullability) with no matching migration, and let the command exit 0 anyway — you're still on the hook for writing the `ALTER TABLE` yourself |

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
| `uuid` | `string` | `TEXT` | `UUID` |
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

> [!NOTE]
> **ID generation:** `id` is a UUIDv7 string, generated in Go (`uuid.NewV7()`, stdlib as of Go 1.27) inside the generated `Store.Create`, not by the database. UUIDv7 is time-ordered, so `ORDER BY id DESC` in generated `List` queries is also newest-first without a separate index.

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
scaffold gen Step title:string! --skip create,delete                # no create/delete affordances
scaffold gen Report title:string! --only list,read                  # read-only resource
scaffold gen Post title:string! --middleware update:RequireAuth --middleware delete:RequireAuth,RequireAdmin
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
| `--keep-custom` | Only delete generated files (`*_gen.go` and templates), keeping your hand-written logic (`{model}.go`, `_service.go`, `_store.go`, `_handler.go`, and view files). |
| `--force` | Force model destruction even if other scaffolded models have active foreign key references (`fk=`) targeting this model's table. |

---

## Generated project structure

### SSR mode (default)

```
myapp/
├── main.go
├── Makefile                           # make run / make build (+ make generate if --ssr-engine templ)
├── .env.example
├── .scaffold/models.json              # manifest
├── internal/
│   ├── app/
│   │   ├── app.go                     # server startup, dependency wiring, route mounting,
│   │   │                               #   ALL IN ONE FILE — see "app.go markers" below
│   │   └── config.go
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
    ├── static/app.css                 # plain CSS stylesheet (served at /static/)
    ├── static.go                      # //go:embed for static assets
    # --ssr-engine templ (default):
    └── views/
        ├── layout.templ               # shared page layout component
        ├── home.templ
        ├── helpers.go                 # display/truthy template helpers
        └── {model}.templ              # List/Form/Show components (write-once — yours)
    # --ssr-engine html:
    └── templates/
        ├── layout/base.html           # shared page layout
        ├── home.html
        └── {model}.html               # write-once — yours
```

> `--ssr-engine templ` compiles `.templ` files to Go. `make run`/`make build` run `templ generate` for you; `--ssr-engine html` needs no compile step.

#### `app.go` markers (SSR mode)

SSR mode has **no separate `registry.go` or `routes_gen.go`** — dependency wiring and route registration live inline in `internal/app/app.go`, inside marker comments (`// scaffold:type-defs:start/end`, `// scaffold:stores-wire:start/end`, `// scaffold:service-wire:insert`, `// scaffold:handler-wire:insert`, `// scaffold:routes:start/end`). `scaffold gen`/`destroy` only ever rewrite the content between markers for the model(s) involved — everything else in `app.go`, including code you add above or between marker blocks, is yours and is never touched.

### REST mode

Same as SSR, except:
- `internal/app/registry.go` and `internal/app/routes_gen.go` are separate files, always fully regenerated (`// Code generated by scaffold. DO NOT EDIT.`) — no markers, no hand edits
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
| `app/app.go` — marker spans (SSR only) | Regenerated per-model, scoped to marker blocks — see above |
| `app/app.go` — everything outside marker spans (SSR only) | Yours — never touched |
| `app/registry.go`, `app/routes_gen.go` (REST/gRPC only) | Always regenerated in full |
| `app/custom.go` | Yours — wire custom services with access to `cfg` + registry |
| `http/{model}_handler_gen.go` | SSR only — always regenerated |
| `http/{model}_handler.go` | SSR only — yours; `registerCustomRoutes(mux *http.ServeMux)` lives here and is always called from the generated `Router()` |
| `web/views/{model}.templ` or `web/templates/{model}.html` | SSR only — written once, yours (`--regen-views` to refresh) |
| `adapters/grpc/{model}_handler_gen.go` | gRPC only — always regenerated |
| `adapters/grpc/shared.go` | gRPC only — written once |
| `internal/adapters/grpc/pb/{model}.proto` | gRPC only — always regenerated |

---

## Makefile targets

**SSR mode:**
```bash
make run       # go run main.go (+ templ generate first, if --ssr-engine templ)
make build     # go build -o bin/server (+ templ generate first, if --ssr-engine templ)
make generate  # templ generate — only present when --ssr-engine templ
make proto     # buf generate --path internal/adapters/grpc/pb  (gRPC only)
```

**REST mode:**
```bash
make run       # build TypeScript + go run
make build     # build TypeScript + go build
make build-fe  # esbuild TypeScript + CSS only
make proto     # buf generate --path internal/adapters/grpc/pb  (gRPC only)
```

---

## Routing

Every mode routes on the stdlib `net/http.ServeMux` — no router dependency. Routes use Go 1.22+ method+pattern syntax (`mux.HandleFunc("GET /products/{id}", ...)`). There's no global `r.Use()`; middleware is plain function composition (`wrapMiddlewares` in `app.go`), and per-op middleware set via `scaffold gen --middleware` wraps individual generated routes.

A `mountAt(mux, prefix, handler)` helper mounts a sub-handler at both `/prefix` and `/prefix/...` without an HTTP redirect (`http.StripPrefix` alone only covers the trailing-slash subtree).

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
// registerCustomRoutes is always called by the generated Router() — add
// hand-written routes here, they survive regeneration.
func (h *ProductHandler) registerCustomRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /featured", h.Featured)
}

func (h *ProductHandler) Featured(w http.ResponseWriter, r *http.Request) {
    // ...
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

# scaffold

A CLI that bootstraps production-ready Go REST APIs with a hexagonal architecture, then lets you add, modify, and remove models without touching boilerplate.

## Prerequisites

- Go 1.23+
- PostgreSQL (if using `--db postgres`)

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
# 1. Bootstrap a new project
scaffold init myapp --module github.com/yourname/myapp --db sqlite

# 2. Enter the project and start the server
cd myapp
make run

# 3. Generate your first model
scaffold gen Product name:string! price:float! sku:string{unique}
```

The server is running on `:8080`. You now have a full CRUD REST API for `Product` with zero boilerplate to write.

## Commands

### `scaffold init`

Bootstrap a complete project from the built-in boilerplate.

```
scaffold init [dir] --module <module-path> [--db sqlite|postgres]
```

| Flag | Description |
|------|-------------|
| `--module` | Go module path, e.g. `github.com/you/myapp` **(required)** |
| `--db` | Database driver: `sqlite` or `postgres` (prompted if omitted) |

If `dir` is omitted, it defaults to the last segment of `--module`.

```bash
# Postgres project in ./myapp
scaffold init myapp --module github.com/you/myapp --db postgres

# SQLite project, dir inferred from module
scaffold init --module github.com/you/myapp --db sqlite
# → creates ./myapp/
```

`init` runs `go mod tidy` automatically. When it finishes:

```
cd myapp
make run   # dev server on :8080
```

---

### `scaffold gen`

Generate or update the full CRUD scaffold for a model.

```
scaffold gen <Model> [field:type{modifier}!...] [--dry-run] [--table-name <name>]
```

Running `gen` again on an existing model **adds or removes fields** and writes a diff migration — your hand-written service and store code is never overwritten.

| Flag | Description |
|------|-------------|
| `--dry-run` | Preview what would be written without touching the filesystem |
| `--table-name` | Override the auto-pluralized table name (e.g. `people` for `Person`) |

#### Field syntax

```
name:type           nullable field (Go pointer, e.g. *string)
name:type!          NOT NULL field
name:type{unique}   UNIQUE constraint
name:type{index}    database index
name:type{unique,index}  both
```

#### Types

| Alias | Go type | SQL type |
|-------|---------|----------|
| `string`, `text` | `string` | `TEXT` |
| `int`, `int64` | `int` / `int64` | `INTEGER` |
| `float`, `float64` | `float64` | `REAL` / `DOUBLE PRECISION` |
| `bool` | `bool` | `BOOLEAN` / `INTEGER` |
| `time`, `datetime` | `time.Time` | `DATETIME` / `TIMESTAMPTZ` |
| `json` | `json.RawMessage` | `TEXT` / `JSONB` |

`id`, `created_at`, and `updated_at` are auto-managed — do not declare them.

#### Examples

```bash
# Basic model
scaffold gen Product name:string! price:float! sku:string{unique}

# Nullable field (no !) → Go pointer *int
scaffold gen Article title:string! body:string views:int

# JSON field
scaffold gen Event payload:json! metadata:json

# Add a field to an existing model (generates ALTER TABLE migration)
scaffold gen Product name:string! price:float! sku:string{unique} stock:int

# Preview without writing
scaffold gen Product name:string! price:float! --dry-run

# Custom table name
scaffold gen Person name:string! --table-name people
```

---

### `scaffold destroy`

Remove all scaffold files for a model and create a `DROP TABLE` migration.

```
scaffold destroy <Model>
```

Prompts for confirmation before deleting anything. Hand-written files (`{model}_service.go`, `{model}_store.go`) are deleted along with generated ones — back them up first if they contain custom logic.

```bash
scaffold destroy Product
```

---

## Generated project structure

```
myapp/
├── main.go
├── Makefile
├── .env.example
├── .scaffold/
│   └── models.json              # manifest tracking all models
└── internal/
    ├── app/
    │   ├── app.go               # wires everything together
    │   ├── config.go            # env-based config
    │   └── registry.go          # auto-regenerated on every gen/destroy
    ├── core/
    │   ├── domain/
    │   │   ├── errors.go        # NotFoundError, ValidationError, etc.
    │   │   └── {model}.go       # struct + Validate() + marker blocks
    │   ├── ports/
    │   │   └── {model}.go       # repository interface (written once)
    │   └── services/
    │       ├── {model}_service_gen.go   # CRUD delegation (regenerated)
    │       └── {model}_service.go       # your custom logic (never touched)
    └── adapters/
        ├── http/
        │   ├── crud_handler.go  # generic Chi CRUD handler
        │   └── middleware.go
        └── store/
            ├── schema.sql                     # full schema
            ├── migrations/                    # numbered SQL migrations
            ├── {model}_store_gen.go           # generated queries (regenerated)
            └── {model}_store.go               # your custom queries (never touched)
```

### What gets regenerated vs. what's yours

| File | Behaviour |
|------|-----------|
| `domain/{model}.go` | Struct fields patched via markers; `Validate()` body is yours |
| `ports/{model}.go` | Written once, never touched again |
| `services/{model}_service_gen.go` | Always regenerated |
| `services/{model}_service.go` | Yours — never overwritten |
| `store/{model}_store_gen.go` | Always regenerated |
| `store/{model}_store.go` | Yours — never overwritten |
| `app/registry.go` | Always regenerated |

---

## REST API

Every model gets five routes registered under `/api/{plural-model}`:

| Method | Path | Action |
|--------|------|--------|
| `GET` | `/api/products` | List (limit 10) |
| `GET` | `/api/products/{id}` | Get by ID |
| `POST` | `/api/products` | Create |
| `PUT` | `/api/products/{id}` | Update |
| `DELETE` | `/api/products/{id}` | Delete |

The handler rejects unknown JSON fields and caps request bodies at 1 MB.

---

## Makefile targets

```bash
make run       # build frontend + run server (go run)
make build     # build frontend + compile binary to bin/server
make build-fe  # esbuild TypeScript + CSS only
make clean     # remove web/dist and bin/
```

---

## Adding custom logic

After `scaffold gen`, open the two stub files that are yours to own:

**`internal/core/domain/{model}.go`** — add field validation:
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

**`internal/adapters/store/{model}_store.go`** — add custom queries below the generated section:
```go
func (s *ProductStore) FindBySKU(ctx context.Context, sku string) (*domain.Product, error) {
    // your query here
}
```

**`internal/core/services/{model}_service.go`** — add business logic:
```go
func (s *ProductService) Publish(ctx context.Context, id string) error {
    // your logic here
}
```

# Best-practices pass on the scaffold generator

Goal: make the **generated** boilerplate follow Go best practices only.
We audit what `scaffold` emits — the templates in `internal/generator/templates.go`
and the static boilerplate under `internal/generator/boilerplate/`.

## How to resume

1. Load the Go skills first: invoke `golang-how-to` (orchestrator). For this work it
   pulls in: `golang-error-handling`, `golang-security`, `golang-safety`,
   `golang-database`, `golang-grpc`, `golang-observability`/`golang-context`,
   `golang-code-style`, `golang-naming`.
2. Tests have **no golden files**. Validation = the `TestCompile_*` tests, which
   actually compile generated apps, plus substring asserts in `scaffold_test.go`.
3. Baseline before edits was **green**:
   ```
   go build ./...
   go test ./internal/generator/...        # ~22s (boilerplate compile tests)
   ```
   Re-run this after every change. `internal/formatter` only runs `go/format`
   (gofmt) — it sorts imports within a group but does **not** add missing imports,
   so add imports explicitly in templates.

## DONE (applied + `go test ./internal/generator/...` GREEN)

- [x] **Silent JSON unmarshal error** — `templates.go` `scanFuncTmpl` (array fields).
      `_ = json.Unmarshal(...)` → now returns the error. Skill: golang-error-handling,
      golang-safety. (replace_all hit both `scanX` and `scanXRows`.)
- [x] **gRPC internal error leak** — `templates.go` `grpcSharedTmpl` `translateError`.
      `status.Errorf(codes.Internal, "internal error: %v", err)` →
      `status.Error(codes.Internal, "internal error")` (no detail to client; a logging
      interceptor should record the full error). Skill: golang-grpc, golang-security.
- [x] **REST internal error leak** — `boilerplate/rest/.../http/crud_handler.go.tmpl`
      `respondError` default branch echoed `err.Error()` → now `slog.Error(...)` +
      opaque `{"error":"internal_server_error"}`. Skill: golang-security.
- [x] **SSR maps every error to 500** — `templates.go` `ssrHandlerTmpl` `serverError`.
      Added `errors` import; now maps `*domain.NotFoundError`→404,
      `*domain.ValidationError`→422, else 500. Skill: golang-error-handling.
- [x] **Generic CRUD service used `Debug` not `DebugContext`** —
      `boilerplate/static/.../services/crud_service.go.tmpl`. All 5 calls →
      `DebugContext(ctx, ...)`, matching the per-model `serviceGenTmpl`. Skill:
      golang-context, golang-observability.
- [x] **Server crash didn't trigger shutdown** — all 3 `app.go.tmpl`
      (sqlite/postgres/ssr). Added a buffered `serverErr := make(chan error, 2)`;
      HTTP & gRPC `Serve` goroutines now send `fmt.Errorf("... failed: %w", err)`
      instead of only logging; `Run` does `select { ctx.Done() | serverErr }`, then
      `shutdown`, and returns the crash error (so `main` exits non-zero). Dependency-
      free (no errgroup). Skill: golang-concurrency, golang-design-patterns.
- [x] **Generic `services.CRUDService` struct is DEAD CODE** — Removed the dead template file
      `internal/generator/boilerplate/static/internal/core/services/crud_service.go.tmpl`.
      Eliminated dead code and avoided the footgun of a generic service bypassing validation.
- [x] **Postgres `SELECT *` / `RETURNING *`** — Changed `templates.go` `storeGenTmplPostgres`
      to use explicit column lists (`{{.SelectCols}}`) for both SQLite and Postgres.
- [x] **CORS `Access-Control-Allow-Origin: *`** — Modified the CORS middleware in
      `boilerplate/static/.../middleware.go.tmpl` to take `AllowedOrigins` from the app config.
      Added the config option in config templates, and updated the middleware calls to pass it.
- [x] **SSR `bindForm` for `*string`** — Updated `templates.go` `bindForm` generation logic
      to check `r.Form` presence for pointer fields (`*string`, `*int`, etc.). If a field is present
      but has an empty value, it is correctly set to `nil` (resetting it to NULL in the database).
- [x] **Struct field packing** — Sort generated struct fields descending by byte size (with alphabetical tie-breakers) to optimize memory alignment / struct packing and minimize padding. This applies to both user-defined and system fields.
- [x] **SQLite struct mapping helpers** — Port pgx-style reflection-based query mapping helpers (CollectRows, RowToStructByName, RowToStructByPos, RowToMap) to SQLite database/sql boilerplate so that custom queries can be mapped easily.

## TODO (not started — evaluate, these are more involved / behavioral)

- None! All tasks completed.

## Explicitly DECIDED — do NOT change

- None.

## Notes
- Generated code uses `errors.AsType[...]` (Go 1.26) in crud_handler — left as-is.
- `internal/generator/context.go` has 2 pre-existing linter warnings (writestring,
  unusedparams) unrelated to this work.


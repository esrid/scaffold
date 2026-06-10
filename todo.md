# Scaffold Improvements Implementation Progress

Track the implementation of proposed features and database safety enhancements.

---

## 📋 Roadmap & Status

### Phase 1: Developer Experience & Safeguards (DX)
- [x] **Destruction Backup System**
  - [x] Add `--keep-custom` flag to `destroyCmd` in [cmd/scaffold/destroy.go](file:///Users/ap/dev/scaffold/cmd/scaffold/destroy.go)
  - [x] Implement automatic backups to `.scaffold/backups/<timestamp>/` in [internal/generator/generator.go](file:///Users/ap/dev/scaffold/internal/generator/generator.go)
  - [x] Add unit tests verifying backup creation and custom file preservation
- [x] **SQLite WAL Mode & Concurrency Tuning**
  - [x] Tune SQLite connections in [store.go.tmpl](file:///Users/ap/dev/scaffold/internal/generator/boilerplate/sqlite/internal/adapters/store/store.go.tmpl) to run `PRAGMA journal_mode=WAL;` and `PRAGMA busy_timeout=5000;` on connection startup
  - [x] Increase connection limit from `MaxOpenConns(1)` to `MaxOpenConns(10)`

### Phase 2: Database Integrity & Constraints
- [x] **Cross-Reference FK Checking on Destroy**
  - [x] Add check to inspect manifest for foreign keys (`fk=`) referencing the model being destroyed in [internal/generator/generator.go](file:///Users/ap/dev/scaffold/internal/generator/generator.go)
  - [x] Add a `--force` flag to bypass the warning and force destruction
  - [x] Add unit tests for FK dependency detection
- [x] **Foreign Key Validation on Generation**
  - [x] Add warning/validation check for missing target tables in [internal/parser/model.go](file:///Users/ap/dev/scaffold/internal/parser/model.go)
- [x] **SQLite Foreign Key Enforcement**
  - [x] Programmatically execute `PRAGMA foreign_keys = ON;` in SQLite connection setups

### Phase 3: Code Formatting Compliance
- [x] **Import Formatting & Sorting**
  - [x] Modify `formatter.GoSource` in [internal/formatter/format.go](file:///Users/ap/dev/scaffold/internal/formatter/format.go) to use `golang.org/x/tools/imports`
  - [x] Resolve any linters issues with import groupings
  - [x] Add test cases to check generated imports

### Phase 4: Advanced Features
- [x] **Multi-Column Constraints**
  - [x] Implement CLI support and migrations syntax for compound indexes and unique keys (e.g., `--unique-together`)
- [x] **Soft Deletes**
  - [x] Add `--soft-delete` flag to `genCmd`
  - [x] Generate nullable `deleted_at` field and update SQL query templates to filter out deleted rows
  - [x] Add comprehensive integration tests for soft delete behavior

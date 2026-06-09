package generator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esrid/scaffold/internal/parser"
)

// genModelNoHandler is genModel with the --no-handler flag set.
func genModelNoHandler(t *testing.T, manifest *parser.Manifest, name string, fields ...string) *parser.Model {
	t.Helper()
	fs, err := parser.ParseFields(fields)
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	model, err := parser.BuildModel(name, fs, nil, manifest, "", true)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	return model
}

// findMigration returns the content of the first migration whose name contains
// substr, failing the test if none matches.
func findMigration(t *testing.T, root, substr string) string {
	t.Helper()
	dir := filepath.Join(root, "internal", "adapters", "store", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), substr) {
			return readFile(t, root, filepath.Join("internal", "adapters", "store", "migrations", e.Name()))
		}
	}
	t.Fatalf("no migration matching %q in %s", substr, dir)
	return ""
}

// ---- Registry: no unused imports with 0 handlers (BOILERPLATE_REVIEW 1.3 / 3.3) ----

func TestScaffold_AllNoHandler_RegistryHasNoUnusedImports(t *testing.T) {
	for _, mode := range []string{"rest", "ssr"} {
		t.Run(mode, func(t *testing.T) {
			root, manifest := projectSetup(t, "sqlite", mode)
			model := genModelNoHandler(t, manifest, "Job", "payload:string!")
			runScaffold(t, root, manifest, model)

			registry := readFile(t, root, "internal/app/registry.go")
			assertGoSyntax(t, registry, "registry.go")
			assertNotContains(t, registry, "httpadapter", "registry.go with only --no-handler models")
			assertNotContains(t, registry, "/internal/core/domain\"", "registry.go with only --no-handler models")
			assertContains(t, registry, "/internal/adapters/store\"", "registry.go store import")
		})
	}
}

func TestDestroy_LastHandlerModel_RegistryDropsHTTPImport(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")

	// One handler model + one no-handler model.
	withHandler := genModel(t, manifest, "Product", "name:string!")
	runScaffold(t, root, manifest, withHandler)
	noHandler := genModelNoHandler(t, manifest, "Job", "payload:string!")
	runScaffold(t, root, manifest, noHandler)

	// Destroy the only handler model — registry must compile without httpadapter.
	entry := manifest.Models["Product"]
	model, err := parser.ModelFromEntry("Product", entry, manifest)
	if err != nil {
		t.Fatalf("ModelFromEntry: %v", err)
	}
	runDestroy(t, root, manifest, model)

	registry := readFile(t, root, "internal/app/registry.go")
	assertGoSyntax(t, registry, "registry.go")
	assertNotContains(t, registry, "httpadapter", "registry.go after destroying last handler model")
}

// ---- Destroy: migration numbering must be disk-aware (BOILERPLATE_REVIEW 1.2) ----

func TestDestroy_MigrationVersionAvoidsDiskCollision(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Product", "name:string!")
	runScaffold(t, root, manifest, model)

	// Simulate unrelated migrations created after this model (hand-written or
	// from another model) that the model's own counter knows nothing about.
	migDir := filepath.Join(root, "internal", "adapters", "store", "migrations")
	if err := os.WriteFile(filepath.Join(migDir, "00007_create_users.sql"), []byte("-- x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reload so the manifest picks up the on-disk floor, as the CLI does.
	if err := parser.SaveManifest(root, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	reloaded, err := parser.LoadManifest(root)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	dropModel, err := parser.ModelFromEntry("Product", reloaded.Models["Product"], reloaded)
	if err != nil {
		t.Fatalf("ModelFromEntry: %v", err)
	}
	runDestroy(t, root, reloaded, dropModel)

	assertExists(t, root, "internal/adapters/store/migrations/00008_drop_products.sql")
	assertNotExists(t, root, "internal/adapters/store/migrations/00007_drop_products.sql")
}

// ---- Registry determinism / idempotence ----

func TestScaffold_Registry_DeterministicOrder(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	runScaffold(t, root, manifest, genModel(t, manifest, "Zebra", "name:string!"))
	runScaffold(t, root, manifest, genModel(t, manifest, "Apple", "name:string!"))

	registry := readFile(t, root, "internal/app/registry.go")
	if strings.Index(registry, "Apple") > strings.Index(registry, "Zebra") {
		t.Errorf("registry.go models are not sorted alphabetically")
	}

	// Re-running gen with no changes must produce byte-identical output.
	model, err := parser.BuildModel("Apple", nil, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	for range 5 {
		runScaffold(t, root, manifest, model)
		if got := readFile(t, root, "internal/app/registry.go"); got != registry {
			t.Fatal("registry.go changed across identical re-gens (non-deterministic order)")
		}
	}
}

// ---- ALTER migrations must be valid on SQLite ----

func TestScaffold_AddNotNullColumn_MigrationHasDefault(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	runScaffold(t, root, manifest, genModel(t, manifest, "Product", "name:string!"))

	model := genModel(t, manifest, "Product", "stock:int!", "sku:string,unique!", "tags:string,array!")
	runScaffold(t, root, manifest, model)

	mig := findMigration(t, root, "_add_")
	// SQLite rejects ADD COLUMN ... NOT NULL without a default.
	assertContains(t, mig, "ADD COLUMN stock INTEGER NOT NULL DEFAULT 0", "add-column migration")
	// SQLite rejects inline UNIQUE in ADD COLUMN — must become an index.
	assertNotContains(t, mig, "sku VARCHAR", "no varchar without size given")
	assertContains(t, mig, "CREATE UNIQUE INDEX IF NOT EXISTS idx_products_sku_unique ON products(sku);", "unique add-column migration")
	if strings.Contains(mig, "ADD COLUMN sku TEXT NOT NULL UNIQUE") {
		t.Errorf("inline UNIQUE in ADD COLUMN is invalid on SQLite:\n%s", mig)
	}
	// Arrays are JSON TEXT on SQLite; zero default is an empty JSON array.
	assertContains(t, mig, "ADD COLUMN tags TEXT NOT NULL DEFAULT '[]'", "array add-column migration")
}

func TestScaffold_AddIndexedColumn_MigrationCreatesIndex(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	runScaffold(t, root, manifest, genModel(t, manifest, "Post", "title:string!"))

	model := genModel(t, manifest, "Post", "author_id:string,index!")
	runScaffold(t, root, manifest, model)

	mig := findMigration(t, root, "_add_author_id")
	assertContains(t, mig, "CREATE INDEX IF NOT EXISTS idx_posts_author_id ON posts(author_id);", "indexed add-column migration")
}

func TestScaffold_DefaultModifier_LiteralQuoting(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Order2",
		"status:string,default=pending!",
		"retries:int,default=3!",
		"note:string,default=O'Brien!",
	)
	runScaffold(t, root, manifest, model)

	mig := findMigration(t, root, "create_order2s")
	assertContains(t, mig, "DEFAULT 'pending'", "string default stays quoted")
	assertContains(t, mig, "DEFAULT 3", "numeric default is unquoted")
	assertContains(t, mig, "DEFAULT 'O''Brien'", "embedded quote is escaped")
}

// ---- Store SQL ----

func TestScaffold_Store_ListIsOrdered(t *testing.T) {
	for _, db := range []string{"sqlite", "postgres"} {
		t.Run(db, func(t *testing.T) {
			root, manifest := projectSetup(t, db, "rest")
			runScaffold(t, root, manifest, genModel(t, manifest, "Product", "name:string!"))
			store := readFile(t, root, "internal/adapters/store/product_store_gen.go")
			assertContains(t, store, "ORDER BY id DESC", db+" list query must have a deterministic order")
		})
	}
}

// ---- SSR bindForm coverage for time/json fields ----

func TestScaffold_SSR_Handler_BindForm_TimeAndJSON(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Event",
		"starts_at:time!", "ends_at:time", "payload:json!",
	)
	runScaffold(t, root, manifest, model)

	handler := readFile(t, root, "internal/adapters/http/event_handler_gen.go")
	assertGoSyntax(t, handler, "event_handler_gen.go")
	assertContains(t, handler, `"time"`, "handler imports time")
	assertContains(t, handler, `"encoding/json"`, "handler imports json")
	assertContains(t, handler, "time.Parse", "bindForm parses time fields")
	assertContains(t, handler, "item.StartsAt = t", "bindForm binds NOT NULL time field")
	assertContains(t, handler, "item.EndsAt = &t", "bindForm binds nullable time field")
	assertContains(t, handler, "json.Valid", "bindForm validates json fields")

	view := readFile(t, root, "web/views/event.templ")
	assertContains(t, view, `type="datetime-local"`, "time fields render datetime-local inputs")
}

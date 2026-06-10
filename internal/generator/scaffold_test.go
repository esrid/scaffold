package generator_test

import (
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esrid/scaffold/internal/generator"
	"github.com/esrid/scaffold/internal/parser"
)

// ---- Helpers ----

const testModule = "github.com/test/testapp"

// projectSetup creates a minimal temp project directory for generator tests.
func projectSetup(t *testing.T, db, apiMode string) (string, *parser.Manifest) {
	t.Helper()
	root := t.TempDir()

	storeDir := filepath.Join(root, "internal", "adapters", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema.sql"), nil, 0644); err != nil {
		t.Fatalf("write schema.sql: %v", err)
	}

	manifest := &parser.Manifest{
		Module:  testModule,
		DB:      db,
		APIMode: apiMode,
		Models:  map[string]parser.ManifestModel{},
	}
	if err := parser.SaveManifest(root, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	return root, manifest
}

func genModel(t *testing.T, manifest *parser.Manifest, name string, fields ...string) *parser.Model {
	t.Helper()
	fs, err := parser.ParseFields(fields)
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	model, err := parser.BuildModel(name, fs, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	return model
}

func runScaffold(t *testing.T, root string, manifest *parser.Manifest, model *parser.Model) {
	t.Helper()
	manifest.Models[model.Name] = model.ManifestEntry()
	g := generator.New(root, testModule, manifest, false)
	if _, err := g.Scaffold(model); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
}

func runDestroy(t *testing.T, root string, manifest *parser.Manifest, model *parser.Model) {
	t.Helper()
	g := generator.New(root, testModule, manifest, false)
	if _, err := g.Destroy(model); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func assertExists(t *testing.T, root, rel string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("expected file %s to exist: %v", rel, err)
		return ""
	}
	return string(data)
}

func assertNotExists(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
		t.Errorf("expected file %s to NOT exist", rel)
	}
}

func assertContains(t *testing.T, content, substr, desc string) {
	t.Helper()
	if !strings.Contains(content, substr) {
		t.Errorf("%s: expected to contain %q", desc, substr)
	}
}

func assertNotContains(t *testing.T, content, substr, desc string) {
	t.Helper()
	if strings.Contains(content, substr) {
		t.Errorf("%s: expected NOT to contain %q", desc, substr)
	}
}

func assertGoSyntax(t *testing.T, content, name string) {
	t.Helper()
	fset := token.NewFileSet()
	if _, err := goparser.ParseFile(fset, name, content, goparser.AllErrors); err != nil {
		t.Errorf("Go syntax error in %s: %v\n---\n%s\n---", name, err, content)
	}
}

// ---- REST mode tests ----

func TestScaffold_REST_CreatesExpectedFiles(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Product", "name:string!", "price:float!")
	runScaffold(t, root, manifest, model)

	assertExists(t, root, "internal/core/domain/product.go")
	assertExists(t, root, "internal/core/ports/product.go")
	assertExists(t, root, "internal/core/services/product_service_gen.go")
	assertExists(t, root, "internal/core/services/product_service.go")
	assertExists(t, root, "internal/adapters/store/product_store_gen.go")
	assertExists(t, root, "internal/adapters/store/product_store.go")
	assertExists(t, root, "internal/app/registry.go")
	assertExists(t, root, "internal/adapters/store/migrations/00002_create_products.sql")

	// SSR-specific files must NOT exist
	assertNotExists(t, root, "internal/adapters/http/product_handler_gen.go")
	assertNotExists(t, root, "web/views/product.templ")

	// gRPC-specific files must NOT exist
	assertNotExists(t, root, "internal/adapters/grpc/pb/product.proto")
	assertNotExists(t, root, "internal/adapters/grpc/product_handler_gen.go")
}

func TestScaffold_REST_Registry_UsesGenericHandler(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Product", "name:string!", "price:float!")
	runScaffold(t, root, manifest, model)

	reg := assertExists(t, root, "internal/app/registry.go")
	assertContains(t, reg, "CRUDHandler[domain.Product]", "REST registry")
	assertContains(t, reg, `httpadapter.NewCRUDHandler(svcs.Product`, "REST registry")
	assertNotContains(t, reg, "template.Template", "REST registry should not take tmpl param")
	assertGoSyntax(t, reg, "registry.go")
}

func TestScaffold_REST_Domain_ContainsFields(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Order", "status:string!", "total:float!", "notes:string")
	runScaffold(t, root, manifest, model)

	domain := assertExists(t, root, "internal/core/domain/order_gen.go")
	// Use json tags as stable markers — gofmt aligns field types with spaces but never touches tags.
	assertContains(t, domain, `json:"status"`, "domain struct has status field")
	assertContains(t, domain, `json:"total"`, "domain struct has total field")
	assertContains(t, domain, `json:"notes"`, "domain struct has notes field")
	// Nullable fields must be pointers — check the type appears anywhere in the file.
	assertContains(t, domain, "*string", "nullable field is a pointer type")
	assertContains(t, domain, "float64", "float field type")
	assertContains(t, domain, `json:"id"`, "auto field ID")
	assertContains(t, domain, `json:"created_at"`, "auto field CreatedAt")
	assertGoSyntax(t, domain, "order_gen.go")
	// Validate() lives in the user-owned file, never the generated one.
	assertNotContains(t, domain, "Validate", "Validate not in _gen.go")
}

func TestScaffold_REST_Store_SQLite_UsesPlaceholders(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Item", "name:string!", "qty:int!")
	runScaffold(t, root, manifest, model)

	store := assertExists(t, root, "internal/adapters/store/item_store_gen.go")
	assertContains(t, store, "?", "SQLite uses ? placeholders")
	assertNotContains(t, store, "$1", "SQLite must not use $N placeholders")
	assertGoSyntax(t, store, "item_store_gen.go")
}

func TestScaffold_REST_Store_Postgres_UsesDollarPlaceholders(t *testing.T) {
	root, manifest := projectSetup(t, "postgres", "rest")
	model := genModel(t, manifest, "Item", "name:string!", "qty:int!")
	runScaffold(t, root, manifest, model)

	store := assertExists(t, root, "internal/adapters/store/item_store_gen.go")
	assertContains(t, store, "$1", "Postgres uses $N placeholders")
	assertNotContains(t, store, "?", "Postgres must not use ? placeholders")
	assertGoSyntax(t, store, "item_store_gen.go")
}

// ---- SSR mode tests ----

func TestScaffold_SSR_CreatesTemplViews(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Post", "title:string!", "body:string!", "views:int")
	runScaffold(t, root, manifest, model)

	assertExists(t, root, "internal/adapters/http/post_handler_gen.go")
	assertExists(t, root, "internal/adapters/http/post_handler.go")
	view := assertExists(t, root, "web/views/post.templ")
	assertContains(t, view, "templ PostList(", "List component")
	assertContains(t, view, "templ PostForm(", "Form component")
	assertContains(t, view, "templ PostShow(", "Show component")

	// No JSON handler
	assertNotExists(t, root, "internal/adapters/grpc/pb/post.proto")
}

func TestScaffold_SSR_Registry_UsesPerModelHandler(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Post", "title:string!", "body:string!")
	runScaffold(t, root, manifest, model)

	reg := assertExists(t, root, "internal/app/registry.go")
	assertContains(t, reg, "*httpadapter.PostHandler", "SSR registry uses per-model handler")
	assertNotContains(t, reg, "template.Template", "templ SSR registry must not take tmpl param")
	assertContains(t, reg, "httpadapter.NewPostHandler(svcs.Post", "SSR registry wires handler")
	assertNotContains(t, reg, "CRUDHandler[domain.Post]", "SSR must not use generic CRUDHandler")
	assertGoSyntax(t, reg, "registry.go")
}

func TestScaffold_SSR_Handler_BindForm_AllScalarTypes(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Thing",
		"name:string!", "count:int!", "score:float!", "active:bool!",
		"label:string", "qty:int", "rate:float", "flag:bool",
	)
	runScaffold(t, root, manifest, model)

	handler := assertExists(t, root, "internal/adapters/http/thing_handler_gen.go")

	// Required fields (non-pointer)
	assertContains(t, handler, `item.Name = r.FormValue("name")`, "string bind")
	assertContains(t, handler, `strconv.Atoi(r.FormValue("count"))`, "int bind")
	assertContains(t, handler, `strconv.ParseFloat(r.FormValue("score")`, "float bind")
	assertContains(t, handler, `r.FormValue("active") == "on"`, "bool bind")

	// Nullable (pointer) fields
	assertContains(t, handler, `r.Form["label"]`, "nullable string bind")
	assertContains(t, handler, `strconv.Atoi(val[0])`, "nullable int bind")
	assertContains(t, handler, `strconv.ParseFloat(val[0], 64)`, "nullable float bind")

	assertGoSyntax(t, handler, "thing_handler_gen.go")
}

func TestScaffold_SSR_Handler_BindForm_ArrayTypes(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Bag",
		"tags:[]string!", "scores:[]int", "ids:[]int64", "weights:[]float64", "flags:[]bool",
	)
	runScaffold(t, root, manifest, model)

	handler := assertExists(t, root, "internal/adapters/http/bag_handler_gen.go")

	// Every array element type binds from the multi-valued form field.
	assertContains(t, handler, `r.Form["tags"]`, "[]string bind reads multi-valued form")
	assertContains(t, handler, `item.Tags = v`, "[]string assigned directly")
	assertContains(t, handler, `r.Form["scores"]`, "[]int bind")
	assertContains(t, handler, `strconv.Atoi(s)`, "[]int parses each element")
	assertContains(t, handler, `strconv.ParseInt(s, 10, 64)`, "[]int64 parses each element")
	assertContains(t, handler, `strconv.ParseFloat(s, 64)`, "[]float64 parses each element")
	assertContains(t, handler, `s == "on" || s == "true"`, "[]bool parses each element")

	assertGoSyntax(t, handler, "bag_handler_gen.go")
}

func TestScaffold_SSR_Handler_HasHTMXDelete(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Widget", "name:string!")
	runScaffold(t, root, manifest, model)

	handler := assertExists(t, root, "internal/adapters/http/widget_handler_gen.go")
	assertContains(t, handler, `HX-Request`, "HTMX delete returns 200")
	assertGoSyntax(t, handler, "widget_handler_gen.go")
}

func TestScaffold_SSR_ListView_ContainsModelFields(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Book", "title:string!", "author:string!", "pages:int!")
	runScaffold(t, root, manifest, model)

	view := assertExists(t, root, "web/views/book.templ")
	assertContains(t, view, "Title", "list column header")
	assertContains(t, view, "Author", "list column header")
	assertContains(t, view, "Pages", "list column header")
	assertContains(t, view, "display(item.Title)", "list field value")
	assertContains(t, view, "display(item.Author)", "list field value")
	assertContains(t, view, "/books/new", "new link")
	assertContains(t, view, "hx-delete", "HTMX delete button")
	assertContains(t, view, "@Layout(", "wraps content in shared layout")
}

func TestScaffold_SSR_FormView_ContainsInputs(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Book", "title:string!", "pages:int!", "published:bool!")
	runScaffold(t, root, manifest, model)

	view := assertExists(t, root, "web/views/book.templ")
	assertContains(t, view, `name="title"`, "title input")
	assertContains(t, view, `name="pages"`, "pages input")
	assertContains(t, view, `name="published"`, "boolean input")
	assertContains(t, view, `type="checkbox"`, "bool renders as checkbox")
	assertContains(t, view, "/books", "action URL contains plural")
}

func TestScaffold_SSR_ShowView_ContainsFields(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Event", "name:string!", "capacity:int!")
	runScaffold(t, root, manifest, model)

	view := assertExists(t, root, "web/views/event.templ")
	assertContains(t, view, "Name", "show field label")
	assertContains(t, view, "Capacity", "show field label")
	assertContains(t, view, "display(item.Name)", "show field value")
	assertContains(t, view, "hx-delete", "HTMX delete in show page")
}

func TestScaffold_SSR_Views_WriteOnce(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")

	// First gen creates the view.
	model := genModel(t, manifest, "Task", "title:string!")
	runScaffold(t, root, manifest, model)
	list1 := readFile(t, root, "web/views/task.templ")
	assertContains(t, list1, "Title", "first gen has title")
	assertNotContains(t, list1, "Priority", "first gen has no priority")

	// Second gen adds a field — but views are WRITE-ONCE, so the existing view
	// is left untouched (the user owns it now).
	model2 := genModel(t, manifest, "Task", "title:string!", "priority:int!")
	runScaffold(t, root, manifest, model2)
	list2 := readFile(t, root, "web/views/task.templ")
	assertNotContains(t, list2, "Priority", "write-once: view not clobbered on re-gen")

	// --regen-views forces a fresh scaffold that includes the new field.
	model3 := genModel(t, manifest, "Task", "title:string!", "priority:int!")
	manifest.Models["Task"] = model3.ManifestEntry()
	g := generator.New(root, testModule, manifest, false)
	g.RegenViews = true
	if _, err := g.Scaffold(model3); err != nil {
		t.Fatalf("Scaffold (regen): %v", err)
	}
	list3 := readFile(t, root, "web/views/task.templ")
	assertContains(t, list3, "Priority", "--regen-views refreshes the view")
}

func TestScaffold_NoFields_KeepsExistingFields(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")

	// First gen
	model := genModel(t, manifest, "Task", "title:string!")
	runScaffold(t, root, manifest, model)

	list1 := readFile(t, root, "web/views/task.templ")
	assertContains(t, list1, "Title", "first gen has title")

	// Second gen (no fields)
	model2 := genModel(t, manifest, "Task")
	runScaffold(t, root, manifest, model2)

	list2 := readFile(t, root, "web/views/task.templ")
	assertContains(t, list2, "Title", "second gen with no fields preserves title")
}

// ---- gRPC mode tests ----

func TestScaffold_GRPC_CreatesProtoAndHandler(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "grpc")
	model := genModel(t, manifest, "Product", "name:string!", "price:float!")
	runScaffold(t, root, manifest, model)

	assertExists(t, root, "internal/adapters/grpc/pb/product.proto")
	assertExists(t, root, "internal/adapters/grpc/product_handler_gen.go")
	assertExists(t, root, "internal/adapters/grpc/shared.go")

	// SSR templates must NOT exist in gRPC mode
	assertNotExists(t, root, "internal/adapters/http/product_handler_gen.go")
	assertNotExists(t, root, "web/views/product.templ")
}

func TestScaffold_GRPC_Proto_FieldNumbers(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "grpc")
	model := genModel(t, manifest, "Product", "name:string!", "price:float!", "sku:string!")
	runScaffold(t, root, manifest, model)

	proto := assertExists(t, root, "internal/adapters/grpc/pb/product.proto")
	assertContains(t, proto, "string id = 1;", "id is field 1")
	assertContains(t, proto, "string name = 2;", "name is field 2")
	assertContains(t, proto, "double price = 3;", "price is field 3")
	assertContains(t, proto, "string sku = 4;", "sku is field 4")
	assertContains(t, proto, "google.protobuf.Timestamp created_at = 5;", "created_at field")
	assertContains(t, proto, "google.protobuf.Timestamp updated_at = 6;", "updated_at field")
}

func TestScaffold_GRPC_Proto_TypeMapping(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "grpc")
	model := genModel(t, manifest, "Item",
		"name:string!", "count:int!", "score:float!", "active:bool!",
		"label:string", "qty:int", "meta:json!",
	)
	runScaffold(t, root, manifest, model)

	proto := assertExists(t, root, "internal/adapters/grpc/pb/item.proto")
	assertContains(t, proto, "string name = 2;", "string→string")
	assertContains(t, proto, "int32 count = 3;", "int→int32")
	assertContains(t, proto, "double score = 4;", "float64→double")
	assertContains(t, proto, "bool active = 5;", "bool→bool")
	assertContains(t, proto, "optional string label = 6;", "nullable string→optional string")
	assertContains(t, proto, "optional int32 qty = 7;", "nullable int→optional int32")
	assertContains(t, proto, "bytes meta = 8;", "json.RawMessage→bytes")
}

func TestScaffold_GRPC_Handler_TranslatesAllErrors(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "grpc")
	model := genModel(t, manifest, "User", "email:string!")
	runScaffold(t, root, manifest, model)

	shared := assertExists(t, root, "internal/adapters/grpc/shared.go")
	assertContains(t, shared, "codes.NotFound", "NotFoundError → NotFound")
	assertContains(t, shared, "codes.AlreadyExists", "AlreadyExistsError → AlreadyExists")
	assertContains(t, shared, "codes.InvalidArgument", "ValidationError → InvalidArgument")
	assertContains(t, shared, "codes.Unauthenticated", "UnauthorizedError → Unauthenticated")
	assertContains(t, shared, "codes.PermissionDenied", "ForbiddenError → PermissionDenied")
	assertContains(t, shared, "codes.Internal", "fallback → Internal")
	assertGoSyntax(t, shared, "shared.go")
}

func TestScaffold_GRPC_Registry_HasGRPCHandlers(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "grpc")
	model := genModel(t, manifest, "User", "email:string!")
	runScaffold(t, root, manifest, model)

	reg := assertExists(t, root, "internal/app/registry.go")
	assertContains(t, reg, "GRPCHandlers", "registry has GRPCHandlers group")
	assertContains(t, reg, "*grpcadapter.UserHandler", "gRPC handler type")
	assertGoSyntax(t, reg, "registry.go")
}

// ---- Destroy tests ----

func TestDestroy_REST_RemovesCoreFiles(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Tag", "name:string!")
	runScaffold(t, root, manifest, model)

	assertExists(t, root, "internal/core/domain/tag.go")
	assertExists(t, root, "internal/adapters/store/tag_store_gen.go")

	runDestroy(t, root, manifest, model)

	assertNotExists(t, root, "internal/core/domain/tag.go")
	assertNotExists(t, root, "internal/core/ports/tag.go")
	assertNotExists(t, root, "internal/core/services/tag_service_gen.go")
	assertNotExists(t, root, "internal/core/services/tag_service.go")
	assertNotExists(t, root, "internal/adapters/store/tag_store_gen.go")
	assertNotExists(t, root, "internal/adapters/store/tag_store.go")
}

func TestDestroy_SSR_RemovesHandlerAndTemplates(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Note", "content:string!")
	runScaffold(t, root, manifest, model)

	assertExists(t, root, "internal/adapters/http/note_handler_gen.go")
	assertExists(t, root, "web/views/note.templ")

	runDestroy(t, root, manifest, model)

	assertNotExists(t, root, "internal/adapters/http/note_handler_gen.go")
	assertNotExists(t, root, "web/views/note.templ")
}

func TestDestroy_GRPC_RemovesProtoAndHandler(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "grpc")
	model := genModel(t, manifest, "Message", "body:string!")
	runScaffold(t, root, manifest, model)

	assertExists(t, root, "internal/adapters/grpc/pb/message.proto")
	assertExists(t, root, "internal/adapters/grpc/message_handler_gen.go")

	runDestroy(t, root, manifest, model)

	assertNotExists(t, root, "internal/adapters/grpc/pb/message.proto")
	assertNotExists(t, root, "internal/adapters/grpc/message_handler_gen.go")
}

func TestDestroy_RemovesModel_FromManifest(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Category", "name:string!")
	runScaffold(t, root, manifest, model)

	if _, ok := manifest.Models["Category"]; !ok {
		t.Fatal("model should be in manifest after scaffold")
	}

	runDestroy(t, root, manifest, model)

	if _, ok := manifest.Models["Category"]; ok {
		t.Error("model should be removed from manifest after destroy")
	}

	// Registry should be regenerated without the model
	reg := assertExists(t, root, "internal/app/registry.go")
	assertNotContains(t, reg, "Category", "registry must not reference destroyed model")
}

// ---- Multiple models ----

func TestScaffold_MultipleModels_Registry(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")

	m1 := genModel(t, manifest, "Product", "name:string!")
	runScaffold(t, root, manifest, m1)

	m2 := genModel(t, manifest, "Category", "label:string!")
	runScaffold(t, root, manifest, m2)

	reg := assertExists(t, root, "internal/app/registry.go")
	assertContains(t, reg, "Product", "registry has Product")
	assertContains(t, reg, "Category", "registry has Category")
	assertGoSyntax(t, reg, "registry.go")
}

func TestScaffold_MultipleModels_SSR_EachGetsTemplates(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")

	m1 := genModel(t, manifest, "Post", "title:string!")
	runScaffold(t, root, manifest, m1)

	m2 := genModel(t, manifest, "Comment", "body:string!")
	runScaffold(t, root, manifest, m2)

	assertExists(t, root, "web/views/post.templ")
	assertExists(t, root, "web/views/comment.templ")

	reg := assertExists(t, root, "internal/app/registry.go")
	assertContains(t, reg, "*httpadapter.PostHandler", "registry has Post")
	assertContains(t, reg, "*httpadapter.CommentHandler", "registry has Comment")
}

// ---- Schema SQL ----

func TestScaffold_Schema_ContainsTableDefinition(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Category", "name:string!", "slug:string{unique}!")
	runScaffold(t, root, manifest, model)

	schema := assertExists(t, root, "internal/adapters/store/schema.sql")
	assertContains(t, schema, "CREATE TABLE IF NOT EXISTS categories", "schema has table")
	assertContains(t, schema, "name TEXT NOT NULL", "NOT NULL field")
	assertContains(t, schema, "UNIQUE", "unique modifier")
	assertContains(t, schema, "scaffold:table:Category:start", "scaffold start marker")
	assertContains(t, schema, "scaffold:table:Category:end", "scaffold end marker")
}

func TestScaffold_Migration_CreatedForNewModel(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Label", "text:string!")
	runScaffold(t, root, manifest, model)

	assertExists(t, root, "internal/adapters/store/migrations/00002_create_labels.sql")
}

func TestScaffold_StructPacking(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Product", "active:bool!", "qty:int!", "name:string!")
	runScaffold(t, root, manifest, model)

	domain := assertExists(t, root, "internal/core/domain/product_gen.go")

	// Isolate the struct body (no markers anymore) to check field packing order.
	startIdx := strings.Index(domain, "type Product struct {")
	if startIdx == -1 {
		t.Fatalf("could not find Product struct in product_gen.go")
	}
	endIdx := strings.Index(domain[startIdx:], "}")
	if endIdx == -1 {
		t.Fatalf("could not find struct closing brace")
	}
	fieldsBlock := domain[startIdx : startIdx+endIdx]

	// Verify that the fields in the struct are sorted by size descending (Struct Packing)
	// with alphabetical tie-breakers.
	// Sizes: CreatedAt (24), UpdatedAt (24), ID (16), Name (16), Qty (8), Active (1)
	expectedOrder := []string{
		"CreatedAt",
		"UpdatedAt",
		"ID",
		"Name",
		"Qty",
		"Active",
	}

	for i := 0; i < len(expectedOrder)-1; i++ {
		idx1 := strings.Index(fieldsBlock, expectedOrder[i])
		idx2 := strings.Index(fieldsBlock, expectedOrder[i+1])
		if idx1 == -1 {
			t.Errorf("expected to find %q in domain struct fields block", expectedOrder[i])
		}
		if idx2 == -1 {
			t.Errorf("expected to find %q in domain struct fields block", expectedOrder[i+1])
		}
		if idx1 > idx2 {
			t.Errorf("field %q (index %d) should come before %q (index %d) in domain struct fields block", expectedOrder[i], idx1, expectedOrder[i+1], idx2)
		}
	}
}

func TestScaffold_DomainSplit(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Widget", "name:string!")
	runScaffold(t, root, manifest, model)

	gen := assertExists(t, root, "internal/core/domain/widget_gen.go")
	assertContains(t, gen, "DO NOT EDIT", "gen file is marked generated")
	assertContains(t, gen, "type Widget struct", "struct lives in gen file")
	assertContains(t, gen, "GetID() string", "GetID in gen file")
	assertContains(t, gen, "WithID(id string)", "WithID in gen file")
	assertNotContains(t, gen, "scaffold:fields", "no markers in gen file")

	user := assertExists(t, root, "internal/core/domain/widget.go")
	assertContains(t, user, "Validate() error", "Validate in user file")
	assertNotContains(t, user, "type Widget struct", "struct not duplicated in user file")

	// Re-gen with a new field refreshes the generated struct; the user file is
	// never touched (it is in Unchanged).
	model2 := genModel(t, manifest, "Widget", "name:string!", "size:int!")
	runScaffold(t, root, manifest, model2)
	gen2 := readFile(t, root, "internal/core/domain/widget_gen.go")
	assertContains(t, gen2, `json:"size"`, "re-gen adds the new field to the struct")
}

func TestScaffold_Diff(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Gadget", "name:string!")
	runScaffold(t, root, manifest, model)

	// Re-gen adding a field, in --diff mode: diffs are produced, nothing written.
	model2 := genModel(t, manifest, "Gadget", "name:string!", "weight:int!")
	manifest.Models["Gadget"] = model2.ManifestEntry()
	g := generator.New(root, testModule, manifest, true)
	g.Diff = true
	res, err := g.Scaffold(model2)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	if len(res.Diffs) == 0 {
		t.Fatal("expected diffs in --diff mode")
	}
	joined := strings.Join(res.Diffs, "\n")
	if !strings.Contains(joined, "weight") {
		t.Errorf("diff should mention the new field:\n%s", joined)
	}
	// --diff must not touch disk: the struct still lacks the new field.
	gen := readFile(t, root, "internal/core/domain/gadget_gen.go")
	if strings.Contains(gen, "weight") {
		t.Error("--diff mode must not write files")
	}
}

func TestScaffold_NoHandler_SkipsHTTP(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	fs, err := parser.ParseFields([]string{"name:string!"})
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	model, err := parser.BuildModel("Product", fs, nil, manifest, "", true)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	runScaffold(t, root, manifest, model)

	// Verify store & service are created
	assertExists(t, root, "internal/core/domain/product.go")
	assertExists(t, root, "internal/core/ports/product.go")
	assertExists(t, root, "internal/core/services/product_service_gen.go")
	assertExists(t, root, "internal/adapters/store/product_store_gen.go")

	// Verify handlers are NOT created
	assertNotExists(t, root, "internal/adapters/http/product_handler_gen.go")
	assertNotExists(t, root, "internal/adapters/http/product_handler.go")
	assertNotExists(t, root, "web/views/product.templ")

	// Verify registry.go does not reference the Handler
	registry := assertExists(t, root, "internal/app/registry.go")
	if strings.Contains(registry, "Handlers.Product") || strings.Contains(registry, "httpadapter.NewProductHandler") {
		t.Error("expected registry.go to not reference Product Handler")
	}

	// Verify routes_gen.go does not mount routes for Product
	routes := assertExists(t, root, "internal/app/routes_gen.go")
	if strings.Contains(routes, "registry.Handlers.Product") {
		t.Error("expected routes_gen.go to not mount Product routes")
	}
}

func TestDestroy_BackupAndKeepCustom(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Product", "name:string!")
	runScaffold(t, root, manifest, model)

	// Add custom code into one of the write-once files
	customStorePath := filepath.Join(root, "internal", "adapters", "store", "product_store.go")
	customContent := "// custom method here"
	if err := os.WriteFile(customStorePath, []byte(customContent), 0644); err != nil {
		t.Fatalf("write custom store: %v", err)
	}

	// 1. First, test with KeepCustom = true. Custom files should be kept.
	g1 := generator.New(root, testModule, manifest, false)
	g1.KeepCustom = true
	_, err := g1.Destroy(model)
	if err != nil {
		t.Fatalf("Destroy (KeepCustom=true) failed: %v", err)
	}

	// Generated files must be gone
	assertNotExists(t, root, "internal/adapters/store/product_store_gen.go")
	assertNotExists(t, root, "internal/core/domain/product_gen.go")

	// Custom files must still exist
	assertExists(t, root, "internal/core/domain/product.go")
	assertExists(t, root, "internal/core/ports/product.go")
	assertExists(t, root, "internal/core/services/product_service.go")
	storeContent := assertExists(t, root, "internal/adapters/store/product_store.go")
	if storeContent != customContent {
		t.Errorf("expected custom file content to be preserved, got %q", storeContent)
	}

	// Check that backup was created
	backupsDir := filepath.Join(root, ".scaffold", "backups")
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 backup folder, got %d", len(entries))
	}
	backupFolder := filepath.Join(backupsDir, entries[0].Name())
	backupStorePath := filepath.Join(backupFolder, "internal", "adapters", "store", "product_store.go")
	backupContent, err := os.ReadFile(backupStorePath)
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if string(backupContent) != customContent {
		t.Errorf("expected backup file content %q, got %q", customContent, string(backupContent))
	}

	// Re-add to manifest for second step (we want to simulate deleting it without KeepCustom)
	manifest.Models["Product"] = model.ManifestEntry()

	// 2. Test with KeepCustom = false. Custom files must be deleted.
	g2 := generator.New(root, testModule, manifest, false)
	g2.KeepCustom = false
	_, err = g2.Destroy(model)
	if err != nil {
		t.Fatalf("Destroy (KeepCustom=false) failed: %v", err)
	}

	// Now custom files must also be deleted
	assertNotExists(t, root, "internal/core/domain/product.go")
	assertNotExists(t, root, "internal/core/ports/product.go")
	assertNotExists(t, root, "internal/core/services/product_service.go")
	assertNotExists(t, root, "internal/adapters/store/product_store.go")
}

func TestScaffold_FK_TargetValidationWarning(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")

	// 1. Create a model with fk referencing a missing table
	fs, err := parser.ParseFields([]string{"user_id:string,fk=users!"})
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	model, err := parser.BuildModel("Post", fs, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}

	// Should contain a warning about "users" target table
	if len(model.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(model.Warnings))
	}
	expectedWarning := `foreign key target table "users" for field "user_id" does not exist`
	if !strings.Contains(model.Warnings[0], expectedWarning) {
		t.Errorf("expected warning to contain %q, got %q", expectedWarning, model.Warnings[0])
	}

	// 2. Now scaffold the "User" model so the table exists
	userModel := genModel(t, manifest, "User", "name:string!")
	runScaffold(t, root, manifest, userModel)

	// Build "Post" again, warning should be gone
	model2, err := parser.BuildModel("Post", fs, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if len(model2.Warnings) != 0 {
		t.Errorf("expected 0 warnings after target model was scaffolded, got %v", model2.Warnings)
	}
}

func TestDestroy_FK_ReferencingBlocksAndForce(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")

	// Scaffold "User"
	userModel := genModel(t, manifest, "User", "name:string!")
	runScaffold(t, root, manifest, userModel)

	// Scaffold "Post" referencing "users"
	postModel := genModel(t, manifest, "Post", "user_id:string,fk=users!")
	runScaffold(t, root, manifest, postModel)

	// Attempt to destroy "User" without force. It should fail because of referencing constraint.
	g1 := generator.New(root, testModule, manifest, false)
	g1.Force = false
	_, err := g1.Destroy(userModel)
	if err == nil {
		t.Fatal("expected Destroy to fail because of active foreign key referencing constraint, but it succeeded")
	}
	expectedErr := `cannot destroy model "User": referenced by foreign key in Post (user_id)`
	if !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("expected error %q, got %q", expectedErr, err.Error())
	}

	// Attempt to destroy "User" WITH force. It should succeed.
	g2 := generator.New(root, testModule, manifest, false)
	g2.Force = true
	_, err = g2.Destroy(userModel)
	if err != nil {
		t.Fatalf("expected Destroy to succeed with Force=true, but got error: %v", err)
	}

	// User should be deleted successfully
	assertNotExists(t, root, "internal/core/domain/user_gen.go")
}

func TestScaffold_SoftDelete(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")

	// 1. Scaffold new model with soft-delete enabled
	model := genModel(t, manifest, "Product", "name:string!")
	model.SoftDelete = true
	manifest.Models["Product"] = model.ManifestEntry()

	runScaffold(t, root, manifest, model)
	model.IsNew = false

	// Verify domain model contains DeletedAt field
	domainContent := assertExists(t, root, "internal/core/domain/product_gen.go")
	if !strings.Contains(domainContent, "DeletedAt *time.Time") {
		t.Error("expected domain struct to contain DeletedAt *time.Time")
	}

	// Verify store queries filter by deleted_at IS NULL and soft-delete on Delete
	storeContent := assertExists(t, root, "internal/adapters/store/product_store_gen.go")
	if !strings.Contains(storeContent, "deleted_at IS NULL") {
		t.Error("expected store queries to contain 'deleted_at IS NULL' filters")
	}
	if !strings.Contains(storeContent, "UPDATE products SET deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted_at IS NULL") {
		t.Error("expected store Delete to be a soft-delete UPDATE query")
	}

	// Verify migration creates the deleted_at column
	migDir := filepath.Join(root, "internal", "adapters", "store", "migrations")
	migEntries, err := os.ReadDir(migDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	if len(migEntries) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migEntries))
	}
	migContent := readFile(t, root, filepath.Join("internal/adapters/store/migrations", migEntries[0].Name()))
	t.Logf("--- Migration 1 ---\n%s", migContent)
	if !strings.Contains(migContent, "deleted_at DATETIME") {
		t.Error("expected migration to create deleted_at column")
	}

	// 2. Re-gen disabling soft-delete
	fs, err := parser.ParseFields([]string{"name:string!"})
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	model, err = parser.BuildModel("Product", fs, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	model.PrevSoftDelete = true
	model.SoftDelete = false
	model.SoftDeleteJustEnabled = false
	model.MigrationVersion = parser.NextMigrationVersion(manifest)
	manifest.Models["Product"] = model.ManifestEntry()

	runScaffold(t, root, manifest, model)

	// Verify domain model no longer contains DeletedAt field
	domainContent2 := assertExists(t, root, "internal/core/domain/product_gen.go")
	if strings.Contains(domainContent2, "DeletedAt *time.Time") {
		t.Error("expected domain struct to NOT contain DeletedAt after disabling soft-delete")
	}

	// Verify alter migration drops the deleted_at column
	migEntries2, err := os.ReadDir(migDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	if len(migEntries2) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migEntries2))
	}
	// The second migration should be the alter migration dropping deleted_at
	migContent2 := readFile(t, root, filepath.Join("internal/adapters/store/migrations", migEntries2[1].Name()))
	t.Logf("--- Migration 2 ---\n%s", migContent2)
	if !strings.Contains(migContent2, "ALTER TABLE products DROP COLUMN deleted_at;") {
		t.Error("expected alter migration to drop deleted_at column")
	}

	// 3. Re-gen enabling soft-delete back
	model, err = parser.BuildModel("Product", fs, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	model.PrevSoftDelete = false
	model.SoftDelete = true
	model.SoftDeleteJustEnabled = true
	model.MigrationVersion = parser.NextMigrationVersion(manifest)
	manifest.Models["Product"] = model.ManifestEntry()

	runScaffold(t, root, manifest, model)

	// Verify alter migration adds the deleted_at column back
	migEntries3, err := os.ReadDir(migDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	if len(migEntries3) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(migEntries3))
	}
	migContent3 := readFile(t, root, filepath.Join("internal/adapters/store/migrations", migEntries3[2].Name()))
	t.Logf("--- Migration 3 ---\n%s", migContent3)
	if !strings.Contains(migContent3, "ALTER TABLE products ADD COLUMN deleted_at DATETIME;") {
		t.Error("expected alter migration to add deleted_at column")
	}
}

func TestScaffold_UniqueTogether(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")

	// 1. Scaffold new model with unique-together enabled
	model := genModel(t, manifest, "Product", "name:string!", "category:string!")
	model.UniqueTogether = [][]string{{"name", "category"}}
	manifest.Models["Product"] = model.ManifestEntry()

	runScaffold(t, root, manifest, model)
	model.IsNew = false

	// Verify schema.sql contains compound unique index
	schemaContent := assertExists(t, root, "internal/adapters/store/schema.sql")
	if !strings.Contains(schemaContent, "CREATE UNIQUE INDEX IF NOT EXISTS idx_products_name_category_unique ON products(name, category);") {
		t.Error("expected schema.sql to contain compound unique index")
	}

	// Verify initial migration creates compound unique index
	migDir := filepath.Join(root, "internal", "adapters", "store", "migrations")
	migEntries, err := os.ReadDir(migDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	if len(migEntries) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migEntries))
	}
	migContent := readFile(t, root, filepath.Join("internal/adapters/store/migrations", migEntries[0].Name()))
	if !strings.Contains(migContent, "CREATE UNIQUE INDEX IF NOT EXISTS idx_products_name_category_unique ON products(name, category);") {
		t.Error("expected initial migration to create compound unique index")
	}

	// 2. Re-gen adding a new compound unique index and removing the old one
	fs, err := parser.ParseFields([]string{"name:string!", "category:string!", "sku:string!", "vendor:string!"})
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	model, err = parser.BuildModel("Product", fs, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	model.IsNew = false
	model.PrevUniqueTogether = [][]string{{"name", "category"}}
	model.UniqueTogether = [][]string{{"sku", "vendor"}}
	model.MigrationVersion = parser.NextMigrationVersion(manifest)
	manifest.Models["Product"] = model.ManifestEntry()

	runScaffold(t, root, manifest, model)

	// Verify alter migration drops old index and creates new compound index
	migEntries2, err := os.ReadDir(migDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	if len(migEntries2) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migEntries2))
	}
	migContent2 := readFile(t, root, filepath.Join("internal/adapters/store/migrations", migEntries2[1].Name()))
	t.Logf("--- UniqueTogether Alter Migration ---\n%s", migContent2)
	if !strings.Contains(migContent2, "DROP INDEX IF EXISTS idx_products_name_category_unique;") {
		t.Error("expected alter migration to drop old compound unique index")
	}
	if !strings.Contains(migContent2, "CREATE UNIQUE INDEX IF NOT EXISTS idx_products_sku_vendor_unique ON products(sku, vendor);") {
		t.Error("expected alter migration to create new compound unique index")
	}
}


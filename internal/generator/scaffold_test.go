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

	domain := assertExists(t, root, "internal/core/domain/order.go")
	// Use json tags as stable markers — gofmt aligns field types with spaces but never touches tags.
	assertContains(t, domain, `json:"status"`, "domain struct has status field")
	assertContains(t, domain, `json:"total"`, "domain struct has total field")
	assertContains(t, domain, `json:"notes"`, "domain struct has notes field")
	// Nullable fields must be pointers — check the type appears anywhere in the file.
	assertContains(t, domain, "*string", "nullable field is a pointer type")
	assertContains(t, domain, "float64", "float field type")
	assertContains(t, domain, `json:"id"`, "auto field ID")
	assertContains(t, domain, `json:"created_at"`, "auto field CreatedAt")
	assertGoSyntax(t, domain, "order.go")
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

func TestScaffold_SSR_Update_RegeneratesViews(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")

	// First gen
	model := genModel(t, manifest, "Task", "title:string!")
	runScaffold(t, root, manifest, model)

	list1 := readFile(t, root, "web/views/task.templ")
	assertContains(t, list1, "Title", "first gen has title")
	assertNotContains(t, list1, "Priority", "first gen has no priority")

	// Second gen (add field)
	model2 := genModel(t, manifest, "Task", "title:string!", "priority:int!")
	runScaffold(t, root, manifest, model2)

	list2 := readFile(t, root, "web/views/task.templ")
	assertContains(t, list2, "Priority", "second gen adds priority")
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

	domain := assertExists(t, root, "internal/core/domain/product.go")

	startIdx := strings.Index(domain, "// scaffold:fields:start")
	endIdx := strings.Index(domain, "// scaffold:fields:end")
	if startIdx == -1 || endIdx == -1 {
		t.Fatalf("could not find scaffold:fields markers")
	}
	fieldsBlock := domain[startIdx:endIdx]

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
	// Write mock app.go with route markers
	appDir := filepath.Join(root, "internal", "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	appMock := `package app
func mountRoutes() {
	// scaffold:routes:start
	// scaffold:routes:end
}`
	if err := os.WriteFile(filepath.Join(appDir, "app.go"), []byte(appMock), 0644); err != nil {
		t.Fatalf("write mock app.go: %v", err)
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

	// Verify app.go does not mount routes for Product
	appFile := assertExists(t, root, "internal/app/app.go")
	if strings.Contains(appFile, "registry.Handlers.Product") {
		t.Error("expected app.go to not mount Product routes")
	}
}


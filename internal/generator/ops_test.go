package generator_test

import (
	"testing"

	"github.com/esrid/scaffold/internal/parser"
)

// TestScaffold_SkipsOps verifies --skip removes routes from the SSR handler and
// affordances from the views, while keeping the rest.
func TestScaffold_SkipsOps(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "ssr")
	model := genModel(t, manifest, "Note", "title:string!")
	model.Ops = parser.OpsFromSkipped([]string{"create", "delete"})
	runScaffold(t, root, manifest, model)

	handler := readFile(t, root, "internal/adapters/http/note_handler_gen.go")
	assertContains(t, handler, "mux.Handle(\"GET /{$}\", http.HandlerFunc(h.List))", "list route kept")
	assertContains(t, handler, "h.Show)", "read route kept")
	assertContains(t, handler, "h.Update)", "update route kept")
	assertNotContains(t, handler, "h.Create)", "create route removed")
	assertNotContains(t, handler, "h.New)", "new route removed")
	assertNotContains(t, handler, "\"DELETE ", "delete route removed")

	view := readFile(t, root, "web/views/note.templ")
	assertNotContains(t, view, "btn btn-primary\">New Note", "list New button removed")
	assertNotContains(t, view, "Delete this Note?", "delete affordances removed")
	assertContains(t, view, ">Edit</a>", "edit affordance kept")

	// Op selection is persisted for re-gen.
	entry := manifest.Models["Note"]
	if len(entry.SkippedOps) != 2 {
		t.Fatalf("manifest SkippedOps = %v, want 2 entries", entry.SkippedOps)
	}
}

// TestScaffold_SkipsOps_REST verifies the REST registry wires the per-model
// CRUDOps so the generic handler registers only the enabled routes at runtime.
func TestScaffold_SkipsOps_REST(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "rest")
	model := genModel(t, manifest, "Note", "title:string!")
	model.Ops = parser.OpsFromSkipped([]string{"create", "delete"})
	runScaffold(t, root, manifest, model)

	registry := readFile(t, root, "internal/app/registry.go")
	assertContains(t, registry, "Create: false", "create disabled in CRUDOps")
	assertContains(t, registry, "Delete: false", "delete disabled in CRUDOps")
	assertContains(t, registry, "List: true", "list enabled in CRUDOps")
	assertContains(t, registry, "Update: true", "update enabled in CRUDOps")
}

// TestScaffold_SkipsOps_GRPC verifies skipped ops drop both the proto RPC and
// the handler method (kept consistent so the handler matches the service).
func TestScaffold_SkipsOps_GRPC(t *testing.T) {
	root, manifest := projectSetup(t, "sqlite", "grpc")
	model := genModel(t, manifest, "Note", "title:string!")
	model.Ops = parser.OpsFromSkipped([]string{"create", "delete"})
	runScaffold(t, root, manifest, model)

	proto := readFile(t, root, "internal/adapters/grpc/pb/note.proto")
	assertNotContains(t, proto, "rpc CreateNote", "create rpc removed")
	assertNotContains(t, proto, "rpc DeleteNote", "delete rpc removed")
	assertContains(t, proto, "rpc GetNote", "get rpc kept")
	assertContains(t, proto, "rpc ListNotes", "list rpc kept")

	handler := readFile(t, root, "internal/adapters/grpc/note_handler_gen.go")
	assertNotContains(t, handler, ") CreateNote(", "create method removed")
	assertNotContains(t, handler, ") DeleteNote(", "delete method removed")
	assertContains(t, handler, ") GetNote(", "get method kept")
}

package parser

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- Manifest mode helpers ----

func TestManifest_IsSSR(t *testing.T) {
	cases := []struct {
		mode   string
		want   bool
	}{
		{"ssr", true},
		{"rest", false},
		{"grpc", false},
		{"", false},
	}
	for _, c := range cases {
		m := &Manifest{APIMode: c.mode}
		if got := m.IsSSR(); got != c.want {
			t.Errorf("APIMode=%q: IsSSR()=%v want %v", c.mode, got, c.want)
		}
	}
}

func TestManifest_IsREST(t *testing.T) {
	cases := []struct {
		mode string
		grpc bool
		want bool
	}{
		{"rest", false, true},
		{"", false, true},     // empty defaults to REST
		{"ssr", false, false},
		{"grpc", false, false},
		{"", true, false},     // legacy GRPC flag overrides empty
	}
	for _, c := range cases {
		m := &Manifest{APIMode: c.mode, GRPC: c.grpc}
		if got := m.IsREST(); got != c.want {
			t.Errorf("APIMode=%q GRPC=%v: IsREST()=%v want %v", c.mode, c.grpc, got, c.want)
		}
	}
}

func TestManifest_IsGRPC(t *testing.T) {
	cases := []struct {
		mode string
		grpc bool
		want bool
	}{
		{"grpc", false, true},
		{"", true, true},      // legacy --grpc flag
		{"rest", false, false},
		{"ssr", false, false},
	}
	for _, c := range cases {
		m := &Manifest{APIMode: c.mode, GRPC: c.grpc}
		if got := m.IsGRPC(); got != c.want {
			t.Errorf("APIMode=%q GRPC=%v: IsGRPC()=%v want %v", c.mode, c.grpc, got, c.want)
		}
	}
}

// ---- Save / Load round-trip ----

func TestManifest_SaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := &Manifest{
		Module:  "github.com/user/myapp",
		DB:      "postgres",
		APIMode: "ssr",
		Models: map[string]ManifestModel{
			"Product": {
				TableName:        "products",
				MigrationVersion: 2,
				Fields: []ManifestField{
					{Name: "name", GoType: "string", SQLType: "TEXT", NotNull: true},
					{Name: "price", GoType: "float64", SQLType: "REAL", NotNull: true},
				},
			},
		},
	}

	if err := SaveManifest(dir, original); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	loaded, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	if loaded.Module != original.Module {
		t.Errorf("Module: got %q want %q", loaded.Module, original.Module)
	}
	if loaded.DB != original.DB {
		t.Errorf("DB: got %q want %q", loaded.DB, original.DB)
	}
	if loaded.APIMode != original.APIMode {
		t.Errorf("APIMode: got %q want %q", loaded.APIMode, original.APIMode)
	}

	prod, ok := loaded.Models["Product"]
	if !ok {
		t.Fatal("Product model not found after round-trip")
	}
	if prod.TableName != "products" {
		t.Errorf("TableName: got %q want %q", prod.TableName, "products")
	}
	if len(prod.Fields) != 2 {
		t.Errorf("Fields: got %d want 2", len(prod.Fields))
	}
}

func TestLoadManifest_MissingFile_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Models == nil {
		t.Error("Models should be non-nil empty map")
	}
	if len(m.Models) != 0 {
		t.Errorf("expected empty models, got %d", len(m.Models))
	}
}

func TestSaveManifest_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	m := &Manifest{Module: "github.com/x/y", Models: map[string]ManifestModel{}}
	if err := SaveManifest(dir, m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".scaffold", "models.json")); err != nil {
		t.Errorf("manifest file not created: %v", err)
	}
}

// ---- Model building ----

func TestBuildModel_NewModel(t *testing.T) {
	manifest := &Manifest{DB: "sqlite", Models: map[string]ManifestModel{}}
	fields, _ := ParseFields([]string{"name:string!", "price:float!"})
	model, err := BuildModel("Product", fields, manifest, "")
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if !model.IsNew {
		t.Error("expected IsNew=true for new model")
	}
	if model.TableName != "products" {
		t.Errorf("TableName: got %q want %q", model.TableName, "products")
	}
	if model.MigrationVersion != 2 {
		t.Errorf("MigrationVersion: got %d want 2 (DB set → starts at 2)", model.MigrationVersion)
	}
}

func TestBuildModel_ExistingModel(t *testing.T) {
	manifest := &Manifest{
		DB: "sqlite",
		Models: map[string]ManifestModel{
			"Product": {
				TableName:        "products",
				MigrationVersion: 3,
				Fields: []ManifestField{
					{Name: "name", GoType: "string", NotNull: true},
				},
			},
		},
	}
	fields, _ := ParseFields([]string{"name:string!", "price:float!"})
	model, err := BuildModel("Product", fields, manifest, "")
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if model.IsNew {
		t.Error("expected IsNew=false for existing model")
	}
	if model.MigrationVersion != 4 {
		t.Errorf("MigrationVersion: got %d want 4 (prev+1)", model.MigrationVersion)
	}
	if len(model.PrevFields) != 1 {
		t.Errorf("PrevFields: got %d want 1", len(model.PrevFields))
	}
}

func TestBuildModel_TableNameOverride(t *testing.T) {
	manifest := &Manifest{Models: map[string]ManifestModel{}}
	fields, _ := ParseFields([]string{"name:string!"})
	model, err := BuildModel("Person", fields, manifest, "people")
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	if model.TableName != "people" {
		t.Errorf("TableName: got %q want %q", model.TableName, "people")
	}
}

func TestBuildModel_RejectsReservedName(t *testing.T) {
	manifest := &Manifest{Models: map[string]ManifestModel{}}
	fields, _ := ParseFields([]string{"name:string!"})
	_, err := BuildModel("type", fields, manifest, "")
	if err == nil {
		t.Error("expected error for reserved Go keyword model name")
	}
}

func TestBuildModel_RejectsLowercaseName(t *testing.T) {
	manifest := &Manifest{Models: map[string]ManifestModel{}}
	_, err := BuildModel("product", nil, manifest, "")
	if err == nil {
		t.Error("expected error for lowercase model name")
	}
}

// ---- DiffFields ----

func TestDiffFields_AddedAndRemoved(t *testing.T) {
	manifest := &Manifest{
		Models: map[string]ManifestModel{
			"Article": {
				Fields: []ManifestField{
					{Name: "title"},
					{Name: "body"},
				},
			},
		},
	}
	// Add "views", remove "body"
	fields, _ := ParseFields([]string{"title:string!", "views:int!"})
	model, err := BuildModel("Article", fields, manifest, "articles")
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	added, removed := model.DiffFields()
	if len(added) != 1 || added[0].Name != "views" {
		t.Errorf("added: got %v want [views]", added)
	}
	if len(removed) != 1 || removed[0].Name != "body" {
		t.Errorf("removed: got %v want [body]", removed)
	}
}

func TestDiffFields_NoChange(t *testing.T) {
	manifest := &Manifest{
		Models: map[string]ManifestModel{
			"Tag": {Fields: []ManifestField{{Name: "name"}}},
		},
	}
	fields, _ := ParseFields([]string{"name:string!"})
	model, _ := BuildModel("Tag", fields, manifest, "tags")
	added, removed := model.DiffFields()
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("expected no diff, got added=%v removed=%v", added, removed)
	}
}

// ---- Field parsing ----

func TestParseFields_AllTypes(t *testing.T) {
	cases := []struct {
		input    string
		goType   string
		sqlType  string
		notNull  bool
	}{
		{"name:string!", "string", "TEXT", true},
		{"desc:string", "*string", "TEXT", false},
		{"count:int!", "int", "INTEGER", true},
		{"qty:int", "*int", "INTEGER", false},
		{"score:float!", "float64", "REAL", true},
		{"rate:float", "*float64", "REAL", false},
		{"active:bool!", "bool", "INTEGER", true},
		{"active:bool", "*bool", "INTEGER", false},
		{"meta:json!", "json.RawMessage", "TEXT", true},
		{"ts:time!", "time.Time", "DATETIME", true},
		{"ts:time", "*time.Time", "DATETIME", false},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			fields, err := ParseFields([]string{c.input})
			if err != nil {
				t.Fatalf("ParseFields(%q): %v", c.input, err)
			}
			f := fields[0]
			if f.GoType != c.goType {
				t.Errorf("GoType: got %q want %q", f.GoType, c.goType)
			}
			if f.SQLType != c.sqlType {
				t.Errorf("SQLType: got %q want %q", f.SQLType, c.sqlType)
			}
			if f.NotNull != c.notNull {
				t.Errorf("NotNull: got %v want %v", f.NotNull, c.notNull)
			}
		})
	}
}

func TestParseFields_RejectsAutoManaged(t *testing.T) {
	for _, name := range []string{"id", "created_at", "updated_at"} {
		_, err := ParseFields([]string{name + ":string!"})
		if err == nil {
			t.Errorf("expected error for auto-managed field %q", name)
		}
	}
}

func TestParseFields_RejectsDuplicate(t *testing.T) {
	_, err := ParseFields([]string{"name:string!", "name:string!"})
	if err == nil {
		t.Error("expected error for duplicate field")
	}
}

func TestParseFields_RejectsUnknownType(t *testing.T) {
	_, err := ParseFields([]string{"field:uuid!"})
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestParseFields_VarcharModifier(t *testing.T) {
	fields, err := ParseFields([]string{"name:string{92}!"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields[0].SQLType != "VARCHAR(92)" {
		t.Errorf("SQLType: got %q want VARCHAR(92)", fields[0].SQLType)
	}
}

func TestParseFields_CascadeRequiresFK(t *testing.T) {
	_, err := ParseFields([]string{"user_id:string{cascade}!"})
	if err == nil {
		t.Error("expected error: cascade without fk")
	}
}

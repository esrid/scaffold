package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- Field name validation ----

func TestParseFields_RejectsReservedSQLWords(t *testing.T) {
	// "select" is also a Go keyword and is rejected by that check first — any
	// rejection is fine; the message assertion applies to SQL-only keywords.
	for _, name := range []string{"order", "user", "to", "from", "group", "select", "limit", "desc", "index"} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseFields([]string{name + ":string"})
			if err == nil {
				t.Fatalf("expected error for reserved SQL word %q", name)
			}
			if !goKeywords[name] && !strings.Contains(err.Error(), "reserved SQL keyword") {
				t.Errorf("error should mention reserved SQL keyword, got: %v", err)
			}
		})
	}
}

func TestParseFields_RejectsAutoManagedVariants(t *testing.T) {
	// Spellings that differ from id/created_at/updated_at but collide with the
	// auto-managed Go struct fields (ID, CreatedAt, UpdatedAt) or, on SQLite's
	// case-insensitive columns, with the auto-managed columns themselves.
	for _, name := range []string{"Id", "ID", "i_d", "createdAt", "CreatedAt", "createdat", "updatedAt", "Updated_At"} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFields([]string{name + ":string"}); err == nil {
				t.Fatalf("expected error for auto-managed collision %q", name)
			}
		})
	}
}

func TestParseFields_RejectsInvalidFKTarget(t *testing.T) {
	for _, arg := range []string{
		"author_id:string,fk=bad-name",
		"author_id:string,fk=",
		"author_id:string,fk=user", // reserved word as table
	} {
		t.Run(arg, func(t *testing.T) {
			if _, err := ParseFields([]string{arg}); err == nil {
				t.Fatalf("expected error for %q", arg)
			}
		})
	}
}

func TestParseFields_CheckExpressionWithCommasInParens(t *testing.T) {
	fields, err := ParseFields([]string{"status:string,check=status IN ('a','b')!"})
	if err != nil {
		t.Fatalf("ParseFields: %v", err)
	}
	f := fields[0]
	want := "check=status IN ('a','b')"
	found := false
	for _, m := range f.Modifiers {
		if m == want {
			found = true
		}
	}
	if !found {
		t.Errorf("modifiers = %v, want one equal to %q", f.Modifiers, want)
	}
	if !f.NotNull {
		t.Errorf("expected NOT NULL from ! suffix")
	}
}

func TestBuildModel_RejectsReservedTableName(t *testing.T) {
	manifest := &Manifest{Models: map[string]ManifestModel{}}
	fields, _ := ParseFields([]string{"name:string!"})
	if _, err := BuildModel("Product", fields, nil, manifest, "order", false); err == nil {
		t.Fatal("expected error for reserved --table-name")
	}
	if _, err := BuildModel("Product", fields, nil, manifest, "bad-table", false); err == nil {
		t.Fatal("expected error for invalid --table-name characters")
	}
}

// ---- Re-gen metadata and change detection ----

func TestBuildModel_PreservesScaffoldedAt(t *testing.T) {
	manifest := &Manifest{Models: map[string]ManifestModel{}}
	fields, _ := ParseFields([]string{"name:string!"})

	m1, err := BuildModel("Product", fields, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	entry1 := m1.ManifestEntry()
	if entry1.ScaffoldedAt.IsZero() {
		t.Fatal("first gen: ScaffoldedAt should be set")
	}
	manifest.Models["Product"] = entry1

	time.Sleep(10 * time.Millisecond)
	m2, err := BuildModel("Product", fields, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel re-gen: %v", err)
	}
	entry2 := m2.ManifestEntry()
	if !entry2.ScaffoldedAt.Equal(entry1.ScaffoldedAt) {
		t.Errorf("re-gen must preserve ScaffoldedAt: got %v want %v", entry2.ScaffoldedAt, entry1.ScaffoldedAt)
	}
	if !entry2.UpdatedAt.After(entry1.UpdatedAt) {
		t.Errorf("re-gen should bump UpdatedAt")
	}
}

func TestBuildModel_DetectsChangedFieldDefinitions(t *testing.T) {
	manifest := &Manifest{Models: map[string]ManifestModel{}}
	orig, _ := ParseFields([]string{"name:string!", "price:float!"})
	m1, err := BuildModel("Product", orig, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	manifest.Models["Product"] = m1.ManifestEntry()

	// Same field name, different definition (VARCHAR size + nullability).
	changed, _ := ParseFields([]string{"name:string,255"})
	m2, err := BuildModel("Product", changed, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel re-gen: %v", err)
	}
	if len(m2.ChangedFields) != 1 || m2.ChangedFields[0] != "name" {
		t.Errorf("ChangedFields = %v, want [name]", m2.ChangedFields)
	}

	// Re-gen with the identical definition must report no changes.
	same, _ := ParseFields([]string{"name:string!"})
	m3, err := BuildModel("Product", same, nil, manifest, "", false)
	if err != nil {
		t.Fatalf("BuildModel identical re-gen: %v", err)
	}
	if len(m3.ChangedFields) != 0 {
		t.Errorf("identical re-gen: ChangedFields = %v, want none", m3.ChangedFields)
	}
}

func TestModelFromEntry_UsesDiskAwareMigrationVersion(t *testing.T) {
	root := t.TempDir()
	migDir := filepath.Join(root, migrationsDir)
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migDir, "00007_create_users.sql"), []byte("-- x"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := &Manifest{Models: map[string]ManifestModel{
		"Product": {TableName: "products", MigrationVersion: 2},
	}}
	manifest.migrationFloor = highestMigrationOnDisk(root)

	model, err := ModelFromEntry("Product", manifest.Models["Product"], manifest)
	if err != nil {
		t.Fatalf("ModelFromEntry: %v", err)
	}
	// entry.MigrationVersion+1 would be 3 — but 00007 exists on disk, so the
	// DROP migration must be numbered 8 to avoid a goose duplicate-version panic.
	if model.MigrationVersion != 8 {
		t.Errorf("MigrationVersion = %d, want 8 (disk floor 7 + 1)", model.MigrationVersion)
	}
}

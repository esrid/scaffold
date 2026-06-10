package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHighestMigrationOnDisk(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, migrationsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"00001_initial.sql",
		"00002_create_steps.sql",
		"00004_drop_categories.sql", // gap is fine
		"notes.txt",                 // ignored (not .sql)
		"bad_x.sql",                 // ignored (no numeric prefix)
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("-- x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := highestMigrationOnDisk(root); got != 4 {
		t.Fatalf("highestMigrationOnDisk = %d, want 4", got)
	}
	if got := highestMigrationOnDisk(t.TempDir()); got != 0 {
		t.Fatalf("highestMigrationOnDisk(empty) = %d, want 0", got)
	}
}

func TestNextMigrationVersion_RespectsDiskFloor(t *testing.T) {
	// Disk has migrations beyond the manifest counter → next must clear the floor.
	m := &Manifest{DB: "sqlite", Models: map[string]ManifestModel{}, migrationFloor: 4}
	if got := NextMigrationVersion(m); got != 5 {
		t.Fatalf("NextMigrationVersion = %d, want 5 (floor+1)", got)
	}

	// Manifest counter higher than the floor still wins.
	m2 := &Manifest{
		DB:             "sqlite",
		Models:         map[string]ManifestModel{"X": {MigrationVersion: 7}},
		migrationFloor: 4,
	}
	if got := NextMigrationVersion(m2); got != 8 {
		t.Fatalf("NextMigrationVersion = %d, want 8", got)
	}
}

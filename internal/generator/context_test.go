package generator

import (
	"strings"
	"testing"
)

func TestBuildSQLModifiers_check(t *testing.T) {
	for _, db := range []string{"sqlite", "postgres"} {
		got := buildSQLModifiers([]string{"check=age>0"}, db)
		if !strings.Contains(got, "CHECK (age>0)") {
			t.Errorf("db=%s: expected CHECK (age>0), got %q", db, got)
		}
	}
}

func TestBuildSQLModifiers_cascade(t *testing.T) {
	mods := []string{"fk=users", "cascade"}
	for _, db := range []string{"sqlite", "postgres"} {
		got := buildSQLModifiers(mods, db)
		if !strings.Contains(got, "ON DELETE CASCADE") {
			t.Errorf("db=%s: expected ON DELETE CASCADE, got %q", db, got)
		}
	}
}

func TestBuildSQLModifiers_setnull(t *testing.T) {
	mods := []string{"fk=users", "setnull"}
	for _, db := range []string{"sqlite", "postgres"} {
		got := buildSQLModifiers(mods, db)
		if !strings.Contains(got, "ON DELETE SET NULL") {
			t.Errorf("db=%s: expected ON DELETE SET NULL, got %q", db, got)
		}
	}
}

func TestBuildSQLModifiers_fk_default_sqlite(t *testing.T) {
	got := buildSQLModifiers([]string{"fk=users"}, "sqlite")
	if strings.Contains(got, "ON DELETE") {
		t.Errorf("sqlite fk without cascade/setnull should have no ON DELETE clause, got %q", got)
	}
	if !strings.Contains(got, "REFERENCES users(id)") {
		t.Errorf("expected REFERENCES users(id), got %q", got)
	}
}

func TestBuildSQLModifiers_fk_default_postgres(t *testing.T) {
	got := buildSQLModifiers([]string{"fk=users"}, "postgres")
	if !strings.Contains(got, "ON DELETE RESTRICT") {
		t.Errorf("postgres fk without cascade/setnull should default to RESTRICT, got %q", got)
	}
}

func TestBuildSQLModifiers_check_and_unique(t *testing.T) {
	got := buildSQLModifiers([]string{"check=score>=0", "unique"}, "sqlite")
	if !strings.Contains(got, "UNIQUE") {
		t.Errorf("expected UNIQUE, got %q", got)
	}
	if !strings.Contains(got, "CHECK (score>=0)") {
		t.Errorf("expected CHECK (score>=0), got %q", got)
	}
}

func TestBuildSQLModifiers_cascade_not_duplicated(t *testing.T) {
	got := buildSQLModifiers([]string{"fk=users", "cascade"}, "sqlite")
	if strings.Count(got, "CASCADE") != 1 {
		t.Errorf("CASCADE should appear exactly once, got %q", got)
	}
}

package generator

import (
	"strings"
	"testing"
)

func TestBuildSQLModifiers_check(t *testing.T) {
	t.Parallel()
	cases := []struct {
		db string
	}{
		{"sqlite"},
		{"postgres"},
	}
	for _, tc := range cases {
		t.Run(tc.db, func(t *testing.T) {
			got := buildSQLModifiers([]string{"check=age>0"}, tc.db)
			if !strings.Contains(got, "CHECK (age>0)") {
				t.Errorf("expected CHECK (age>0), got %q", got)
			}
		})
	}
}

func TestBuildSQLModifiers_cascade(t *testing.T) {
	t.Parallel()
	mods := []string{"fk=users", "cascade"}
	cases := []struct {
		db string
	}{
		{"sqlite"},
		{"postgres"},
	}
	for _, tc := range cases {
		t.Run(tc.db, func(t *testing.T) {
			got := buildSQLModifiers(mods, tc.db)
			if !strings.Contains(got, "ON DELETE CASCADE") {
				t.Errorf("expected ON DELETE CASCADE, got %q", got)
			}
		})
	}
}

func TestBuildSQLModifiers_setnull(t *testing.T) {
	t.Parallel()
	mods := []string{"fk=users", "setnull"}
	cases := []struct {
		db string
	}{
		{"sqlite"},
		{"postgres"},
	}
	for _, tc := range cases {
		t.Run(tc.db, func(t *testing.T) {
			got := buildSQLModifiers(mods, tc.db)
			if !strings.Contains(got, "ON DELETE SET NULL") {
				t.Errorf("expected ON DELETE SET NULL, got %q", got)
			}
		})
	}
}

func TestBuildSQLModifiers_fk_default_sqlite(t *testing.T) {
	t.Parallel()
	got := buildSQLModifiers([]string{"fk=users"}, "sqlite")
	if strings.Contains(got, "ON DELETE") {
		t.Errorf("sqlite fk without cascade/setnull should have no ON DELETE clause, got %q", got)
	}
	if !strings.Contains(got, "REFERENCES users(id)") {
		t.Errorf("expected REFERENCES users(id), got %q", got)
	}
}

func TestBuildSQLModifiers_fk_default_postgres(t *testing.T) {
	t.Parallel()
	got := buildSQLModifiers([]string{"fk=users"}, "postgres")
	if !strings.Contains(got, "ON DELETE RESTRICT") {
		t.Errorf("postgres fk without cascade/setnull should default to RESTRICT, got %q", got)
	}
}

func TestBuildSQLModifiers_check_and_unique(t *testing.T) {
	t.Parallel()
	got := buildSQLModifiers([]string{"check=score>=0", "unique"}, "sqlite")
	if !strings.Contains(got, "UNIQUE") {
		t.Errorf("expected UNIQUE, got %q", got)
	}
	if !strings.Contains(got, "CHECK (score>=0)") {
		t.Errorf("expected CHECK (score>=0), got %q", got)
	}
}

func TestBuildSQLModifiers_cascade_not_duplicated(t *testing.T) {
	t.Parallel()
	got := buildSQLModifiers([]string{"fk=users", "cascade"}, "sqlite")
	if strings.Count(got, "CASCADE") != 1 {
		t.Errorf("CASCADE should appear exactly once, got %q", got)
	}
}

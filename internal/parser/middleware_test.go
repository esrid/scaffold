package parser

import (
	"reflect"
	"testing"
)

func TestParseMiddlewareFlags(t *testing.T) {
	got, err := ParseMiddlewareFlags([]string{"create:RequireAuth,RateLimit", "delete:RequireAuth"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"create": {"RequireAuth", "RateLimit"},
		"delete": {"RequireAuth"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseMiddlewareFlags_UnknownOp(t *testing.T) {
	if _, err := ParseMiddlewareFlags([]string{"frobnicate:RequireAuth"}); err == nil {
		t.Error("expected error on unknown op")
	}
}

func TestParseMiddlewareFlags_DuplicateOpAcrossFlags(t *testing.T) {
	if _, err := ParseMiddlewareFlags([]string{"create:A", "create:B"}); err == nil {
		t.Error("expected error: op specified twice — combine as create:A,B")
	}
}

func TestParseMiddlewareFlags_InvalidIdentifier(t *testing.T) {
	if _, err := ParseMiddlewareFlags([]string{"create:not-an-identifier"}); err == nil {
		t.Error("expected error on invalid Go identifier")
	}
	if _, err := ParseMiddlewareFlags([]string{"create:has space"}); err == nil {
		t.Error("expected error on middleware name with a space")
	}
}

func TestParseMiddlewareFlags_MissingColon(t *testing.T) {
	if _, err := ParseMiddlewareFlags([]string{"RequireAuth"}); err == nil {
		t.Error("expected error: missing op: prefix")
	}
}

func TestParseMiddlewareFlags_AllExclusiveWithSpecific(t *testing.T) {
	if _, err := ParseMiddlewareFlags([]string{"all:RequestLogger", "create:RequireAuth"}); err == nil {
		t.Error("expected error: 'all' cannot combine with a specific op")
	}
}

func TestExpandAllMiddleware(t *testing.T) {
	got := ExpandAllMiddleware(map[string][]string{"all": {"RequestLogger"}})
	for _, op := range opNames {
		if !reflect.DeepEqual(got[op], []string{"RequestLogger"}) {
			t.Errorf("op %q = %+v, want [RequestLogger]", op, got[op])
		}
	}
}

func TestExpandAllMiddleware_NoAllKey_PassesThrough(t *testing.T) {
	in := map[string][]string{"create": {"RequireAuth"}}
	got := ExpandAllMiddleware(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("got %+v, want unchanged %+v", got, in)
	}
}

func TestMergeMiddleware_StickyAcrossRegen(t *testing.T) {
	prev := map[string][]string{"create": {"RequireAuth"}, "delete": {"RequireAuth"}}
	// A later `scaffold gen` touching a different op shouldn't drop
	// previously configured ops it doesn't mention.
	got := MergeMiddleware(prev, map[string][]string{"update": {"RateLimit"}})
	want := map[string][]string{
		"create": {"RequireAuth"},
		"delete": {"RequireAuth"},
		"update": {"RateLimit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMergeMiddleware_ReplacesNamedOp(t *testing.T) {
	prev := map[string][]string{"create": {"RequireAuth"}}
	got := MergeMiddleware(prev, map[string][]string{"create": {"RateLimit"}})
	want := map[string][]string{"create": {"RateLimit"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRemoveMiddlewareOps(t *testing.T) {
	mw := map[string][]string{"create": {"RequireAuth"}, "delete": {"RequireAuth"}}
	got, err := RemoveMiddlewareOps(mw, []string{"create"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"delete": {"RequireAuth"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRemoveMiddlewareOps_All(t *testing.T) {
	mw := map[string][]string{"create": {"RequireAuth"}, "delete": {"RequireAuth"}}
	got, err := RemoveMiddlewareOps(mw, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

func TestRemoveMiddlewareOps_UnknownOp(t *testing.T) {
	if _, err := RemoveMiddlewareOps(map[string][]string{}, []string{"frobnicate"}); err == nil {
		t.Error("expected error on unknown op")
	}
}

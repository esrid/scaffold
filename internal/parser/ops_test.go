package parser

import "testing"

func TestResolveOps(t *testing.T) {
	if got, err := ResolveOps(nil, nil); err != nil || got != AllOps() {
		t.Fatalf("default = %+v, %v; want AllOps()", got, err)
	}

	got, err := ResolveOps(nil, []string{"create", "delete"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Create || got.Delete || !got.List || !got.Read || !got.Update {
		t.Fatalf("--skip create,delete = %+v", got)
	}

	got, err = ResolveOps([]string{"list", "read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.List || !got.Read || got.Create || got.Update || got.Delete {
		t.Fatalf("--only list,read = %+v", got)
	}

	if _, err := ResolveOps([]string{"list"}, []string{"create"}); err == nil {
		t.Error("expected error: --only and --skip are mutually exclusive")
	}
	if _, err := ResolveOps(nil, []string{"frobnicate"}); err == nil {
		t.Error("expected error on unknown op")
	}
}

func TestOpsSkippedRoundTrip(t *testing.T) {
	o := AllOps()
	o.Create = false
	o.Delete = false

	skipped := o.Skipped()
	if len(skipped) != 2 {
		t.Fatalf("Skipped() = %v, want 2 entries", skipped)
	}
	if got := OpsFromSkipped(skipped); got != o {
		t.Fatalf("round trip: %+v -> %v -> %+v", o, skipped, got)
	}

	if len(AllOps().Skipped()) != 0 {
		t.Error("AllOps().Skipped() should be empty")
	}
}

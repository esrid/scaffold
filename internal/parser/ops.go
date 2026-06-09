package parser

import (
	"fmt"
	"strings"
)

// Ops is the set of CRUD operations a model exposes. The zero value enables
// nothing; AllOps() is the default (full CRUD). It is rendered into handler
// route registration and SSR view affordances.
type Ops struct {
	List   bool
	Read   bool
	Create bool
	Update bool
	Delete bool
}

// opNames is the canonical, user-facing op vocabulary for --only / --skip.
var opNames = []string{"list", "read", "create", "update", "delete"}

// AllOps returns the default: every operation enabled.
func AllOps() Ops {
	return Ops{List: true, Read: true, Create: true, Update: true, Delete: true}
}

func setOp(o *Ops, name string, v bool) bool {
	switch name {
	case "list":
		o.List = v
	case "read":
		o.Read = v
	case "create":
		o.Create = v
	case "update":
		o.Update = v
	case "delete":
		o.Delete = v
	default:
		return false
	}
	return true
}

// Skipped returns the disabled op names (stable order), for manifest persistence.
func (o Ops) Skipped() []string {
	enabled := map[string]bool{
		"list": o.List, "read": o.Read, "create": o.Create,
		"update": o.Update, "delete": o.Delete,
	}
	var s []string
	for _, name := range opNames {
		if !enabled[name] {
			s = append(s, name)
		}
	}
	return s
}

// OpsFromSkipped builds Ops from a list of disabled op names (manifest form).
func OpsFromSkipped(skipped []string) Ops {
	o := AllOps()
	for _, name := range skipped {
		setOp(&o, strings.ToLower(strings.TrimSpace(name)), false)
	}
	return o
}

// ResolveOps computes the enabled ops from the --only and --skip flags, which
// are mutually exclusive. Empty for both means full CRUD. Unknown names error.
func ResolveOps(only, skip []string) (Ops, error) {
	if len(only) > 0 && len(skip) > 0 {
		return Ops{}, fmt.Errorf("--only and --skip are mutually exclusive")
	}

	if len(only) > 0 {
		o := Ops{}
		for _, name := range only {
			name = strings.ToLower(strings.TrimSpace(name))
			if !setOp(&o, name, true) {
				return Ops{}, fmt.Errorf("unknown op %q in --only (valid: %s)", name, strings.Join(opNames, ", "))
			}
		}
		return o, nil
	}

	o := AllOps()
	for _, name := range skip {
		name = strings.ToLower(strings.TrimSpace(name))
		if !setOp(&o, name, false) {
			return Ops{}, fmt.Errorf("unknown op %q in --skip (valid: %s)", name, strings.Join(opNames, ", "))
		}
	}
	return o, nil
}

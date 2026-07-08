package parser

import (
	"fmt"
	"regexp"
	"strings"
)

var middlewareIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseMiddlewareFlags parses --middleware values like
// "create:RequireAuth,RateLimit" into an op -> ordered middleware-name-list
// map. "all" is a reserved op meaning every real op; it's mutually
// exclusive with naming a specific op in the same command, same rule as
// --only/--skip.
func ParseMiddlewareFlags(specs []string) (map[string][]string, error) {
	result := map[string][]string{}
	sawAll, sawSpecific := false, false

	for _, spec := range specs {
		op, names, ok := strings.Cut(spec, ":")
		if !ok || strings.TrimSpace(names) == "" {
			return nil, fmt.Errorf("invalid --middleware %q: expected op:Func1,Func2", spec)
		}
		op = strings.ToLower(strings.TrimSpace(op))

		if op != "all" && !isKnownOp(op) {
			return nil, fmt.Errorf("unknown op %q in --middleware (valid: %s, all)", op, strings.Join(opNames, ", "))
		}
		if _, exists := result[op]; exists {
			return nil, fmt.Errorf("op %q specified in --middleware more than once — combine as %s:Func1,Func2", op, op)
		}
		if op == "all" {
			sawAll = true
		} else {
			sawSpecific = true
		}

		var mws []string
		for _, name := range strings.Split(names, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !middlewareIdentRe.MatchString(name) {
				return nil, fmt.Errorf("invalid middleware name %q in --middleware %q: must be a valid Go identifier", name, spec)
			}
			mws = append(mws, name)
		}
		if len(mws) == 0 {
			return nil, fmt.Errorf("invalid --middleware %q: no middleware names given", spec)
		}
		result[op] = mws
	}

	if sawAll && sawSpecific {
		return nil, fmt.Errorf("--middleware all:... cannot be combined with a specific op in the same command — run them separately")
	}
	return result, nil
}

func isKnownOp(name string) bool {
	for _, n := range opNames {
		if n == name {
			return true
		}
	}
	return false
}

// ExpandAllMiddleware turns {"all": [...]} into an explicit entry for
// every real op, so downstream template rendering never has to special-case
// "all".
func ExpandAllMiddleware(mw map[string][]string) map[string][]string {
	all, ok := mw["all"]
	if !ok {
		return mw
	}
	out := make(map[string][]string, len(opNames))
	for _, op := range opNames {
		out[op] = all
	}
	return out
}

// MergeMiddleware is sticky like field merging: ops named in newMW replace
// their previous entry, ops not mentioned keep whatever they had.
func MergeMiddleware(prev, newMW map[string][]string) map[string][]string {
	if len(newMW) == 0 {
		return prev
	}
	out := make(map[string][]string, len(prev)+len(newMW))
	for k, v := range prev {
		out[k] = v
	}
	for k, v := range newMW {
		out[k] = v
	}
	return out
}

// RemoveMiddlewareOps deletes the named ops (or "all" for every op) from
// mw, for --remove-middleware.
func RemoveMiddlewareOps(mw map[string][]string, remove []string) (map[string][]string, error) {
	if len(remove) == 0 {
		return mw, nil
	}
	out := make(map[string][]string, len(mw))
	for k, v := range mw {
		out[k] = v
	}
	for _, name := range remove {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "all" {
			return map[string][]string{}, nil
		}
		if !isKnownOp(name) {
			return nil, fmt.Errorf("unknown op %q in --remove-middleware (valid: %s, all)", name, strings.Join(opNames, ", "))
		}
		delete(out, name)
	}
	return out, nil
}

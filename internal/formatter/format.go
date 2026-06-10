package formatter

import (
	"fmt"

	"golang.org/x/tools/imports"
)

// GoSource formats Go source code using goimports rules.
// Returns an error with the raw source if formatting fails (template bug).
func GoSource(src string) ([]byte, error) {
	out, err := imports.Process("gen.go", []byte(src), nil)
	if err != nil {
		return nil, fmt.Errorf("generated invalid Go source (template bug):\n---\n%s\n---\n%w", src, err)
	}
	return out, nil
}

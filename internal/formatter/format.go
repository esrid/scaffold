package formatter

import (
	"fmt"
	"go/format"
)

// GoSource formats Go source code using gofmt rules.
// Returns an error with the raw source if formatting fails (template bug).
func GoSource(src string) ([]byte, error) {
	out, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("generated invalid Go source (template bug):\n---\n%s\n---\n%w", src, err)
	}
	return out, nil
}

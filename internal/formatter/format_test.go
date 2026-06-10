package formatter_test

import (
	"strings"
	"testing"

	"github.com/esrid/scaffold/internal/formatter"
)

func TestFormatter_GoSourceGroupsImports(t *testing.T) {
	src := `package main

import (
	"fmt"
	"github.com/esrid/scaffold/internal/parser"
	"os"
)

func main() {
	fmt.Println(os.Args)
	_ = parser.Field{}
}
`

	formatted, err := formatter.GoSource(src)
	if err != nil {
		t.Fatalf("GoSource failed: %v", err)
	}

	res := string(formatted)

	// goimports should group standard library imports ("fmt", "os") together,
	// and separate "github.com/esrid/scaffold/internal/parser" with a blank line.
	expected := `import (
	"fmt"
	"os"

	"github.com/esrid/scaffold/internal/parser"
)`

	if !strings.Contains(res, expected) {
		t.Errorf("expected formatted code to contain grouped imports:\n%s\n\nGot:\n%s", expected, res)
	}
}

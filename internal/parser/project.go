package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindProjectRoot walks up from cwd looking for go.mod and returns
// the project root path and the Go module path declared in go.mod.
func FindProjectRoot() (root, modulePath string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}

	for {
		gomod := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(gomod)
		if err == nil {
			modulePath = parseModulePath(string(data))
			return dir, modulePath, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("no go.mod found — run scaffold from your project root")
		}
		dir = parent
	}
}

func parseModulePath(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

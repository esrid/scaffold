package boilerplate

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed static sqlite postgres
var files embed.FS

// BoilerplateData is passed to all .tmpl files during rendering.
type BoilerplateData struct {
	Module  string // e.g. "github.com/user/myapp"
	DB      string // "sqlite" | "postgres"
	AppName string // e.g. "myapp"
}

// Generate writes the full boilerplate for the chosen DB into dir.
// It walks the static/ tree first, then the db-specific tree (sqlite/ or postgres/).
func Generate(dir, module, db string) error {
	parts := strings.Split(module, "/")
	appName := parts[len(parts)-1]

	data := BoilerplateData{
		Module:  module,
		DB:      db,
		AppName: appName,
	}

	for _, src := range []string{"static", db} {
		if err := walkAndWrite(dir, src, data); err != nil {
			return fmt.Errorf("boilerplate %s: %w", src, err)
		}
	}
	return nil
}

func walkAndWrite(dir, srcPrefix string, data BoilerplateData) error {
	return fs.WalkDir(files, srcPrefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Strip top-level prefix: "static/internal/app/app.go.tmpl" → "internal/app/app.go.tmpl"
		rel, err := filepath.Rel(srcPrefix, path)
		if err != nil {
			return err
		}

		// Determine destination — strip .tmpl suffix
		dest := strings.TrimSuffix(rel, ".tmpl")
		destAbs := filepath.Join(dir, dest)

		if err := os.MkdirAll(filepath.Dir(destAbs), 0755); err != nil {
			return err
		}

		content, err := files.ReadFile(path)
		if err != nil {
			return err
		}

		// Render templates, copy everything else verbatim
		if strings.HasSuffix(path, ".tmpl") {
			rendered, err := renderTemplate(path, string(content), data)
			if err != nil {
				return err
			}
			return os.WriteFile(destAbs, []byte(rendered), 0644)
		}

		return os.WriteFile(destAbs, content, 0644)
	})
}

func renderTemplate(name, tmplStr string, data BoilerplateData) (string, error) {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return buf.String(), nil
}

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

// The all: prefix is required so dotfiles (e.g. static/.env.example.tmpl) are
// embedded — a bare //go:embed pattern silently skips names starting with "." or "_".
//
//go:embed all:static all:sqlite all:postgres all:grpc all:rest all:ssr all:ssr-templ all:ssr-html
var files embed.FS

// BoilerplateData is passed to all .tmpl files during rendering.
type BoilerplateData struct {
	Module    string // e.g. "github.com/user/myapp"
	DB        string // "sqlite" | "postgres"
	AppName   string // e.g. "myapp"
	GRPC      bool   // true if gRPC support is enabled
	APIMode   string // "rest" | "ssr" | "grpc"
	SSREngine string // "templ" | "html" — SSR mode only
	IsSSR     bool
	IsREST    bool
	IsGRPC    bool
	IsHTML    bool // true when IsSSR && SSREngine == "html"
	IsTempl   bool // true when IsSSR && SSREngine == "templ"
}

// Generate writes the full boilerplate for the chosen DB, API mode, and (SSR
// only) view engine into dir.
// Walk order: static/ → {db}/ → {api_mode}/ (→ grpc/ if gRPC) (→ ssr-templ/
// or ssr-html/ if ssr)
func Generate(dir, module, db, apiMode, ssrEngine string) error {
	parts := strings.Split(module, "/")
	appName := parts[len(parts)-1]

	if ssrEngine == "" {
		ssrEngine = "templ"
	}

	data := BoilerplateData{
		Module:    module,
		DB:        db,
		AppName:   appName,
		GRPC:      apiMode == "grpc",
		APIMode:   apiMode,
		SSREngine: ssrEngine,
		IsSSR:     apiMode == "ssr",
		IsREST:    apiMode == "rest",
		IsGRPC:    apiMode == "grpc",
		IsHTML:    apiMode == "ssr" && ssrEngine == "html",
		IsTempl:   apiMode == "ssr" && ssrEngine != "html",
	}

	sources := []string{"static", db}
	switch apiMode {
	case "ssr":
		sources = append(sources, "ssr")
		if ssrEngine == "html" {
			sources = append(sources, "ssr-html")
		} else {
			sources = append(sources, "ssr-templ")
		}
	case "grpc":
		sources = append(sources, "rest", "grpc")
	default: // "rest"
		sources = append(sources, "rest")
	}

	for _, src := range sources {
		if err := walkAndWrite(dir, src, data); err != nil {
			return fmt.Errorf("boilerplate %s: %w", src, err)
		}
	}

	if apiMode == "ssr" {
		// {db}/internal/app/registry.go.tmpl is REST-shaped (sql.DB/pgxpool.Pool
		// wiring for a standalone registry.go) and gets copied unconditionally
		// by the db source above. SSR mode's Registry lives inside the merged
		// app.go instead (see ssrTypeDefsTmpl etc in the generator package) —
		// drop the file the db source left behind so it can't redeclare
		// Stores/Services/Handlers/Registry/NewRegistry a second time.
		if err := os.Remove(filepath.Join(dir, "internal", "app", "registry.go")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing REST-shaped registry.go for SSR mode: %w", err)
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
	t := template.New(name)
	// README embeds Go/HTML template examples that use {{ }}, so use alternate delimiters.
	if strings.HasSuffix(name, "README.md.tmpl") {
		t = t.Delims("[[", "]]")
	}
	tmpl, err := t.Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return buf.String(), nil
}

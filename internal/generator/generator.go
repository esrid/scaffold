package generator

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/esrid/scaffold/internal/formatter"
	"github.com/esrid/scaffold/internal/parser"
)

// Generator writes scaffold files into a target project.
type Generator struct {
	root       string
	modulePath string
	manifest   *parser.Manifest
	dryRun     bool
}

// New creates a Generator targeting the given project root.
func New(root, modulePath string, manifest *parser.Manifest, dryRun bool) *Generator {
	return &Generator{
		root:       root,
		modulePath: modulePath,
		manifest:   manifest,
		dryRun:     dryRun,
	}
}

func (g *Generator) isPostgres() bool {
	return g.manifest.IsPostgres()
}

// Result holds the list of file operations performed.
type Result struct {
	Created     []string
	Overwritten []string
	Unchanged   []string
	Deleted     []string
}

// Print writes a human-readable summary to w.
func (r *Result) Print(w io.Writer) {
	if len(r.Created) > 0 {
		fmt.Fprintln(w, "\nCreated:")
		for _, f := range r.Created {
			fmt.Fprintf(w, "  + %s\n", f)
		}
	}
	if len(r.Overwritten) > 0 {
		fmt.Fprintln(w, "\nOverwritten (generated):")
		for _, f := range r.Overwritten {
			fmt.Fprintf(w, "  ~ %s\n", f)
		}
	}
	if len(r.Unchanged) > 0 {
		fmt.Fprintln(w, "\nUnchanged (user code):")
		for _, f := range r.Unchanged {
			fmt.Fprintf(w, "  = %s\n", f)
		}
	}
	if len(r.Deleted) > 0 {
		fmt.Fprintln(w, "\nDeleted:")
		for _, f := range r.Deleted {
			fmt.Fprintf(w, "  - %s\n", f)
		}
	}
	fmt.Fprintln(w, "\nNext steps:")
	fmt.Fprintln(w, "  1. Add validation logic in domain file → Validate()")
	fmt.Fprintln(w, "  2. Add custom queries in store file (below generated section)")
	fmt.Fprintln(w, "  3. Run: go build ./...")
}

// Scaffold generates or updates all files for the given model.
func (g *Generator) Scaffold(model *parser.Model) (*Result, error) {
	res := &Result{}

	if model.IsNew {
		if err := g.scaffoldCreate(model, res); err != nil {
			return nil, err
		}
	} else {
		if err := g.scaffoldUpdate(model, res); err != nil {
			return nil, err
		}
	}

	// registry.go is always regenerated
	if err := g.writeRegistry(res); err != nil {
		return nil, err
	}

	return res, nil
}

// Destroy removes all scaffold files for the given model.
func (g *Generator) Destroy(model *parser.Model) (*Result, error) {
	res := &Result{}

	files := []string{
		filepath.Join("internal", "core", "domain", model.Snake()+".go"),
		filepath.Join("internal", "core", "ports", model.Snake()+".go"),
		filepath.Join("internal", "core", "services", model.Snake()+"_service_gen.go"),
		filepath.Join("internal", "core", "services", model.Snake()+"_service.go"),
		filepath.Join("internal", "adapters", "store", model.Snake()+"_store_gen.go"),
		filepath.Join("internal", "adapters", "store", model.Snake()+"_store.go"),
	}

	for _, rel := range files {
		abs := filepath.Join(g.root, rel)
		if !g.dryRun {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("destroy: remove %s: %w", rel, err)
			}
		}
		res.Deleted = append(res.Deleted, rel)
	}

	// Remove table block from schema.sql
	if err := g.removeSchemaBlock(model, res); err != nil {
		return nil, err
	}

	// Generate DROP TABLE migration
	if err := g.writeDropMigration(model, res); err != nil {
		return nil, err
	}

	// Remove model from manifest before regenerating registry so it's excluded.
	delete(g.manifest.Models, model.Name)

	// Regenerate registry without this model
	if err := g.writeRegistry(res); err != nil {
		return nil, err
	}

	return res, nil
}

// ---- CREATE mode ----

func (g *Generator) scaffoldCreate(model *parser.Model, res *Result) error {
	// domain/{model}.go — full file with markers
	domainPath := filepath.Join("internal", "core", "domain", model.Snake()+".go")
	domainTmplToUse := domainTmpl
	if g.isPostgres() {
		domainTmplToUse = domainTmplPostgres
	}
	if err := g.writeGoFile(domainPath, domainTmplToUse, buildDomainCtx(model, g.modulePath, g.manifest.DB), true, res); err != nil {
		return err
	}

	// ports/{model}.go — created once
	portsPath := filepath.Join("internal", "core", "ports", model.Snake()+".go")
	if err := g.writeGoFileOnce(portsPath, portsTmpl, map[string]string{
		"ModulePath": g.modulePath, "Name": model.Name,
	}, res); err != nil {
		return err
	}

	// services/{model}_service_gen.go — always regenerated
	svcGenPath := filepath.Join("internal", "core", "services", model.Snake()+"_service_gen.go")
	if err := g.writeGoFile(svcGenPath, serviceGenTmpl, serviceGenCtx{
		Name:       model.Name,
		Lower:      model.Lower(),
		ModulePath: g.modulePath,
	}, true, res); err != nil {
		return err
	}

	// services/{model}_service.go — created once
	svcPath := filepath.Join("internal", "core", "services", model.Snake()+"_service.go")
	if err := g.writeGoFileOnce(svcPath, serviceUserTmpl, map[string]string{
		"Name": model.Name,
	}, res); err != nil {
		return err
	}

	// store/{model}_store_gen.go — always regenerated
	storeGenPath := filepath.Join("internal", "adapters", "store", model.Snake()+"_store_gen.go")
	storeCtx := buildStoreGenCtx(model, g.modulePath, g.manifest.DB)
	if err := g.writeStoreGen(storeGenPath, storeCtx, res); err != nil {
		return err
	}

	// store/{model}_store.go — created once
	storePath := filepath.Join("internal", "adapters", "store", model.Snake()+"_store.go")
	if err := g.writeGoFileOnce(storePath, storeUserTmpl, map[string]string{
		"Name": model.Name,
	}, res); err != nil {
		return err
	}

	// schema.sql — add table block
	if err := g.addSchemaBlock(model, res); err != nil {
		return err
	}

	// migration — create table
	if err := g.writeCreateMigration(model, res); err != nil {
		return err
	}

	return nil
}

// ---- UPDATE mode ----

func (g *Generator) scaffoldUpdate(model *parser.Model, res *Result) error {
	// domain/{model}.go — patch marker blocks only
	domainPath := filepath.Join("internal", "core", "domain", model.Snake()+".go")
	if err := g.patchDomainMarkers(domainPath, model, res); err != nil {
		return err
	}

	// ports/{model}.go — created once (do not overwrite)
	portsPath := filepath.Join("internal", "core", "ports", model.Snake()+".go")
	if err := g.writeGoFileOnce(portsPath, portsTmpl, map[string]string{
		"ModulePath": g.modulePath, "Name": model.Name,
	}, res); err != nil {
		return err
	}

	// services/{model}_service_gen.go — overwrite
	svcGenPath := filepath.Join("internal", "core", "services", model.Snake()+"_service_gen.go")
	if err := g.writeGoFile(svcGenPath, serviceGenTmpl, serviceGenCtx{
		Name:       model.Name,
		Lower:      model.Lower(),
		ModulePath: g.modulePath,
	}, true, res); err != nil {
		return err
	}

	// services/{model}_service.go — never touched
	svcPath := filepath.Join("internal", "core", "services", model.Snake()+"_service.go")
	res.Unchanged = append(res.Unchanged, svcPath)

	// store/{model}_store_gen.go — overwrite
	storeGenPath := filepath.Join("internal", "adapters", "store", model.Snake()+"_store_gen.go")
	storeCtx := buildStoreGenCtx(model, g.modulePath, g.manifest.DB)
	if err := g.writeStoreGen(storeGenPath, storeCtx, res); err != nil {
		return err
	}

	// store/{model}_store.go — never touched
	storePath := filepath.Join("internal", "adapters", "store", model.Snake()+"_store.go")
	res.Unchanged = append(res.Unchanged, storePath)

	// schema.sql — replace table block
	if err := g.replaceSchemaBlock(model, res); err != nil {
		return err
	}

	// migration — diff fields
	added, removed := model.DiffFields()
	if len(added) > 0 || len(removed) > 0 {
		if err := g.writeAlterMigration(model, added, removed, res); err != nil {
			return err
		}
	}

	return nil
}

// ---- File writing helpers ----

// writeGoFile renders a Go template, formats it, and writes to rel path.
// If overwrite is true it always writes; otherwise skips existing files.
func (g *Generator) writeGoFile(rel, tmplStr string, data any, overwrite bool, res *Result) error {
	abs := filepath.Join(g.root, rel)

	src, err := renderTemplate(tmplStr, data)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	formatted, err := formatter.GoSource(src)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	exists := fileExists(abs)
	if exists && !overwrite {
		res.Unchanged = append(res.Unchanged, rel)
		return nil
	}

	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}

	if exists {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
	return nil
}

// writeGoFileOnce writes only if the file does not already exist.
func (g *Generator) writeGoFileOnce(rel, tmplStr string, data any, res *Result) error {
	return g.writeGoFile(rel, tmplStr, data, false, res)
}

// writeStoreGen renders storeGenTmpl + scanFuncTmpl together.
func (g *Generator) writeStoreGen(rel string, ctx storeGenCtx, res *Result) error {
	abs := filepath.Join(g.root, rel)

	combined := scanFuncTmpl + storeGenTmpl
	if g.isPostgres() {
		combined = storeGenTmplPostgres
	}
	src, err := renderTemplate(combined, ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	formatted, err := formatter.GoSource(src)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	exists := fileExists(abs)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return err
		}
	}

	if exists {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
	return nil
}

// writeSQLFile renders a SQL template and writes it.
func (g *Generator) writeSQLFile(rel, tmplStr string, data any, res *Result) error {
	abs := filepath.Join(g.root, rel)

	content, err := renderTemplate(tmplStr, data)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			return err
		}
	}
	res.Created = append(res.Created, rel)
	return nil
}

// ---- Schema.sql patching ----

func (g *Generator) addSchemaBlock(model *parser.Model, res *Result) error {
	schemaPath := filepath.Join(g.root, "internal", "adapters", "store", "schema.sql")
	tmpl := schemaTmpl
	if g.isPostgres() {
		tmpl = schemaTmplPostgres
	}
	block, err := renderTemplate(tmpl, buildMigrationCtx(model, nil, nil, g.manifest.DB))
	if err != nil {
		return err
	}

	if !g.dryRun {
		existing, _ := os.ReadFile(schemaPath)
		updated := string(existing) + "\n" + block + "\n"
		if err := os.WriteFile(schemaPath, []byte(updated), 0644); err != nil {
			return fmt.Errorf("schema.sql: %w", err)
		}
	}
	res.Overwritten = append(res.Overwritten, "internal/adapters/store/schema.sql")
	return nil
}

func (g *Generator) replaceSchemaBlock(model *parser.Model, res *Result) error {
	schemaPath := filepath.Join(g.root, "internal", "adapters", "store", "schema.sql")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("schema.sql: %w", err)
	}

	startMark := fmt.Sprintf("-- scaffold:table:%s:start", model.Name)
	endMark := fmt.Sprintf("-- scaffold:table:%s:end", model.Name)

	tmpl := schemaTmpl
	if g.isPostgres() {
		tmpl = schemaTmplPostgres
	}
	block, err := renderTemplate(tmpl, buildMigrationCtx(model, nil, nil, g.manifest.DB))
	if err != nil {
		return err
	}

	updated, err := replaceMarkerBlock(string(data), startMark, endMark, block)
	if err != nil {
		// Block not found — add it
		updated = string(data) + "\n" + block + "\n"
	}

	if !g.dryRun {
		if err := os.WriteFile(schemaPath, []byte(updated), 0644); err != nil {
			return fmt.Errorf("schema.sql: %w", err)
		}
	}
	res.Overwritten = append(res.Overwritten, "internal/adapters/store/schema.sql")
	return nil
}

func (g *Generator) removeSchemaBlock(model *parser.Model, res *Result) error {
	schemaPath := filepath.Join(g.root, "internal", "adapters", "store", "schema.sql")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil // nothing to remove
	}

	startMark := fmt.Sprintf("-- scaffold:table:%s:start", model.Name)
	endMark := fmt.Sprintf("-- scaffold:table:%s:end", model.Name)

	updated, err := removeMarkerBlock(string(data), startMark, endMark)
	if err != nil {
		return nil // block not found, nothing to do
	}

	if !g.dryRun {
		if err := os.WriteFile(schemaPath, []byte(updated), 0644); err != nil {
			return fmt.Errorf("schema.sql: %w", err)
		}
	}
	res.Overwritten = append(res.Overwritten, "internal/adapters/store/schema.sql")
	return nil
}

// ---- Domain marker patching ----

func (g *Generator) patchDomainMarkers(rel string, model *parser.Model, res *Result) error {
	abs := filepath.Join(g.root, rel)
	data, err := os.ReadFile(abs)
	if err != nil {
		// file doesn't exist yet — create it
		tmpl := domainTmpl
		if g.isPostgres() {
			tmpl = domainTmplPostgres
		}
		return g.writeGoFile(rel, tmpl, buildDomainCtx(model, g.modulePath, g.manifest.DB), true, res)
	}

	src := string(data)

	// Replace struct fields block
	fields := buildTemplateFields(model.Fields, g.manifest.DB)
	fieldLines := buildFieldLines(fields, g.manifest.DB)
	src, err = replaceMarkerBlock(src, "// scaffold:fields:start", "// scaffold:fields:end",
		"// scaffold:fields:start\n"+fieldLines+"// scaffold:fields:end")
	if err != nil {
		return fmt.Errorf("domain marker patch (fields): %w", err)
	}

	// Replace GetID method
	receiver := model.Receiver()
	name := model.Name
	getIDBody := fmt.Sprintf("// scaffold:method:GetID:start\nfunc (%s %s) GetID() string { return %s.ID }\n// scaffold:method:GetID:end",
		receiver, name, receiver)
	src, err = replaceMarkerBlock(src, "// scaffold:method:GetID:start", "// scaffold:method:GetID:end", getIDBody)
	if err != nil {
		return fmt.Errorf("domain marker patch (GetID): %w", err)
	}

	// Replace WithID method
	withIDBody := fmt.Sprintf("// scaffold:method:WithID:start\nfunc (%s %s) WithID(id string) %s { %s.ID = id; return %s }\n// scaffold:method:WithID:end",
		receiver, name, name, receiver, receiver)
	src, err = replaceMarkerBlock(src, "// scaffold:method:WithID:start", "// scaffold:method:WithID:end", withIDBody)
	if err != nil {
		return fmt.Errorf("domain marker patch (WithID): %w", err)
	}

	formatted, err := formatter.GoSource(src)
	if err != nil {
		return fmt.Errorf("domain format after patch: %w", err)
	}

	if !g.dryRun {
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}

	res.Overwritten = append(res.Overwritten, rel+" (struct fields updated via markers)")
	return nil
}

// ---- Migrations ----

func (g *Generator) writeCreateMigration(model *parser.Model, res *Result) error {
	name := fmt.Sprintf("%05d_create_%s.sql", model.MigrationVersion, model.TableName)
	rel := filepath.Join("internal", "adapters", "store", "migrations", name)
	tmpl := migrationCreateTmpl
	if g.isPostgres() {
		tmpl = migrationCreateTmplPostgres
	}
	return g.writeSQLFile(rel, tmpl, buildMigrationCtx(model, nil, nil, g.manifest.DB), res)
}

func (g *Generator) writeAlterMigration(model *parser.Model, added, removed []parser.Field, res *Result) error {
	suffix := ""
	if len(added) > 0 {
		names := make([]string, len(added))
		for i, f := range added {
			names[i] = f.Name
		}
		suffix += "_add_" + strings.Join(names, "_")
	}
	if len(removed) > 0 {
		names := make([]string, len(removed))
		for i, f := range removed {
			names[i] = f.Name
		}
		suffix += "_drop_" + strings.Join(names, "_")
	}

	name := fmt.Sprintf("%05d_%s%s.sql", model.MigrationVersion, model.TableName, suffix)
	rel := filepath.Join("internal", "adapters", "store", "migrations", name)

	ctx := buildMigrationCtx(model, added, removed, g.manifest.DB)

	tmplStr := ""
	if len(added) > 0 {
		tmplStr += migrationAddColTmpl
	}
	if len(removed) > 0 {
		tmplStr += migrationDropColTmpl
	}

	return g.writeSQLFile(rel, tmplStr, ctx, res)
}

func (g *Generator) writeDropMigration(model *parser.Model, res *Result) error {
	name := fmt.Sprintf("%05d_drop_%s.sql", model.MigrationVersion, model.TableName)
	rel := filepath.Join("internal", "adapters", "store", "migrations", name)
	tmpl := migrationDropTableTmpl
	if g.isPostgres() {
		tmpl = migrationDropTableTmplPostgres
	}
	return g.writeSQLFile(rel, tmpl, buildMigrationCtx(model, nil, nil, g.manifest.DB), res)
}

// ---- Registry ----

func (g *Generator) writeRegistry(res *Result) error {
	models := make([]registryModel, 0, len(g.manifest.Models))
	for name := range g.manifest.Models {
		models = append(models, registryModel{Name: name})
	}

	rel := filepath.Join("internal", "app", "registry.go")
	tmpl := registryTmpl
	if g.isPostgres() {
		tmpl = registryTmplPostgres
	}
	return g.writeGoFile(rel, tmpl, registryCtx{
		ModulePath: g.modulePath,
		Models:     models,
	}, true, res)
}

// ---- Utilities ----

func renderTemplate(tmplStr string, data any) (string, error) {
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func buildFieldLines(fields []templateField, db string) string {
	var b strings.Builder
	for _, f := range fields {
		if db == "postgres" {
			b.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\" db:\"%s\"`\n", f.GoName, f.GoType, f.Name, f.Name))
		} else {
			b.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\"`\n", f.GoName, f.GoType, f.Name))
		}
	}
	// System fields are always present.
	if db == "postgres" {
		b.WriteString("\tID        string    `json:\"id\" db:\"id\"`\n")
		b.WriteString("\tCreatedAt time.Time `json:\"created_at\" db:\"created_at\"`\n")
		b.WriteString("\tUpdatedAt time.Time `json:\"updated_at\" db:\"updated_at\"`\n")
	} else {
		b.WriteString("\tID        string    `json:\"id\"`\n")
		b.WriteString("\tCreatedAt time.Time `json:\"created_at\"`\n")
		b.WriteString("\tUpdatedAt time.Time `json:\"updated_at\"`\n")
	}
	return b.String()
}

func buildMigrationCtx(model *parser.Model, added, removed []parser.Field, db string) migrationCtx {
	var idDef string
	switch db {
	case "postgres":
		idDef = "id UUID PRIMARY KEY DEFAULT uuidv7()"
	case "sqlite":
		idDef = "id TEXT PRIMARY KEY"
	default: // legacy sqlite with sqlean
		idDef = "id TEXT PRIMARY KEY DEFAULT (uuid7())"
	}
	return migrationCtx{
		Name:      model.Name,
		TableName: model.TableName,
		Fields:    buildTemplateFields(model.Fields, db),
		Added:     buildTemplateFields(added, db),
		Removed:   buildTemplateFields(removed, db),
		IDDef:     idDef,
	}
}

// replaceMarkerBlock replaces the section from startMark to endMark (inclusive) with newBlock.
func replaceMarkerBlock(content, startMark, endMark, newBlock string) (string, error) {
	startIdx := strings.Index(content, startMark)
	if startIdx == -1 {
		return "", fmt.Errorf("marker not found: %q", startMark)
	}
	endIdx := strings.Index(content, endMark)
	if endIdx == -1 {
		return "", fmt.Errorf("marker not found: %q", endMark)
	}
	endIdx += len(endMark)
	return content[:startIdx] + newBlock + content[endIdx:], nil
}

// removeMarkerBlock removes the section from startMark to endMark (inclusive) plus any surrounding blank line.
func removeMarkerBlock(content, startMark, endMark string) (string, error) {
	startIdx := strings.Index(content, startMark)
	if startIdx == -1 {
		return "", fmt.Errorf("marker not found: %q", startMark)
	}
	endIdx := strings.Index(content, endMark)
	if endIdx == -1 {
		return "", fmt.Errorf("marker not found: %q", endMark)
	}
	endIdx += len(endMark)
	// consume a trailing newline if present
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}
	return content[:startIdx] + content[endIdx:], nil
}

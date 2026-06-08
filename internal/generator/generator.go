package generator

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

func (g *Generator) isPostgres() bool { return g.manifest.IsPostgres() }
func (g *Generator) isGRPC() bool     { return g.manifest.IsGRPC() }
func (g *Generator) isSSR() bool      { return g.manifest.IsSSR() }

// protoTemplateFuncs returns template.FuncMap needed by protoTmpl and grpcHandlerTmpl.
func protoTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// fieldNum returns the proto field number: base + index.
		"fieldNum": func(idx, base int) int { return base + idx },
		// protoToDomain renders the right-hand side for a domain struct field,
		// read from a proto request. It bridges the type differences between
		// protoc-gen-go output and the domain model: Go int <-> proto int32,
		// nullable domain pointers <-> proto3 optional (pointer) fields, and
		// time.Time <-> google.protobuf.Timestamp.
		"protoToDomain": func(f templateField, recv string) string {
			get := recv + ".Get" + f.ProtoGoName + "()" // value getter
			ptr := recv + "." + f.ProtoGoName            // optional field is itself a pointer
			switch f.GoType {
			case "int":
				return "int(" + get + ")"
			case "time.Time":
				return get + ".AsTime()"
			case "*time.Time":
				return `func() *time.Time { if v := ` + get + `; v != nil { t := v.AsTime(); return &t }; return nil }()`
			case "*int":
				return `func() *int { if v := ` + ptr + `; v != nil { i := int(*v); return &i }; return nil }()`
			case "*string", "*int64", "*float64", "*bool":
				return ptr
			default:
				// string, int64, float64, bool, json.RawMessage (bytes)
				return get
			}
		},
		// domainToProto renders the right-hand side for a proto struct field,
		// written from a domain value. It is the inverse of protoToDomain.
		"domainToProto": func(f templateField, recv string) string {
			field := recv + "." + f.GoName
			switch f.GoType {
			case "int":
				return "int32(" + field + ")"
			case "time.Time":
				return "timestamppb.New(" + field + ")"
			case "*time.Time":
				return `func() *timestamppb.Timestamp { if ` + field + ` != nil { return timestamppb.New(*` + field + `) }; return nil }()`
			case "*int":
				return `func() *int32 { if ` + field + ` != nil { v := int32(*` + field + `); return &v }; return nil }()`
			default:
				// string, int64, float64, bool, json.RawMessage, and *string/
				// *int64/*float64/*bool whose pointer types already match proto.
				return field
			}
		},
	}
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

	// app.go routes section is always regenerated
	if err := g.writeRoutes(res); err != nil {
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
	if g.isSSR() {
		files = append(files,
			filepath.Join("internal", "adapters", "http", model.Snake()+"_handler_gen.go"),
			filepath.Join("internal", "adapters", "http", model.Snake()+"_handler.go"),
		)
		// Remove SSR template directory
		tmplDir := filepath.Join(g.root, "web", "templates", model.Plural())
		if !g.dryRun {
			if err := os.RemoveAll(tmplDir); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("destroy: remove templates dir %s: %w", tmplDir, err)
			}
		}
		res.Deleted = append(res.Deleted, filepath.Join("web", "templates", model.Plural()))
	}
	if g.isGRPC() {
		files = append(files,
			filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+".proto"),
			// buf-generated code derived from the .proto — orphaned once it is gone.
			filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+".pb.go"),
			filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+"_grpc.pb.go"),
			filepath.Join("internal", "adapters", "grpc", model.Snake()+"_handler_gen.go"),
		)
	}

	for _, rel := range files {
		abs := filepath.Join(g.root, rel)
		existed := fileExists(abs)
		if !g.dryRun {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("destroy: remove %s: %w", rel, err)
			}
		}
		// Only report files that were actually present, so optional artifacts
		// (e.g. .pb.go before `make proto`) don't show as deleted phantoms.
		if existed {
			res.Deleted = append(res.Deleted, rel)
		}
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

	// Regenerate routes without this model
	if err := g.writeRoutes(res); err != nil {
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

	if g.isSSR() {
		// internal/adapters/http/{model}_handler_gen.go — always regenerated
		ssrHandlerPath := filepath.Join("internal", "adapters", "http", model.Snake()+"_handler_gen.go")
		if err := g.writeSSRHandler(ssrHandlerPath, model, res); err != nil {
			return err
		}
		// internal/adapters/http/{model}_handler.go — written once
		ssrUserPath := filepath.Join("internal", "adapters", "http", model.Snake()+"_handler.go")
		if err := g.writeGoFileOnce(ssrUserPath, ssrHandlerUserTmpl, map[string]string{
			"Name": model.Name,
		}, res); err != nil {
			return err
		}
		// web/templates/{plural}/*.html — SSR HTML templates (always regenerated)
		if err := g.writeSSRTemplates(model, res); err != nil {
			return err
		}
	}

	if g.isGRPC() {
		// internal/adapters/grpc/pb/{model}.proto — generated .pb.go lands
		// here too (buf source_relative), matching the pb import path.
		protoPath := filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+".proto")
		if err := g.writeProto(protoPath, model, res); err != nil {
			return err
		}
		// internal/adapters/grpc/{model}_handler_gen.go
		handlerPath := filepath.Join("internal", "adapters", "grpc", model.Snake()+"_handler_gen.go")
		if err := g.writeGRPCHandler(handlerPath, model, res); err != nil {
			return err
		}
		// internal/adapters/grpc/shared.go — created once
		sharedPath := filepath.Join("internal", "adapters", "grpc", "shared.go")
		if err := g.writeGRPCShared(sharedPath, res); err != nil {
			return err
		}
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

	if g.isSSR() {
		// Regenerate SSR handler and templates on every update (fields may have changed)
		ssrHandlerPath := filepath.Join("internal", "adapters", "http", model.Snake()+"_handler_gen.go")
		if err := g.writeSSRHandler(ssrHandlerPath, model, res); err != nil {
			return err
		}
		if err := g.writeSSRTemplates(model, res); err != nil {
			return err
		}
		ssrUserPath := filepath.Join("internal", "adapters", "http", model.Snake()+"_handler.go")
		res.Unchanged = append(res.Unchanged, ssrUserPath)
	}

	if g.isGRPC() {
		// Regenerate proto + handler on every update (fields may have changed).
		// Run `make proto` afterwards to recompile the pb package.
		protoPath := filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+".proto")
		if err := g.writeProto(protoPath, model, res); err != nil {
			return err
		}
		handlerPath := filepath.Join("internal", "adapters", "grpc", model.Snake()+"_handler_gen.go")
		if err := g.writeGRPCHandler(handlerPath, model, res); err != nil {
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

// ---- gRPC ----

func (g *Generator) writeProto(rel string, model *parser.Model, res *Result) error {
	abs := filepath.Join(g.root, rel)
	fields := buildTemplateFields(model.Fields, g.manifest.DB)
	ctx := protoCtx{
		ModulePath:   g.modulePath,
		Name:         model.Name,
		Lower:        model.Lower(),
		Fields:       fields,
		CreatedAtIdx: len(fields) + 2,
		UpdatedAtIdx: len(fields) + 3,
	}
	content, err := renderTemplateWithFuncs(protoTmpl, ctx, protoTemplateFuncs())
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if fileExists(abs) {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
	return nil
}

func (g *Generator) writeGRPCHandler(rel string, model *parser.Model, res *Result) error {
	fields := buildTemplateFields(model.Fields, g.manifest.DB)
	needsTime := false
	for _, f := range fields {
		if f.GoType == "*time.Time" {
			needsTime = true
		}
	}
	ctx := grpcHandlerCtx{
		ModulePath: g.modulePath,
		Name:       model.Name,
		Lower:      model.Lower(),
		Fields:     fields,
		NeedsTime:  needsTime,
	}
	src, err := renderTemplateWithFuncs(grpcHandlerTmpl, ctx, protoTemplateFuncs())
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	formatted, err := formatter.GoSource(src)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	abs := filepath.Join(g.root, rel)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if fileExists(abs) {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
	return nil
}

func (g *Generator) writeGRPCShared(rel string, res *Result) error {
	abs := filepath.Join(g.root, rel)
	if fileExists(abs) {
		res.Unchanged = append(res.Unchanged, rel)
		return nil
	}
	src, err := renderTemplate(grpcSharedTmpl, map[string]string{"ModulePath": g.modulePath})
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	formatted, err := formatter.GoSource(src)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	res.Created = append(res.Created, rel)
	return nil
}

// ---- SSR ----

func (g *Generator) writeSSRHandler(rel string, model *parser.Model, res *Result) error {
	fields := buildTemplateFields(model.Fields, g.manifest.DB)
	needsStrconv := false
	for _, f := range fields {
		if strings.Contains(f.GoType, "int") || strings.Contains(f.GoType, "float") {
			needsStrconv = true
			break
		}
	}
	ctx := ssrHandlerCtx{
		ModulePath:   g.modulePath,
		Name:         model.Name,
		Lower:        model.Lower(),
		Plural:       model.Plural(),
		Fields:       fields,
		NeedsStrconv: needsStrconv,
	}
	src, err := renderTemplate(ssrHandlerTmpl, ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	formatted, err := formatter.GoSource(src)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	abs := filepath.Join(g.root, rel)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if fileExists(abs) {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
	return nil
}

func (g *Generator) writeSSRTemplates(model *parser.Model, res *Result) error {
	fields := buildTemplateFields(model.Fields, g.manifest.DB)
	ctx := ssrHandlerCtx{
		ModulePath: g.modulePath,
		Name:       model.Name,
		Lower:      model.Lower(),
		Plural:     model.Plural(),
		Fields:     fields,
	}

	type htmlFile struct {
		relPath string
		tmpl    string
	}
	htmlFiles := []htmlFile{
		{filepath.Join("web", "templates", model.Plural(), "list.html"), ssrListHTMLTmpl},
		{filepath.Join("web", "templates", model.Plural(), "form.html"), ssrFormHTMLTmpl},
		{filepath.Join("web", "templates", model.Plural(), "show.html"), ssrShowHTMLTmpl},
	}

	for _, hf := range htmlFiles {
		content, err := renderTemplateHTML(hf.tmpl, ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", hf.relPath, err)
		}
		abs := filepath.Join(g.root, hf.relPath)
		if !g.dryRun {
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(hf.relPath), err)
			}
			if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
				return fmt.Errorf("write %s: %w", hf.relPath, err)
			}
		}
		res.Created = append(res.Created, hf.relPath)
	}
	return nil
}

// renderTemplateHTML renders a template using [[ ]] as outer delimiters so that
// {{ }} in the output is treated as literal text (used for generating HTML template files).
func renderTemplateHTML(tmplStr string, data any) (string, error) {
	funcs := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"len": func(v []templateField) int { return len(v) },
	}
	t := template.New("").Delims("[[", "]]").Funcs(funcs)
	tmpl, err := t.Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse html template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute html template: %w", err)
	}
	return buf.String(), nil
}

// ---- Registry ----

func (g *Generator) writeRegistry(res *Result) error {
	models := make([]registryModel, 0, len(g.manifest.Models))
	for name := range g.manifest.Models {
		models = append(models, registryModel{Name: name})
	}

	rel := filepath.Join("internal", "app", "registry.go")
	var tmpl string
	switch {
	case g.isSSR() && g.isPostgres():
		tmpl = registryTmplSSRPostgres
	case g.isSSR():
		tmpl = registryTmplSSR
	case g.isPostgres():
		tmpl = registryTmplPostgres
	default:
		tmpl = registryTmpl
	}
	return g.writeGoFile(rel, tmpl, registryCtx{
		ModulePath: g.modulePath,
		Models:     models,
		GRPC:       g.isGRPC(),
		IsSSR:      g.isSSR(),
	}, true, res)
}

// ---- Routes ----

// writeRoutes patches the // scaffold:routes:start/end block in internal/app/app.go
// with route mounting statements for all models in the manifest.
func (g *Generator) writeRoutes(res *Result) error {
	appPath := filepath.Join(g.root, "internal", "app", "app.go")
	data, err := os.ReadFile(appPath)
	if err != nil {
		// app.go doesn't exist yet (e.g. bare project without boilerplate) — skip
		return nil
	}

	// Sort model names for deterministic output
	names := make([]string, 0, len(g.manifest.Models))
	for name := range g.manifest.Models {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build the route block content
	var lines strings.Builder
	for _, name := range names {
		if g.isSSR() {
			fmt.Fprintf(&lines, "\t\tr.Mount(a.registry.Handlers.%s.Prefix(), a.registry.Handlers.%s.Router())\n", name, name)
		} else {
			fmt.Fprintf(&lines, "\t\tr.Route(a.registry.Handlers.%s.Prefix(), a.registry.Handlers.%s.RegisterRoutes)\n", name, name)
		}
	}

	const startMark = "// scaffold:routes:start"
	const endMark = "// scaffold:routes:end"
	newBlock := startMark + "\n" + lines.String() + "\t\t" + endMark

	updated, err := replaceMarkerBlock(string(data), startMark, endMark, newBlock)
	if err != nil {
		// Marker not found — app.go has the old single-line comment; skip silently
		return nil
	}

	// In gRPC mode, also patch the // scaffold:grpc:start/end block with the
	// per-model service registrations so the handlers are actually reachable.
	if g.isGRPC() {
		var grpcLines strings.Builder
		for _, name := range names {
			fmt.Fprintf(&grpcLines, "\ta.registry.GRPCHandlers.%s.Register(a.grpcServer)\n", name)
		}
		const grpcStart = "// scaffold:grpc:start"
		const grpcEnd = "// scaffold:grpc:end"
		grpcBlock := grpcStart + "\n" + grpcLines.String() + "\t" + grpcEnd
		if patched, perr := replaceMarkerBlock(updated, grpcStart, grpcEnd, grpcBlock); perr == nil {
			updated = patched
		}
	}

	formatted, err := formatter.GoSource(updated)
	if err != nil {
		return fmt.Errorf("app.go routes format: %w", err)
	}

	if !g.dryRun {
		if err := os.WriteFile(appPath, formatted, 0644); err != nil {
			return fmt.Errorf("write app.go: %w", err)
		}
	}
	res.Overwritten = append(res.Overwritten, "internal/app/app.go (routes updated)")
	return nil
}

// ---- Utilities ----

func renderTemplate(tmplStr string, data any) (string, error) {
	return renderTemplateWithFuncs(tmplStr, data, nil)
}

func renderTemplateWithFuncs(tmplStr string, data any, funcs template.FuncMap) (string, error) {
	t := template.New("")
	if len(funcs) > 0 {
		t = t.Funcs(funcs)
	}
	tmpl, err := t.Parse(tmplStr)
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
		idDef = "id TEXT PRIMARY KEY DEFAULT (uuid7())"
	default:
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

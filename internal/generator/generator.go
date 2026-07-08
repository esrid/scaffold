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
	"time"

	"github.com/esrid/scaffold/internal/formatter"
	"github.com/esrid/scaffold/internal/parser"
	"github.com/pmezard/go-difflib/difflib"
)

// Generator writes scaffold files into a target project.
type Generator struct {
	root       string
	modulePath string
	manifest   *parser.Manifest
	dryRun     bool

	// RegenViews forces SSR view templates to be overwritten. By default views
	// are write-once (created if absent, never clobbered) so hand edits survive.
	RegenViews bool

	// Diff, when set, records a unified diff of every file that would change
	// instead of writing it. Implies dryRun (no files are touched).
	Diff bool

	// KeepCustom, when set, prevents destroy from deleting user-owned custom files.
	KeepCustom bool

	// Force, when set, overrides safeguards (like foreign key dependency checks).
	Force bool
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
			ptr := recv + "." + f.ProtoGoName           // optional field is itself a pointer
			switch f.GoType {
			case "[]int":
				// proto repeated int32 -> domain []int
				return `func() []int { s := ` + get + `; out := make([]int, len(s)); for i, v := range s { out[i] = int(v) }; return out }()`
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
			case "[]int":
				// domain []int -> proto repeated int32
				return `func() []int32 { out := make([]int32, len(` + field + `)); for i, v := range ` + field + ` { out[i] = int32(v) }; return out }()`
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
	BackedUp    []string
	Diffs       []string // unified diffs, populated in --diff mode
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
	if len(r.BackedUp) > 0 {
		fmt.Fprintln(w, "\nBacked up (custom code):")
		for _, f := range r.BackedUp {
			fmt.Fprintf(w, "  -> %s\n", f)
		}
	}
	if len(r.Diffs) > 0 {
		fmt.Fprintln(w, "\nDiff (no files written):")
		for _, d := range r.Diffs {
			fmt.Fprintln(w, d)
		}
		return
	}
	fmt.Fprintln(w, "\nNext steps:")
	fmt.Fprintln(w, "  1. Add validation logic in domain file → Validate()")
	fmt.Fprintln(w, "  2. Add custom queries in store file (below generated section)")
	fmt.Fprintln(w, "  3. Run: go build ./...")
}

// recordDiff appends a unified diff of rel (old vs newContent) to res when in
// --diff mode. No-op otherwise.
func (g *Generator) recordDiff(rel string, newContent []byte, res *Result) {
	if !g.Diff {
		return
	}
	old, _ := os.ReadFile(filepath.Join(g.root, rel))
	d, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(old)),
		B:        difflib.SplitLines(string(newContent)),
		FromFile: rel + " (current)",
		ToFile:   rel + " (generated)",
		Context:  3,
	})
	if err != nil || strings.TrimSpace(d) == "" {
		return
	}
	res.Diffs = append(res.Diffs, d)
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

	if g.isSSR() {
		// SSR mode: registry + routes are marker-spliced spans inside the
		// merged app.go — writeAppWiring touches only this model's blocks
		// plus the mechanical whole-block sections.
		if err := g.writeAppWiring(model, res); err != nil {
			return nil, err
		}
	} else {
		// REST mode: registry.go and routes_gen.go are still always
		// regenerated in full (no per-model hand-edit target there yet).
		if err := g.writeRegistry(res); err != nil {
			return nil, err
		}
		if err := g.writeRoutes(res); err != nil {
			return nil, err
		}
	}

	return res, nil
}

// Destroy removes all scaffold files for the given model.
func (g *Generator) Destroy(model *parser.Model) (*Result, error) {
	// Check if other models reference this model's table via foreign key
	var referencers []string
	targetTable := model.TableName
	for otherName, otherModel := range g.manifest.Models {
		if otherName == model.Name {
			continue
		}
		for _, f := range otherModel.Fields {
			for _, mod := range f.Modifiers {
				if strings.HasPrefix(mod, "fk=") && strings.TrimPrefix(mod, "fk=") == targetTable {
					referencers = append(referencers, fmt.Sprintf("%s (%s)", otherName, f.Name))
				}
			}
		}
	}

	if len(referencers) > 0 && !g.Force {
		return nil, fmt.Errorf("cannot destroy model %q: referenced by foreign key in %s (use --force to override)",
			model.Name, strings.Join(referencers, ", "))
	}

	res := &Result{}

	// User-owned custom/write-once files
	customFiles := []string{
		filepath.Join("internal", "core", "domain", model.Snake()+".go"),
		filepath.Join("internal", "core", "ports", model.Snake()+".go"),
		filepath.Join("internal", "core", "services", model.Snake()+"_service.go"),
		filepath.Join("internal", "adapters", "store", model.Snake()+"_store.go"),
	}
	if !model.NoHandler && g.isSSR() {
		customFiles = append(customFiles,
			filepath.Join("internal", "adapters", "http", model.Snake()+"_handler.go"),
			filepath.Join("web", "views", model.Snake()+".templ"),
		)
	}

	// Generated files (always deleted)
	genFiles := []string{
		filepath.Join("internal", "core", "domain", model.Snake()+"_gen.go"),
		filepath.Join("internal", "core", "services", model.Snake()+"_service_gen.go"),
		filepath.Join("internal", "adapters", "store", model.Snake()+"_store_gen.go"),
	}
	if !model.NoHandler && g.isSSR() {
		genFiles = append(genFiles,
			filepath.Join("internal", "adapters", "http", model.Snake()+"_handler_gen.go"),
			filepath.Join("web", "views", model.Snake()+"_templ.go"),
		)
	}
	if !model.NoHandler && g.isGRPC() {
		genFiles = append(genFiles,
			filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+".proto"),
			filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+".pb.go"),
			filepath.Join("internal", "adapters", "grpc", "pb", model.Snake()+"_grpc.pb.go"),
			filepath.Join("internal", "adapters", "grpc", model.Snake()+"_handler_gen.go"),
		)
	}

	// Check if any custom files exist and back them up
	var existingCustom []string
	for _, rel := range customFiles {
		abs := filepath.Join(g.root, rel)
		if fileExists(abs) {
			existingCustom = append(existingCustom, rel)
		}
	}

	if len(existingCustom) > 0 && !g.dryRun {
		timestamp := time.Now().Format("20060102150405")
		backupBase := filepath.Join(g.root, ".scaffold", "backups", timestamp)
		for _, rel := range existingCustom {
			abs := filepath.Join(g.root, rel)
			backupPath := filepath.Join(backupBase, rel)
			if err := copyFile(abs, backupPath); err != nil {
				return nil, fmt.Errorf("destroy backup %s: %w", rel, err)
			}
			res.BackedUp = append(res.BackedUp, rel)
		}
	} else if len(existingCustom) > 0 && g.dryRun {
		for _, rel := range existingCustom {
			res.BackedUp = append(res.BackedUp, rel)
		}
	}

	// Delete generated files
	for _, rel := range genFiles {
		abs := filepath.Join(g.root, rel)
		existed := fileExists(abs)
		if !g.dryRun {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("destroy: remove %s: %w", rel, err)
			}
		}
		if existed {
			res.Deleted = append(res.Deleted, rel)
		}
	}

	// Delete custom files if not KeepCustom
	for _, rel := range customFiles {
		abs := filepath.Join(g.root, rel)
		existed := fileExists(abs)
		if g.KeepCustom {
			if existed {
				res.Unchanged = append(res.Unchanged, rel)
			}
			continue
		}
		if !g.dryRun {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("destroy: remove %s: %w", rel, err)
			}
		}
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

	if g.isSSR() {
		if err := g.removeAppWiring(model, res); err != nil {
			return nil, err
		}
	} else {
		// Regenerate registry without this model
		if err := g.writeRegistry(res); err != nil {
			return nil, err
		}
		// Regenerate routes without this model
		if err := g.writeRoutes(res); err != nil {
			return nil, err
		}
	}

	return res, nil
}

// ---- CREATE mode ----

func (g *Generator) scaffoldCreate(model *parser.Model, res *Result) error {
	// domain/{model}_gen.go — struct + identity methods (regenerated, no markers)
	domainGenPath := filepath.Join("internal", "core", "domain", model.Snake()+"_gen.go")
	if err := g.writeGoFile(domainGenPath, domainGenTmpl, buildDomainCtx(model, g.modulePath, g.manifest.DB), true, res); err != nil {
		return err
	}
	// domain/{model}.go — Validate() + custom methods (created once)
	domainUserPath := filepath.Join("internal", "core", "domain", model.Snake()+".go")
	if err := g.writeGoFileOnce(domainUserPath, domainUserTmpl, buildDomainCtx(model, g.modulePath, g.manifest.DB), res); err != nil {
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

	if !model.NoHandler && g.isSSR() {
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
		// web/views/{model}.templ — templ components (always regenerated)
		if err := g.writeSSRTemplates(model, res); err != nil {
			return err
		}
	}

	if !model.NoHandler && g.isGRPC() {
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
	// domain/{model}_gen.go — regenerated; domain/{model}.go is user-owned.
	domainGenPath := filepath.Join("internal", "core", "domain", model.Snake()+"_gen.go")
	if err := g.writeGoFile(domainGenPath, domainGenTmpl, buildDomainCtx(model, g.modulePath, g.manifest.DB), true, res); err != nil {
		return err
	}
	res.Unchanged = append(res.Unchanged, filepath.Join("internal", "core", "domain", model.Snake()+".go"))

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
	addedUT, removedUT := diffUniqueTogether(model.PrevUniqueTogether, model.UniqueTogether)
	if len(added) > 0 || len(removed) > 0 || model.SoftDelete != model.PrevSoftDelete || len(addedUT) > 0 || len(removedUT) > 0 {
		if err := g.writeAlterMigration(model, added, removed, res); err != nil {
			return err
		}
	}

	if !model.NoHandler && g.isSSR() {
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

	if !model.NoHandler && g.isGRPC() {
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

	g.recordDiff(rel, formatted, res)
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
	g.recordDiff(rel, formatted, res)
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

	existed := fileExists(abs)
	g.recordDiff(rel, []byte(content), res)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			return err
		}
	}
	if existed {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
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

	existing, _ := os.ReadFile(schemaPath)
	updated := string(existing) + "\n" + block + "\n"
	g.recordDiff("internal/adapters/store/schema.sql", []byte(updated), res)
	if !g.dryRun {
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
	if os.IsNotExist(err) {
		// Legacy project without a schema.sql — fall back to creating it with
		// just this model's block instead of failing the whole update.
		data = nil
	} else if err != nil {
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

	g.recordDiff("internal/adapters/store/schema.sql", []byte(updated), res)
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

	g.recordDiff("internal/adapters/store/schema.sql", []byte(updated), res)
	if !g.dryRun {
		if err := os.WriteFile(schemaPath, []byte(updated), 0644); err != nil {
			return fmt.Errorf("schema.sql: %w", err)
		}
	}
	res.Overwritten = append(res.Overwritten, "internal/adapters/store/schema.sql")
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
	if model.SoftDelete && !model.PrevSoftDelete {
		tmplStr += migrationAddSoftDeleteTmpl
	} else if !model.SoftDelete && model.PrevSoftDelete {
		tmplStr += migrationDropSoftDeleteTmpl
	}
	addedUT, removedUT := diffUniqueTogether(model.PrevUniqueTogether, model.UniqueTogether)
	if len(addedUT) > 0 {
		tmplStr += migrationAddUniqueTogetherTmpl
	}
	if len(removedUT) > 0 {
		tmplStr += migrationDropUniqueTogetherTmpl
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
		Ops:          model.Ops,
	}
	content, err := renderTemplateWithFuncs(protoTmpl, ctx, protoTemplateFuncs())
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	existed := fileExists(abs)
	g.recordDiff(rel, []byte(content), res)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if existed {
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
		Ops:        model.Ops,
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
	existed := fileExists(abs)
	g.recordDiff(rel, formatted, res)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if existed {
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
	g.recordDiff(rel, formatted, res)
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

// wrapHandlerExpr turns a handler method reference into a ready-to-emit Go
// expression, nesting any configured middleware around it innermost-first
// (the last name in names runs closest to the handler). No names -> the
// bare http.HandlerFunc, identical to before --middleware existed.
func wrapHandlerExpr(names []string, handlerRef string) string {
	expr := "http.HandlerFunc(" + handlerRef + ")"
	for i := len(names) - 1; i >= 0; i-- {
		expr = names[i] + "(" + expr + ")"
	}
	return expr
}

func (g *Generator) writeSSRHandler(rel string, model *parser.Model, res *Result) error {
	fields := buildTemplateFields(model.Fields, g.manifest.DB)
	needsStrconv, needsTime, needsJSON := false, false, false
	for _, f := range fields {
		// bindForm parses int/int64/float values with strconv — for both scalar
		// fields and array fields ([]int/[]int64/[]float64). string and bool
		// (and []string/[]bool) need no strconv.
		if strings.Contains(f.GoType, "int") || strings.Contains(f.GoType, "float") {
			needsStrconv = true
		}
		if f.IsTime {
			needsTime = true
		}
		if f.IsJSON {
			needsJSON = true
		}
	}
	mw := model.Middleware
	ctx := ssrHandlerCtx{
		ModulePath:   g.modulePath,
		Name:         model.Name,
		Lower:        model.Lower(),
		Plural:       model.Plural(),
		Fields:       fields,
		NeedsStrconv: needsStrconv,
		NeedsTime:    needsTime,
		NeedsJSON:    needsJSON,
		Ops:          model.Ops,
		MW: ssrHandlerMiddleware{
			List:   wrapHandlerExpr(mw["list"], "h.List"),
			New:    wrapHandlerExpr(mw["create"], "h.New"),
			Create: wrapHandlerExpr(mw["create"], "h.Create"),
			Show:   wrapHandlerExpr(mw["read"], "h.Show"),
			Edit:   wrapHandlerExpr(mw["update"], "h.Edit"),
			Update: wrapHandlerExpr(mw["update"], "h.Update"),
			Delete: wrapHandlerExpr(mw["delete"], "h.Delete"),
		},
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
	existed := fileExists(abs)
	g.recordDiff(rel, formatted, res)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if existed {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
	return nil
}

func (g *Generator) writeSSRTemplates(model *parser.Model, res *Result) error {
	rel := filepath.Join("web", "views", model.Snake()+".templ")
	abs := filepath.Join(g.root, rel)

	// Views are write-once: once created they belong to the user and are never
	// clobbered on re-gen. --regen-views (g.RegenViews) forces a fresh scaffold.
	if fileExists(abs) && !g.RegenViews {
		res.Unchanged = append(res.Unchanged, rel)
		return nil
	}

	fields := buildTemplateFields(model.Fields, g.manifest.DB)
	ctx := ssrHandlerCtx{
		ModulePath: g.modulePath,
		Name:       model.Name,
		Lower:      model.Lower(),
		Plural:     model.Plural(),
		Fields:     fields,
		Ops:        model.Ops,
	}

	content, err := renderTemplateHTML(ssrViewTmpl, ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	existed := fileExists(abs)
	g.recordDiff(rel, []byte(content), res)
	if !g.dryRun {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	if existed {
		res.Overwritten = append(res.Overwritten, rel)
	} else {
		res.Created = append(res.Created, rel)
	}
	return nil
}

// renderTemplateHTML renders a template using [[ ]] as outer delimiters so that
// { } and {{ }} in the output are treated as literal text (used for generating
// templ component files, whose own { } expression syntax must pass through).
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

// buildCRUDMiddlewareLiteral renders a model's --middleware config (REST
// mode) as a ready-to-emit httpadapter.CRUDMiddleware{...} Go expression,
// qualifying each name as httpadapter.<Name> since registry.go lives in
// package app. Op order is fixed (not map iteration) so re-running gen
// without changes stays byte-identical.
func buildCRUDMiddlewareLiteral(mw map[string][]string) string {
	fieldNames := map[string]string{"list": "List", "read": "Read", "create": "Create", "update": "Update", "delete": "Delete"}
	var parts []string
	for _, op := range []string{"list", "read", "create", "update", "delete"} {
		names := mw[op]
		if len(names) == 0 {
			continue
		}
		qualified := make([]string, len(names))
		for i, n := range names {
			qualified[i] = "httpadapter." + n
		}
		parts = append(parts, fmt.Sprintf("%s: []func(http.Handler) http.Handler{%s}", fieldNames[op], strings.Join(qualified, ", ")))
	}
	return "httpadapter.CRUDMiddleware{" + strings.Join(parts, ", ") + "}"
}

// ---- Registry ----

// buildRegistryCtx gathers every model in the manifest into a registryCtx —
// shared by the REST-mode whole-file registry.go (writeRegistry, unchanged)
// and the SSR-mode merged app.go wiring (writeAppWiring).
func (g *Generator) buildRegistryCtx() registryCtx {
	models := make([]registryModel, 0, len(g.manifest.Models))
	hasHandlers := false
	for name, entry := range g.manifest.Models {
		if !entry.NoHandler {
			hasHandlers = true
		}
		models = append(models, registryModel{
			Name:              name,
			Lower:             strings.ToLower(name),
			NoHandler:         entry.NoHandler,
			Ops:               parser.OpsFromSkipped(entry.SkippedOps),
			MiddlewareLiteral: buildCRUDMiddlewareLiteral(entry.Middleware),
		})
	}
	// Map iteration order is random — sort so output is deterministic and
	// re-running gen without changes stays byte-identical.
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })

	dbVar := "db"
	if g.isPostgres() {
		dbVar = "pool"
	}
	return registryCtx{
		ModulePath:  g.modulePath,
		Models:      models,
		GRPC:        g.isGRPC(),
		IsSSR:       g.isSSR(),
		HasHandlers: hasHandlers,
		DBVar:       dbVar,
	}
}

// writeRegistry regenerates the whole-file internal/app/registry.go —
// REST mode only. SSR mode's Registry lives inside the merged app.go
// instead (see writeAppWiring), never here.
func (g *Generator) writeRegistry(res *Result) error {
	ctx := g.buildRegistryCtx()
	rel := filepath.Join("internal", "app", "registry.go")
	var tmpl string
	switch {
	case g.isPostgres():
		tmpl = registryTmplPostgres
	default:
		tmpl = registryTmpl
	}
	return g.writeGoFile(rel, tmpl, ctx, true, res)
}

// writeAppWiring splices this model's Registry wiring into the merged
// app.go (SSR mode only — REST mode still uses writeRegistry+writeRoutes
// as separate always-overwritten files). Whole-block sections (imports,
// struct field lists, store wiring, route mounting) are rebuilt from the
// full manifest every time; service-wire and handler-wire are spliced for
// ONLY this model, leaving every other model's block — hand-edited or
// not — untouched.
func (g *Generator) writeAppWiring(model *parser.Model, res *Result) error {
	rel := filepath.Join("internal", "app", "app.go")
	path := filepath.Join(g.root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("app.go: %w", err)
	}
	content := string(data)
	ctx := g.buildRegistryCtx()

	whole := []struct{ name, tmpl string }{
		{"imports", ssrImportsBlockTmpl},
		{"type-defs", ssrTypeDefsTmpl},
		{"stores-wire", ssrStoresWireTmpl},
		{"routes", ssrRoutesBlockTmpl},
	}
	if g.isGRPC() {
		whole = append(whole, struct{ name, tmpl string }{"grpc-wire", grpcWireTmpl}, struct{ name, tmpl string }{"grpc-routes", grpcRoutesBlockTmpl})
	}
	for _, w := range whole {
		content, err = spliceWholeBlock(content, w.name, w.tmpl, ctx)
		if err != nil {
			return fmt.Errorf("app.go: %s: %w", w.name, err)
		}
	}

	modelCtx := registryModel{
		Name:              model.Name,
		Lower:             model.Lower(),
		NoHandler:         model.NoHandler,
		Ops:               model.Ops,
		MiddlewareLiteral: buildCRUDMiddlewareLiteral(model.Middleware),
	}
	svcBlock, err := renderTemplate(serviceWireLineTmpl, modelCtx)
	if err != nil {
		return fmt.Errorf("app.go: service-wire: %w", err)
	}
	content, err = spliceModelBlock(content, "service-wire", model.Name, svcBlock)
	if err != nil {
		return fmt.Errorf("app.go: service-wire: %w", err)
	}

	if model.NoHandler {
		content = removeModelBlock(content, "handler-wire", model.Name)
	} else {
		hdlBlock, err := renderTemplate(ssrHandlerWireLineTmpl, modelCtx)
		if err != nil {
			return fmt.Errorf("app.go: handler-wire: %w", err)
		}
		content, err = spliceModelBlock(content, "handler-wire", model.Name, hdlBlock)
		if err != nil {
			return fmt.Errorf("app.go: handler-wire: %w", err)
		}
	}

	formatted, err := formatter.GoSource(content)
	if err != nil {
		return fmt.Errorf("app.go: %w", err)
	}
	g.recordDiff(rel, formatted, res)
	if !g.dryRun {
		if err := os.WriteFile(path, formatted, 0644); err != nil {
			return fmt.Errorf("app.go: %w", err)
		}
	}
	res.Overwritten = append(res.Overwritten, rel)
	return nil
}

// removeAppWiring deletes model's service-wire and handler-wire blocks from
// app.go (SSR mode), then rebuilds the whole-block sections from the
// (now-smaller) manifest — mirrors writeAppWiring but for Destroy.
func (g *Generator) removeAppWiring(model *parser.Model, res *Result) error {
	rel := filepath.Join("internal", "app", "app.go")
	path := filepath.Join(g.root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("app.go: %w", err)
	}
	content := string(data)
	content = removeModelBlock(content, "service-wire", model.Name)
	content = removeModelBlock(content, "handler-wire", model.Name)

	ctx := g.buildRegistryCtx()
	whole := []struct{ name, tmpl string }{
		{"imports", ssrImportsBlockTmpl},
		{"type-defs", ssrTypeDefsTmpl},
		{"stores-wire", ssrStoresWireTmpl},
		{"routes", ssrRoutesBlockTmpl},
	}
	if g.isGRPC() {
		whole = append(whole, struct{ name, tmpl string }{"grpc-wire", grpcWireTmpl}, struct{ name, tmpl string }{"grpc-routes", grpcRoutesBlockTmpl})
	}
	for _, w := range whole {
		content, err = spliceWholeBlock(content, w.name, w.tmpl, ctx)
		if err != nil {
			return fmt.Errorf("app.go: %s: %w", w.name, err)
		}
	}

	formatted, err := formatter.GoSource(content)
	if err != nil {
		return fmt.Errorf("app.go: %w", err)
	}
	g.recordDiff(rel, formatted, res)
	if !g.dryRun {
		if err := os.WriteFile(path, formatted, 0644); err != nil {
			return fmt.Errorf("app.go: %w", err)
		}
	}
	res.Overwritten = append(res.Overwritten, rel)
	return nil
}

// ---- Routes ----

// writeRoutes regenerates internal/app/routes_gen.go with the model HTTP (and
// gRPC) registrations. app.go is hand-written and simply calls the generated
// a.registerGeneratedRoutes(mux) — no markers (yet; see the wiring-markers
// plan for making this per-model additive instead of whole-file overwrite).
func (g *Generator) writeRoutes(res *Result) error {
	models := make([]registryModel, 0, len(g.manifest.Models))
	for name, entry := range g.manifest.Models {
		models = append(models, registryModel{Name: name, Lower: strings.ToLower(name), NoHandler: entry.NoHandler})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })

	rel := filepath.Join("internal", "app", "routes_gen.go")
	return g.writeGoFile(rel, routesGenTmpl, registryCtx{
		ModulePath: g.modulePath,
		Models:     models,
		GRPC:       g.isGRPC(),
		IsSSR:      g.isSSR(),
	}, true, res)
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

func goTypeSize(goType string) int {
	if strings.HasPrefix(goType, "[]") {
		return 24
	}
	if strings.HasPrefix(goType, "*") {
		return 8
	}
	switch goType {
	case "time.Time":
		return 24
	case "string":
		return 16
	case "int", "int64", "float64":
		return 8
	case "json.RawMessage":
		return 24
	case "bool":
		return 1
	default:
		return 8
	}
}

func buildFieldLines(fields []templateField, db string, softDelete bool) string {
	allFields := make([]templateField, len(fields), len(fields)+4)
	copy(allFields, fields)

	// System fields are always present.
	allFields = append(allFields,
		templateField{GoName: "ID", GoType: "string", Name: "id"},
		templateField{GoName: "CreatedAt", GoType: "time.Time", Name: "created_at"},
		templateField{GoName: "UpdatedAt", GoType: "time.Time", Name: "updated_at"},
	)
	if softDelete {
		allFields = append(allFields, templateField{GoName: "DeletedAt", GoType: "*time.Time", Name: "deleted_at"})
	}

	// Sort fields from largest to smallest size to optimize memory alignment / struct packing.
	// In case of size ties, sort alphabetically by GoName to be deterministic.
	sort.Slice(allFields, func(i, j int) bool {
		sizeI := goTypeSize(allFields[i].GoType)
		sizeJ := goTypeSize(allFields[j].GoType)
		if sizeI != sizeJ {
			return sizeI > sizeJ
		}
		return allFields[i].GoName < allFields[j].GoName
	})

	var b strings.Builder
	for _, f := range allFields {
		if db == "postgres" {
			b.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\" db:\"%s\"`\n", f.GoName, f.GoType, f.Name, f.Name))
		} else {
			b.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\"`\n", f.GoName, f.GoType, f.Name))
		}
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

	mapUniqueTogether := func(ut [][]string) []compoundUniqueIndex {
		var out []compoundUniqueIndex
		for _, cols := range ut {
			out = append(out, compoundUniqueIndex{
				Name:    compoundIndexName(model.TableName, cols),
				Columns: cols,
				ColsCS:  strings.Join(cols, ", "),
			})
		}
		return out
	}

	addedUT, removedUT := diffUniqueTogether(model.PrevUniqueTogether, model.UniqueTogether)

	return migrationCtx{
		Name:                  model.Name,
		TableName:             model.TableName,
		Fields:                buildTemplateFields(model.Fields, db),
		Added:                 prepareAlterFields(buildTemplateFields(added, db), db),
		Removed:               prepareAlterFields(buildTemplateFields(removed, db), db),
		IDDef:                 idDef,
		IsPostgres:            db == "postgres",
		SoftDelete:            model.SoftDelete,
		SoftDeleteJustEnabled: model.SoftDeleteJustEnabled,
		UniqueTogether:        mapUniqueTogether(model.UniqueTogether),
		AddedUniqueTogether:   mapUniqueTogether(addedUT),
		RemovedUniqueTogether: mapUniqueTogether(removedUT),
	}
}

func compoundIndexName(tableName string, cols []string) string {
	return fmt.Sprintf("idx_%s_%s_unique", tableName, strings.Join(cols, "_"))
}

func diffUniqueTogether(prev, curr [][]string) (added, removed [][]string) {
	prevMap := make(map[string][]string)
	for _, cols := range prev {
		prevMap[strings.Join(cols, ",")] = cols
	}
	currMap := make(map[string][]string)
	for _, cols := range curr {
		currMap[strings.Join(cols, ",")] = cols
	}

	for _, cols := range curr {
		k := strings.Join(cols, ",")
		if _, exists := prevMap[k]; !exists {
			added = append(added, cols)
		}
	}
	for _, cols := range prev {
		k := strings.Join(cols, ",")
		if _, exists := currMap[k]; !exists {
			removed = append(removed, cols)
		}
	}
	return
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

// spliceWholeBlock renders tmpl (whose own text carries its
// "// scaffold:<name>:start"/"// scaffold:<name>:end" marker comments) with
// ctx and replaces the matching span in content. Used for app.go sections
// rebuilt from the full manifest on every `scaffold gen` — struct field
// lists, store wiring, route mounting — where there's no realistic
// hand-edit target, so full replacement is safe. The span must already
// exist (seeded at init); this never inserts fresh.
func spliceWholeBlock(content, name, tmpl string, ctx any) (string, error) {
	block, err := renderTemplate(tmpl, ctx)
	if err != nil {
		return "", err
	}
	startMark := "// scaffold:" + name + ":start"
	endMark := "// scaffold:" + name + ":end"
	return replaceMarkerBlock(content, startMark, endMark, block)
}

// spliceModelBlock replaces model's existing "// scaffold:<family>:<model>:
// start/end" span in content with block, or — the first time this model is
// generated — inserts it right before the family's fixed
// "// scaffold:<family>:insert" anchor comment. Indentation is sloppy on
// insert by design; gofmt normalizes the whole file afterward.
func spliceModelBlock(content, family, modelName, block string) (string, error) {
	startMark := fmt.Sprintf("// scaffold:%s:%s:start", family, modelName)
	endMark := fmt.Sprintf("// scaffold:%s:%s:end", family, modelName)
	if updated, err := replaceMarkerBlock(content, startMark, endMark, block); err == nil {
		return updated, nil
	}
	anchor := "// scaffold:" + family + ":insert"
	idx := strings.Index(content, anchor)
	if idx == -1 {
		return "", fmt.Errorf("insertion anchor not found: %q", anchor)
	}
	return content[:idx] + block + "\n\t" + content[idx:], nil
}

// removeModelBlock deletes model's "// scaffold:<family>:<model>:start/end"
// span, if present — a no-op (not an error) when the model never had one
// (e.g. --no-handler models have no handler-wire block to remove).
func removeModelBlock(content, family, modelName string) string {
	startMark := fmt.Sprintf("// scaffold:%s:%s:start", family, modelName)
	endMark := fmt.Sprintf("// scaffold:%s:%s:end", family, modelName)
	updated, err := removeMarkerBlock(content, startMark, endMark)
	if err != nil {
		return content
	}
	return updated
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

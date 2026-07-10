package scaffold

import (
	"fmt"
	"os"
	"strings"

	"github.com/esrid/scaffold/internal/generator"
	"github.com/esrid/scaffold/internal/parser"
	"github.com/spf13/cobra"
)

var (
	dryRun           bool
	tableName        string
	removeFields     []string
	noHandler        bool
	skipOps          []string
	onlyOps          []string
	regenViews       bool
	diffMode         bool
	softDelete       bool
	uniqueTogether   []string
	middlewareSpecs  []string
	removeMiddleware []string
	forceChanged     bool
)

var genCmd = &cobra.Command{
	Use:   "gen <Model> [field:type{modifier}!...]",
	Short: "Generate or update scaffold for a model",
	Long: `Generate or update the full CRUD scaffold for a model.

Must be run from inside a project created by "scaffold init" (looks for .scaffold/models.json).
Running gen again on an existing model MERGES the fields you pass into the stored set:
a name that already exists is updated in place, a new name is added, and fields you do
not mention are kept (passing a subset never drops columns). Use --remove to drop a field.
Each change writes a diff migration, prefixed with an "-- Action: ..." comment stating
what happened. Routes are mounted automatically (in app.go's markers for SSR, or
routes_gen.go for REST/gRPC).

Changing the TYPE or NULLABILITY of an existing field is the one case with no
generated migration (column changes are DB-specific and lossy) — the command fails
(exit 1) until you write the ALTER TABLE by hand and re-run with --force.

FIELD SYNTAX
  field:type            nullable field (Go pointer, e.g. *string)
  field:type!           NOT NULL field
  field:type,mod        field with a modifier
  field:type,mod,mod!   multiple modifiers, NOT NULL

  Modifiers may be written comma-separated (field:type,mod,mod) OR in braces
  (field:type{mod,mod}). The comma form needs no shell quoting; the brace form
  must be quoted in zsh/bash. Both are equivalent.

  Do NOT declare id, created_at, or updated_at — they are auto-managed.

TYPES
  string, text        TEXT (VARCHAR(n) when a size modifier is given)
  int                 INTEGER
  int64               BIGINT
  float, float64      REAL / DOUBLE PRECISION
  bool                BOOLEAN (postgres) / INTEGER (sqlite)
  json                TEXT / JSONB
  time, datetime      DATETIME / TIMESTAMPTZ
  []<type>            array of any scalar type above except time/json
                      (Go slice; native array on postgres, JSON-encoded TEXT on sqlite)
                      shell-safe equivalent: <type>,array  (e.g. tags:string,array)

MODIFIERS  (comma-separated after the type, or inside {…})
  nn                  NOT NULL — alias for ! suffix
  array (or arr)      make the field a slice — shell-safe alt to []type
  unique              UNIQUE constraint
  index               separate CREATE INDEX
  <n>                 VARCHAR(n) for string/text, e.g. ,92
  default=val         DEFAULT 'val'
  fk=table            REFERENCES table(id)
  cascade             ON DELETE CASCADE  (requires fk=; mutually exclusive with setnull)
  setnull             ON DELETE SET NULL (requires fk=; mutually exclusive with cascade)
  check=expr          CHECK (expr) — raw SQL expression (quote in shell: '>' '<' need it)

GENERATED FILES  (Model = "Product" → snake = "product", plural = "products")

  Always (all modes):
    internal/core/domain/product_gen.go           struct + GetID/WithID — always regenerated
    internal/core/domain/product.go               Validate() + custom methods — written once
    internal/core/ports/product.go                interfaces — written once
    internal/core/services/product_service_gen.go CRUD delegation — always regenerated
    internal/core/services/product_service.go     your business logic — never overwritten
    internal/adapters/store/product_store_gen.go  generated SQL — always regenerated
    internal/adapters/store/product_store.go      your custom queries — never overwritten
    internal/adapters/store/migrations/           numbered SQL migration file

    REST/gRPC: internal/app/registry.go and routes_gen.go — always regenerated
               in full.
    SSR: no registry.go/routes_gen.go — the same wiring is rewritten inline in
         internal/app/app.go, scoped to this model's // scaffold:*:start/end
         marker spans. Everything else in app.go is untouched.

  SSR mode only:
    internal/adapters/http/product_handler_gen.go SSR handler + typed bindForm — always regenerated
    internal/adapters/http/product_handler.go     your extensions — never overwritten;
                                                  registerCustomRoutes(mux) here is always
                                                  called from the generated Router()
    web/views/product.templ (--ssr-engine templ) or
    web/templates/product.html (--ssr-engine html) — List/Form/Show — WRITE-ONCE
                                                  (created once, then yours; use --regen-views to refresh.
                                                  "templ generate" is run for you on the templ engine)

  gRPC mode only:
    internal/adapters/grpc/pb/product.proto       protobuf definition — always regenerated
    internal/adapters/grpc/product_handler_gen.go gRPC handler — always regenerated
    internal/adapters/grpc/shared.go              error translation — written once

ROUTES (stdlib net/http.ServeMux, Go 1.22+ method+pattern syntax; no router dependency)

  REST:  mux.Handle("GET /api/products", …)   → GET / GET/{id} / POST / PUT/{id} / DELETE/{id}
  SSR:   mux.Handle("GET /products", …)       → GET / GET/new / GET/{id} / GET/{id}/edit /
                                                 POST / POST/{id} / DELETE/{id}
  gRPC:  REST routes + gRPC ProductService on :50051 (after make proto)

EXAMPLES
  # Basic model
  scaffold gen Product name:string! price:float! sku:string,unique

  # Nullable field (no !) → Go pointer *string
  scaffold gen Article title:string! body:string views:int

  # NOT NULL via nn modifier
  scaffold gen Order status:string,default=pending,nn

  # VARCHAR(n) — fixed-length string column
  scaffold gen User username:string,92! email:string,255,unique!

  # CHECK constraint
  scaffold gen Product price:float,check=price>0! stock:int,check=stock>=0

  # Foreign key with cascade delete
  scaffold gen Post user_id:string,fk=users,cascade! title:string!

  # Foreign key with set-null on delete
  scaffold gen Comment author_id:string,fk=users,setnull body:string!

  # FK + cascade + index (modifiers combine freely)
  scaffold gen Post user_id:string,fk=users,cascade,index! title:string!

  # JSON field (JSONB on postgres, TEXT on sqlite)
  scaffold gen Event payload:json! metadata:json

  # Array fields (Go []string/[]int; TEXT[] on postgres, JSON TEXT on sqlite)
  scaffold gen Post title:string! tags:string,array! scores:int,array

  # Add a field to an existing model (kept fields stay; generates ALTER TABLE migration)
  scaffold gen Product stock:int

  # Drop a field from an existing model (generates DROP COLUMN migration)
  scaffold gen Product --remove stock

  # Skip CRUD operations — routes & view affordances are not generated
  # (ops: list, read, create, update, delete). Useful when records are created
  # elsewhere (e.g. by a background job) or for read-only resources.
  scaffold gen Step title:string! --skip create,delete   # no create/delete
  scaffold gen Report title:string! --only list,read      # read-only resource

  # Preview changes without writing any files
  scaffold gen Product name:string! price:float! --dry-run

  # Override the auto-pluralized table name
  scaffold gen Person name:string! --table-name people`,
	Args: cobra.MinimumNArgs(1),
	RunE: runGen,
}

func init() {
	genCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without writing files")
	genCmd.Flags().StringVar(&tableName, "table-name", "", "Override auto-pluralized table name")
	genCmd.Flags().StringSliceVar(&removeFields, "remove", nil, "Field name(s) to drop from an existing model (comma-separated or repeated)")
	genCmd.Flags().BoolVar(&noHandler, "no-handler", false, "Skip generating HTTP/gRPC handlers and routes")
	genCmd.Flags().StringSliceVar(&skipOps, "skip", nil, "CRUD ops to NOT generate (list,read,create,update,delete)")
	genCmd.Flags().StringSliceVar(&onlyOps, "only", nil, "Generate ONLY these CRUD ops (mutually exclusive with --skip)")
	genCmd.Flags().BoolVar(&regenViews, "regen-views", false, "Overwrite SSR views (default: write-once — views are never clobbered once created)")
	genCmd.Flags().BoolVar(&diffMode, "diff", false, "Show a unified diff of what would change, without writing any files")
	genCmd.Flags().BoolVar(&softDelete, "soft-delete", false, "Enable soft deletion (stores deletion timestamp in deleted_at field)")
	genCmd.Flags().StringArrayVar(&uniqueTogether, "unique-together", nil, "Define compound unique constraint(s) (comma-separated fields, e.g. 'name,category')")
	genCmd.Flags().StringArrayVar(&middlewareSpecs, "middleware", nil, "Wrap an op's route with middleware: op:Func1,Func2 (op: list,read,create,update,delete,all). Repeatable. Sticky across regeneration.")
	genCmd.Flags().StringSliceVar(&removeMiddleware, "remove-middleware", nil, "Op name(s) to clear middleware from (comma-separated or repeated; 'all' clears everything)")
	genCmd.Flags().BoolVar(&forceChanged, "force", false, "Acknowledge in-place field changes (type/nullability) with no matching migration, and exit 0 anyway — you must write the ALTER TABLE yourself")
	rootCmd.AddCommand(genCmd)
}

func runGen(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("model name required")
	}

	modelName := args[0]
	fieldArgs := args[1:]

	root, modulePath, err := parser.FindProjectRoot()
	if err != nil {
		return err
	}

	fields, err := parser.ParseFields(fieldArgs)
	if err != nil {
		return err
	}

	manifest, err := parser.LoadManifest(root)
	if err != nil {
		return err
	}
	manifest.Module = modulePath

	var useSoftDelete bool
	if cmd.Flags().Changed("soft-delete") {
		useSoftDelete = softDelete
	} else if existing, exists := manifest.Models[modelName]; exists {
		useSoftDelete = existing.SoftDelete
	}

	var useUniqueTogether [][]string
	if cmd.Flags().Changed("unique-together") {
		for _, ut := range uniqueTogether {
			parts := strings.Split(ut, ",")
			var cleanParts []string
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					cleanParts = append(cleanParts, trimmed)
				}
			}
			if len(cleanParts) > 1 {
				useUniqueTogether = append(useUniqueTogether, cleanParts)
			}
		}
	} else if existing, exists := manifest.Models[modelName]; exists {
		useUniqueTogether = existing.UniqueTogether
	}

	useMiddleware := map[string][]string{}
	if existing, exists := manifest.Models[modelName]; exists {
		useMiddleware = existing.Middleware
	}
	if cmd.Flags().Changed("middleware") {
		newMW, err := parser.ParseMiddlewareFlags(middlewareSpecs)
		if err != nil {
			return err
		}
		useMiddleware = parser.MergeMiddleware(useMiddleware, parser.ExpandAllMiddleware(newMW))
	}
	if cmd.Flags().Changed("remove-middleware") {
		useMiddleware, err = parser.RemoveMiddlewareOps(useMiddleware, removeMiddleware)
		if err != nil {
			return err
		}
	}

	model, err := parser.BuildModel(modelName, fields, removeFields, manifest, tableName, noHandler)
	if err != nil {
		return err
	}

	model.SoftDelete = useSoftDelete
	model.UniqueTogether = useUniqueTogether
	model.Middleware = useMiddleware
	if existing, exists := manifest.Models[modelName]; exists {
		model.PrevSoftDelete = existing.SoftDelete
		model.SoftDeleteJustEnabled = model.SoftDelete && !model.PrevSoftDelete
		model.PrevUniqueTogether = existing.UniqueTogether
		if model.SoftDelete != model.PrevSoftDelete || !equalUniqueTogether(model.UniqueTogether, model.PrevUniqueTogether) {
			model.MigrationVersion = parser.NextMigrationVersion(manifest)
		}
	}

	for _, w := range model.Warnings {
		fmt.Printf("⚠ Warning: %s\n", w)
	}

	// --only / --skip override the model's CRUD ops for this run (authoritative).
	if len(onlyOps) > 0 || len(skipOps) > 0 {
		ops, err := parser.ResolveOps(onlyOps, skipOps)
		if err != nil {
			return err
		}
		model.Ops = ops
	}

	// Add the model to the manifest before scaffolding so writeRegistry sees it.
	manifest.Models[modelName] = model.ManifestEntry()

	// --diff implies a dry run: nothing is written, no manifest save, no templ gen.
	if diffMode {
		dryRun = true
	}

	g := generator.New(root, modulePath, manifest, dryRun)
	g.RegenViews = regenViews
	g.Diff = diffMode
	result, err := g.Scaffold(model)
	if err != nil {
		return err
	}
	result.Print(os.Stdout)

	// A changed field definition (type, nullability, modifiers) is updated in
	// the generated code and schema.sql, but scaffold cannot safely emit the
	// matching ALTER (column type changes are DB-specific and lossy, exactly
	// why Rails/Ecto don't auto-generate them either) — so instead of a
	// warning that scrolls by unnoticed, this fails the command (like Rails'
	// PendingMigrationError) so CI/pre-commit catches a prod/code mismatch
	// before it ships. --force acknowledges you've written the migration
	// yourself (or will, before deploying) and lets the command exit 0.
	// dry-run/--diff write nothing, so there's nothing to drift yet — warn
	// but don't fail, or previewing a type change in CI would itself fail.
	if len(model.ChangedFields) > 0 && !forceChanged && !dryRun {
		fmt.Printf("⚠ Field definition changed in place: %s\n", strings.Join(model.ChangedFields, ", "))
		fmt.Println("  Generated code and schema.sql were updated, but NO migration was written.")
		fmt.Println("  Write a manual migration in internal/adapters/store/migrations/ to alter the column(s),")
		fmt.Println("  then re-run with --force to acknowledge and let this command succeed.")
		return fmt.Errorf("field definition changed with no migration: %s", strings.Join(model.ChangedFields, ", "))
	}
	if len(model.ChangedFields) > 0 && dryRun {
		fmt.Printf("⚠ Field definition changed in place (preview only, no migration would be written): %s\n", strings.Join(model.ChangedFields, ", "))
	} else if len(model.ChangedFields) > 0 && forceChanged {
		fmt.Printf("⚠ Field definition changed in place (acknowledged via --force): %s\n", strings.Join(model.ChangedFields, ", "))
	}

	if !dryRun {
		if err := parser.SaveManifest(root, manifest); err != nil {
			return err
		}
		// SSR views are templ components — regenerate the *_templ.go so the
		// project builds without a separate manual `templ generate` step.
		if manifest.APIMode == "ssr" {
			if err := runTemplGenerate(root); err != nil {
				return err
			}
		}
	}

	return nil
}

func equalUniqueTogether(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

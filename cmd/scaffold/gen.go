package scaffold

import (
	"fmt"
	"os"

	"github.com/esrid/scaffold/internal/generator"
	"github.com/esrid/scaffold/internal/parser"
	"github.com/spf13/cobra"
)

var (
	dryRun       bool
	tableName    string
	removeFields []string
)

var genCmd = &cobra.Command{
	Use:   "gen <Model> [field:type{modifier}!...]",
	Short: "Generate or update scaffold for a model",
	Long: `Generate or update the full CRUD scaffold for a model.

Must be run from inside a project created by "scaffold init" (looks for .scaffold/models.json).
Running gen again on an existing model MERGES the fields you pass into the stored set:
a name that already exists is updated in place, a new name is added, and fields you do
not mention are kept (passing a subset never drops columns). Use --remove to drop a field.
Each change writes a diff migration. Routes are mounted automatically in app.go.

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
    internal/core/domain/product.go               struct + Validate() — fields patched via markers
    internal/core/ports/product.go                interfaces — written once
    internal/core/services/product_service_gen.go CRUD delegation — always regenerated
    internal/core/services/product_service.go     your business logic — never overwritten
    internal/adapters/store/product_store_gen.go  generated SQL — always regenerated
    internal/adapters/store/product_store.go      your custom queries — never overwritten
    internal/app/registry.go                      dependency wiring — always regenerated
    internal/app/app.go                           route block updated
    internal/adapters/store/migrations/           numbered SQL migration file

  SSR mode only:
    internal/adapters/http/product_handler_gen.go SSR handler + typed bindForm — always regenerated
    internal/adapters/http/product_handler.go     your extensions — never overwritten
    web/views/product.templ                       templ List/Form/Show components — regenerated on field changes
                                                  (run "templ generate" — scaffold does this for you)

  gRPC mode only:
    api/proto/v1/product.proto                    protobuf definition — always regenerated
    internal/adapters/grpc/product_handler_gen.go gRPC handler — always regenerated
    internal/adapters/grpc/shared.go              error translation — written once

ROUTES MOUNTED IN app.go

  REST:  r.Route("/api/products", …)   → GET / GET/{id} / POST / PUT/{id} / DELETE/{id}
  SSR:   r.Mount("/products", …)       → GET / GET/new / GET/{id} / GET/{id}/edit /
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

	model, err := parser.BuildModel(modelName, fields, removeFields, manifest, tableName)
	if err != nil {
		return err
	}

	// Add the model to the manifest before scaffolding so writeRegistry sees it.
	manifest.Models[modelName] = model.ManifestEntry()

	g := generator.New(root, modulePath, manifest, dryRun)
	result, err := g.Scaffold(model)
	if err != nil {
		return err
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

	result.Print(os.Stdout)
	return nil
}

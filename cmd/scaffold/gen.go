package scaffold

import (
	"fmt"
	"os"

	"github.com/esrid/scaffold/internal/generator"
	"github.com/esrid/scaffold/internal/parser"
	"github.com/spf13/cobra"
)

var (
	dryRun    bool
	tableName string
)

var genCmd = &cobra.Command{
	Use:   "gen <Model> [field:type{modifier}!...]",
	Short: "Generate or update scaffold for a model",
	Long: `Generate or update the full CRUD scaffold for a model.

Must be run from inside a project created by "scaffold init" (looks for .scaffold/models.json).
Running gen again on an existing model adds/removes fields and writes a diff migration.

FIELD SYNTAX
  field:type          nullable field (Go pointer, e.g. *string)
  field:type!         NOT NULL field (required value)
  field:type{mod}     field with a modifier
  field:type{mod,mod}! multiple modifiers, NOT NULL

  Do NOT declare id, created_at, or updated_at — they are auto-managed.

TYPES
  string, text        TEXT (VARCHAR(n) when a size modifier is given)
  int                 INTEGER
  int64               BIGINT
  float, float64      REAL / DOUBLE PRECISION
  bool                BOOLEAN (postgres) / INTEGER (sqlite)
  json                TEXT / JSONB
  time, datetime      DATETIME / TIMESTAMPTZ

MODIFIERS  (go inside {…}, comma-separated)
  nn                  NOT NULL — alias for ! suffix
  unique              UNIQUE constraint
  index               separate CREATE INDEX
  <n>                 VARCHAR(n) for string/text, e.g. {92}
  default=val         DEFAULT 'val'
  fk=table            REFERENCES table(id)
  cascade             ON DELETE CASCADE  (requires fk=; mutually exclusive with setnull)
  setnull             ON DELETE SET NULL (requires fk=; mutually exclusive with cascade)
  check=expr          CHECK (expr) — raw SQL expression

GENERATED FILES  (Model = "Product" → snake = "product", plural = "products")
  internal/core/domain/product.go          struct + Validate() — fields patched via markers
  internal/core/ports/product.go           repository interface — written once, never touched
  internal/core/services/product_service_gen.go  CRUD delegation — always regenerated
  internal/core/services/product_service.go      your business logic — never overwritten
  internal/adapters/store/product_store_gen.go   generated queries — always regenerated
  internal/adapters/store/product_store.go       your custom queries — never overwritten
  internal/app/registry.go                 wiring — always regenerated
  internal/adapters/store/migrations/      numbered SQL migration file

REST ROUTES REGISTERED
  GET    /api/products
  GET    /api/products/{id}
  POST   /api/products
  PUT    /api/products/{id}
  DELETE /api/products/{id}

EXAMPLES
  # Basic model
  scaffold gen Product name:string! price:float! sku:string{unique}

  # Nullable field (no !) → Go pointer *string
  scaffold gen Article title:string! body:string views:int

  # NOT NULL via nn modifier
  scaffold gen Order status:string{nn,default=pending}

  # VARCHAR(n) — fixed-length string column
  scaffold gen User username:string{92}! email:string{255,unique}!

  # CHECK constraint
  scaffold gen Product price:float{check=price>0}! stock:int{check=stock>=0}

  # Foreign key with cascade delete
  scaffold gen Post user_id:string{fk=users,cascade}! title:string!

  # Foreign key with set-null on delete
  scaffold gen Comment author_id:string{fk=users,setnull} body:string!

  # FK + cascade + index (modifiers combine freely)
  scaffold gen Post user_id:string{fk=users,cascade,index}! title:string!

  # JSON field (JSONB on postgres, TEXT on sqlite)
  scaffold gen Event payload:json! metadata:json

  # Add a field to an existing model (generates ALTER TABLE migration)
  scaffold gen Product name:string! price:float! sku:string{unique} stock:int

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

	model, err := parser.BuildModel(modelName, fields, manifest, tableName)
	if err != nil {
		return err
	}

	g := generator.New(root, modulePath, manifest, dryRun)
	result, err := g.Scaffold(model)
	if err != nil {
		return err
	}

	if !dryRun {
		manifest.Models[modelName] = model.ManifestEntry()
		if err := parser.SaveManifest(root, manifest); err != nil {
			return err
		}
	}

	result.Print(os.Stdout)
	return nil
}

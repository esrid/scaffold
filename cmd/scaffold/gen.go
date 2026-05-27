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
	Long: `Generate or update the full CRUD scaffold for a model (domain, ports, service, store, migration, registry).

Running gen again on an existing model adds/removes fields and creates a diff migration.

FIELD SYNTAX
  field:type          nullable field (Go pointer, e.g. *string)
  field:type!         NOT NULL field (required value)
  field:type{unique}  adds UNIQUE constraint
  field:type{index}   adds an index
  field:type{unique,index}  both constraints

TYPES
  string    TEXT
  int       INTEGER / BIGINT
  float     REAL / DOUBLE PRECISION
  bool      BOOLEAN (postgres) / INTEGER (sqlite)
  json      JSON / JSONB
  time      DATETIME / TIMESTAMPTZ

EXAMPLES
  # Create a Product model with three fields
  scaffold gen Product name:string! price:float! sku:string{unique}

  # Nullable fields (no !) become Go pointers (*string, *int, ...)
  scaffold gen Article title:string! body:string views:int

  # JSON field (stored as JSONB on postgres, JSON on sqlite)
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

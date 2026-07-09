package scaffold

import (
	"fmt"
	"os"

	"github.com/esrid/scaffold/internal/generator"
	"github.com/esrid/scaffold/internal/parser"
	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy <Model>",
	Short: "Remove all scaffold files for a model",
	Long: `Remove all scaffold files for a model and create a DROP TABLE migration.

Deletes all files for the model — generated and user-owned — and removes it from the
registry and manifest. A DROP TABLE migration is written so the schema stays in sync.
Routes and wiring are removed automatically (from app.go's // scaffold: markers in
SSR mode, or registry.go/routes_gen.go in REST/gRPC mode).

FILES DELETED
  Always (all modes):
    internal/core/domain/{model}_gen.go
    internal/core/domain/{model}.go
    internal/core/ports/{model}.go
    internal/core/services/{model}_service_gen.go
    internal/core/services/{model}_service.go    ← contains your custom logic
    internal/adapters/store/{model}_store_gen.go
    internal/adapters/store/{model}_store.go     ← contains your custom queries

  SSR mode:
    internal/adapters/http/{model}_handler_gen.go
    internal/adapters/http/{model}_handler.go    ← contains your custom handler methods
    web/views/{model}.templ                      ← --ssr-engine templ: templ components
    web/views/{model}_templ.go                   ← templ-generated, if "make generate" was run
    web/templates/{model}.html                   ← --ssr-engine html: html/template views

  gRPC mode:
    internal/adapters/grpc/pb/{model}.proto
    internal/adapters/grpc/pb/{model}.pb.go      ← buf-generated, if "make proto" was run
    internal/adapters/grpc/pb/{model}_grpc.pb.go ← buf-generated, if "make proto" was run
    internal/adapters/grpc/{model}_handler_gen.go

WARNING: {model}_service.go and {model}_store.go contain your hand-written logic.
Back them up before running destroy if you want to keep them.

You will be prompted to confirm before anything is deleted.

EXAMPLES
  scaffold destroy Product
  scaffold destroy Order`,
	Args: cobra.ExactArgs(1),
	RunE: runDestroy,
}

var keepCustom bool
var forceDestroy bool

func init() {
	destroyCmd.Flags().BoolVar(&keepCustom, "keep-custom", false, "Keep user-owned custom files (only delete generated files)")
	destroyCmd.Flags().BoolVar(&forceDestroy, "force", false, "Force destruction even if other models reference this table")
	rootCmd.AddCommand(destroyCmd)
}

func runDestroy(cmd *cobra.Command, args []string) error {
	modelName := args[0]

	root, modulePath, err := parser.FindProjectRoot()
	if err != nil {
		return err
	}

	manifest, err := parser.LoadManifest(root)
	if err != nil {
		return err
	}

	entry, ok := manifest.Models[modelName]
	if !ok {
		return fmt.Errorf("model %q not found in manifest", modelName)
	}

	model, err := parser.ModelFromEntry(modelName, entry, manifest)
	if err != nil {
		return err
	}

	// Confirm destruction
	fmt.Printf("This will delete scaffold files for %s and cannot be undone.\n", modelName)
	if !keepCustom {
		fmt.Printf("Custom methods in %s_service.go and %s_store.go will be lost (but backed up to .scaffold/backups/).\n",
			model.Snake(), model.Snake())
	} else {
		fmt.Printf("Custom files like %s_service.go and %s_store.go will be kept.\n",
			model.Snake(), model.Snake())
	}
	fmt.Print("Proceed? (y/N) ")

	var input string
	fmt.Scanln(&input)
	if input != "y" && input != "Y" {
		fmt.Println("Aborted.")
		return nil
	}

	g := generator.New(root, modulePath, manifest, false)
	g.KeepCustom = keepCustom
	g.Force = forceDestroy
	result, err := g.Destroy(model)
	if err != nil {
		return err
	}

	delete(manifest.Models, modelName)
	if err := parser.SaveManifest(root, manifest); err != nil {
		return err
	}

	result.Print(os.Stdout)
	return nil
}

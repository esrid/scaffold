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
	Long: `Remove all generated scaffold files for a model and create a DROP TABLE migration.

Deletes: domain file, ports file, generated service, generated store, user store stub,
user service stub, and adds the model to the registry. A DROP TABLE migration is
written so the schema stays in sync.

WARNING: custom logic in {model}_service.go and {model}_store.go will be lost.
You will be prompted to confirm before anything is deleted.

EXAMPLES
  # Remove the Product model (prompts for confirmation)
  scaffold destroy Product

  # Remove the Order model
  scaffold destroy Order`,
	Args: cobra.ExactArgs(1),
	RunE: runDestroy,
}

func init() {
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

	model, err := parser.ModelFromEntry(modelName, entry)
	if err != nil {
		return err
	}

	// Confirm destruction
	fmt.Printf("This will delete scaffold files for %s and cannot be undone.\n", modelName)
	fmt.Printf("Custom methods in %s_service.go and %s_store.go will be lost.\n",
		model.Snake(), model.Snake())
	fmt.Print("Proceed? (y/N) ")

	var input string
	fmt.Scanln(&input)
	if input != "y" && input != "Y" {
		fmt.Println("Aborted.")
		return nil
	}

	g := generator.New(root, modulePath, manifest, false)
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

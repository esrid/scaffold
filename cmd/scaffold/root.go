package scaffold

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Code generator for Go hexagonal architecture",
	Long: `scaffold — CLI code generator for production-ready Go REST APIs.

Bootstraps a hexagonal-architecture project (domain / ports / services / store / http)
and lets you add, modify, or remove models without touching hand-written code.

WORKFLOW
  1. scaffold init myapp --module github.com/you/myapp --db sqlite|postgres
  2. cd myapp && make run          # dev server on :8080
  3. scaffold gen <Model> [fields] # full CRUD in seconds
  4. scaffold gen <Model> [fields] # run again to add/remove fields
  5. scaffold destroy <Model>      # tear down a model

Run "scaffold <command> --help" for full field syntax, modifiers, and examples.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package scaffold

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/esrid/scaffold/internal/generator/boilerplate"
	"github.com/esrid/scaffold/internal/parser"
	"github.com/spf13/cobra"
)

var (
	initModule  string
	initDB      string
	initGRPC    bool   // legacy alias for --api grpc
	initAPIMode string // "ssr" | "rest" | "grpc"
)

var initCmd = &cobra.Command{
	Use:   "init [dir]",
	Short: "Bootstrap a new project from the boilerplate",
	Long: `Bootstrap a complete Go hexagonal-architecture project from the built-in boilerplate.

Creates the target directory, writes all source files, runs go mod tidy, and writes
.scaffold/models.json so "scaffold gen" can track models immediately.

GENERATED PROJECT STRUCTURE
  myapp/
  ├── main.go
  ├── Makefile
  ├── .env.example
  ├── .scaffold/models.json        manifest — tracks all generated models
  └── internal/
      ├── app/
      │   ├── app.go               wires everything together
      │   ├── config.go            env-based config
      │   └── registry.go          auto-regenerated on every gen/destroy
      ├── core/
      │   ├── domain/errors.go     NotFoundError, ValidationError, etc.
      │   ├── ports/               repository interfaces (one file per model)
      │   └── services/            service stubs (one gen + one user file per model)
      └── adapters/
          ├── http/
          │   ├── crud_handler.go  generic Chi CRUD handler (REST) or per-model handler (SSR)
          │   └── middleware.go
          └── store/
              ├── schema.sql       full schema
              ├── migrations/      numbered SQL migration files
              └── {model}_store*.go  generated + user store files

API MODES
  --api ssr   (default) templ + HTMX server-side rendering
  --api rest  JSON API with generic CRUDHandler[T]
  --api grpc  gRPC server (REST + gRPC hybrid)

MAKEFILE TARGETS (REST/gRPC mode)
  make run       build frontend + run server (go run) on :8080
  make build     build frontend + compile binary to bin/server
  make build-fe  esbuild TypeScript + CSS only
  make clean     remove web/dist and bin/

MAKEFILE TARGETS (SSR mode)
  make run       run server (go run) on :8080
  make build     compile binary to bin/server
  make clean     remove bin/

EXAMPLES
  # Create ./myapp with SSR mode (default) and SQLite
  scaffold init myapp --module github.com/yourname/myapp --db sqlite

  # Create ./myapp with REST API and Postgres
  scaffold init myapp --module github.com/yourname/myapp --db postgres --api rest

  # Create ./myapp with gRPC support
  scaffold init myapp --module github.com/yourname/myapp --db sqlite --api grpc

  # Directory defaults to the last segment of --module when omitted
  scaffold init --module github.com/yourname/myapp --db postgres
  # → creates ./myapp/

  # Omit --db to be prompted interactively
  scaffold init --module github.com/yourname/myapp

NEXT STEPS after init
  cd myapp
  make run                              # start the dev server on :8080
  scaffold gen Product name:string! price:float!   # add your first model`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initModule, "module", "", "Go module path (e.g. github.com/user/myapp)")
	initCmd.Flags().StringVar(&initDB, "db", "", "Database driver: sqlite or postgres")
	initCmd.Flags().StringVar(&initAPIMode, "api", "ssr", "API mode: ssr (templ+HTMX), rest (JSON API), or grpc")
	initCmd.Flags().BoolVar(&initGRPC, "grpc", false, "Enable gRPC support — alias for --api grpc (deprecated)")
	_ = initCmd.MarkFlagRequired("module")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	// Resolve target directory — default to last segment of module path
	dir := ""
	if len(args) == 1 {
		dir = args[0]
	} else {
		parts := strings.Split(initModule, "/")
		dir = parts[len(parts)-1]
	}
	var err error
	dir, err = filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("init: resolve dir: %w", err)
	}

	// Prompt for DB if not provided
	db := strings.ToLower(strings.TrimSpace(initDB))
	if db == "" {
		db, err = promptDB()
		if err != nil {
			return err
		}
	}
	if db != "sqlite" && db != "postgres" {
		return fmt.Errorf("invalid --db %q: must be sqlite or postgres", db)
	}

	// Resolve API mode — --grpc flag takes precedence over --api
	apiMode := strings.ToLower(strings.TrimSpace(initAPIMode))
	if initGRPC {
		apiMode = "grpc"
	}
	if apiMode != "ssr" && apiMode != "rest" && apiMode != "grpc" {
		return fmt.Errorf("invalid --api %q: must be ssr, rest, or grpc", apiMode)
	}

	// Refuse to scaffold into a non-empty directory: walkAndWrite overwrites
	// blindly, which would clobber an existing project. A lone .git/ (or other
	// dotfiles, e.g. from "git init" + a .gitignore) is allowed.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				return fmt.Errorf("init: directory %s is not empty — refusing to overwrite existing files", dir)
			}
		}
	}

	// Create target directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("init: mkdir: %w", err)
	}

	fmt.Printf("Initializing %s/%s project in %s...\n", db, apiMode, dir)

	// Generate boilerplate files
	if err := boilerplate.Generate(dir, initModule, db, apiMode); err != nil {
		return fmt.Errorf("init: generate: %w", err)
	}

	// Write .scaffold/models.json
	if err := writeInitManifest(dir, initModule, db, apiMode); err != nil {
		return fmt.Errorf("init: manifest: %w", err)
	}

	// SSR renders with templ: generate the *_templ.go files BEFORE go mod tidy so
	// tidy sees the templ runtime imports and keeps the dependency in go.mod.
	if apiMode == "ssr" {
		if err := runTemplGenerate(dir); err != nil {
			return fmt.Errorf("init: %w", err)
		}
	}

	// Run go mod tidy
	fmt.Println("Running go mod tidy...")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("init: go mod tidy: %w", err)
	}

	name := filepath.Base(dir)
	fmt.Printf("\n✓ Ready. Next steps:\n")
	if name != "." {
		fmt.Printf("  cd %s\n", name)
	}
	fmt.Printf("  make run\n")
	return nil
}

// runTemplGenerate runs `templ generate` in dir so the *_templ.go files exist
// for SSR projects. If the templ CLI is not installed it prints actionable
// guidance rather than failing — the project still builds once the user
// installs templ and runs `make generate` (or `templ generate`).
func runTemplGenerate(dir string) error {
	if _, err := exec.LookPath("templ"); err != nil {
		fmt.Println("⚠ templ CLI not found — SSR views were not generated.")
		fmt.Println("  Install it, then generate views before building:")
		fmt.Println("    go install github.com/a-h/templ/cmd/templ@latest")
		fmt.Println("    templ generate && go mod tidy")
		return nil
	}
	fmt.Println("Running templ generate...")
	gen := exec.Command("templ", "generate")
	gen.Dir = dir
	gen.Stdout = os.Stdout
	gen.Stderr = os.Stderr
	if err := gen.Run(); err != nil {
		return fmt.Errorf("templ generate: %w", err)
	}
	return nil
}

func promptDB() (string, error) {
	fmt.Print("Which database? (sqlite/postgres) [sqlite]: ")
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	v := strings.ToLower(strings.TrimSpace(line))
	if v == "" {
		return "sqlite", nil
	}
	return v, nil
}

func writeInitManifest(dir, module, db, apiMode string) error {
	m := parser.Manifest{
		Module:  module,
		DB:      db,
		GRPC:    apiMode == "grpc",
		APIMode: apiMode,
		Models:  map[string]parser.ManifestModel{},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	scaffoldDir := filepath.Join(dir, ".scaffold")
	if err := os.MkdirAll(scaffoldDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(scaffoldDir, "models.json"), data, 0644)
}

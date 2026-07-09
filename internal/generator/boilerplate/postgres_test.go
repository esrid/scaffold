//go:build integration

package boilerplate_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/esrid/scaffold/internal/generator"
	"github.com/esrid/scaffold/internal/generator/boilerplate"
	"github.com/esrid/scaffold/internal/parser"
)

// TestPostgres_REST_FullCRUD starts a real Postgres container, generates a REST project,
// runs migrations, starts the server and exercises the full CRUD cycle via HTTP.
func TestPostgres_REST_FullCRUD(t *testing.T) {
	ctx := context.Background()

	pgC, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := pgC.Terminate(ctx); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// ---- Generate project ----
	dir := t.TempDir()
	const module = "github.com/test/pgintegration"
	const addr = "127.0.0.1:17433"

	errGen := boilerplate.Generate(
		dir,
		module,
		"postgres",
		"rest",
		"templ",
	)
	if errGen != nil {
		t.Fatalf("boilerplate.Generate: %v", errGen)
	}

	manifest, err := parser.LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	manifest.Module = module
	manifest.DB = "postgres"
	manifest.APIMode = "rest"

	fields, _ := parser.ParseFields([]string{"name:string!", "price:float!"})
	model, _ := parser.BuildModel("Product", fields, nil, manifest, "", false)
	manifest.Models["Product"] = model.ManifestEntry()
	if err := parser.SaveManifest(dir, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	g := generator.New(dir, module, manifest, false)
	if _, err := g.Scaffold(model); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	// ---- go mod tidy ----
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy:\n%s\n%v", out, err)
	}

	// ---- build ----
	bin := filepath.Join(dir, "server")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build:\n%s\n%v", out, err)
	}

	// ---- run migrations + start server ----
	env := append(os.Environ(),
		"DATABASE_URL="+connStr,
		"SECRET_KEY=integration-test-secret",
		"ADDR="+addr,
		"APP_ENV=development",
	)

	srv := exec.CommandContext(ctx, bin)
	srv.Dir = dir
	srv.Env = env
	srv.Stdout = os.Stdout
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })

	// Poll until server accepts connections
	baseURL := "http://" + addr
	if err := waitForHTTP(baseURL+"/health", 10*time.Second); err != nil {
		t.Fatalf("server did not start: %v", err)
	}

	// ---- health check ----
	t.Run("health", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("health: got %d want 200", resp.StatusCode)
		}
	})

	// ---- list (empty) ----
	t.Run("list_empty", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/products")
		if err != nil {
			t.Fatalf("GET /api/products: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("list: got %d want 200", resp.StatusCode)
		}
	})

	// ---- create ----
	t.Run("create", func(t *testing.T) {
		body := strings.NewReader(`{"name":"Widget","price":9.99}`)
		resp, err := http.Post(baseURL+"/api/products", "application/json", body)
		if err != nil {
			t.Fatalf("POST /api/products: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("create: got %d want 201", resp.StatusCode)
		}
	})

	// ---- list (one item) ----
	t.Run("list_one_item", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/products")
		if err != nil {
			t.Fatalf("GET /api/products: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("list: got %d want 200", resp.StatusCode)
		}
	})
}

// TestPostgres_SSR_Compiles verifies that a Postgres/SSR project compiles correctly.
func TestPostgres_SSR_Compiles(t *testing.T) {
	ctx := context.Background()

	pgC, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { pgC.Terminate(ctx) })

	_, err = pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	dir := t.TempDir()
	const module = "github.com/test/pgssr"

	errGen := boilerplate.Generate(
		dir,
		module,
		"postgres",
		"ssr",
		"templ",
	)
	if errGen != nil {
		t.Fatalf("boilerplate.Generate: %v", errGen)
	}

	manifest, _ := parser.LoadManifest(dir)
	manifest.Module = module
	manifest.DB = "postgres"
	manifest.APIMode = "ssr"

	fields, _ := parser.ParseFields([]string{"title:string!", "content:string!", "published:bool!"})
	model, _ := parser.BuildModel("Article", fields, nil, manifest, "", false)
	manifest.Models["Article"] = model.ManifestEntry()
	parser.SaveManifest(dir, manifest)

	g := generator.New(dir, module, manifest, false)
	if _, err := g.Scaffold(model); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	// templ mode renders via compiled components (views.ArticleList etc, in
	// the generated *_templ.go) — this test drives the generator directly,
	// bypassing the CLI layer that normally runs this for you (see
	// runTemplGenerate in cmd/scaffold/init.go and gen.go), so it must be
	// run explicitly here too, in the same order (before go mod tidy, so
	// tidy sees the templ runtime import).
	templGen := exec.Command("templ", "generate")
	templGen.Dir = dir
	if out, err := templGen.CombinedOutput(); err != nil {
		t.Fatalf("templ generate:\n%s\n%v", out, err)
	}

	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy:\n%s\n%v", out, err)
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./...:\n%s\n%v", out, err)
	}

	t.Logf("Postgres/SSR project compiled successfully: %s", fmt.Sprintf("file://%s", dir))
}

// waitForHTTP polls url until it responds 200 or the timeout elapses.
func waitForHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", url)
}

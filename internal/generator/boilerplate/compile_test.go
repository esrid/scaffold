package boilerplate_test

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/esrid/scaffold/internal/generator"
	"github.com/esrid/scaffold/internal/generator/boilerplate"
	scaffoldparser "github.com/esrid/scaffold/internal/parser"
)

// requiresTempl skips the test if the templ CLI is not installed.
func requiresTempl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("templ"); err != nil {
		t.Skip("templ CLI not installed, skipping SSR compile test")
	}
}

// requiresNetwork skips the test if the Go module proxy is unreachable.
func requiresNetwork(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "proxy.golang.org:443", 3*time.Second)
	if err != nil {
		t.Skip("module proxy unreachable, skipping compile test")
	}
	conn.Close()
}

type modelDef struct {
	name   string
	fields []string
}

// initAndBuild generates a full project with boilerplate + models, runs go mod tidy, go build.
func initAndBuild(t *testing.T, db, apiMode string, models []modelDef) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping compilation test in short mode")
	}
	requiresNetwork(t)

	dir := t.TempDir()
	const module = "github.com/test/compilechk"

	errGen := boilerplate.Generate(
		dir,
		module,
		db,
		apiMode,
		"templ",
	)
	if errGen != nil {
		t.Fatalf("boilerplate.Generate: %v", errGen)
	}

	manifest, err := scaffoldparser.LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	manifest.Module = module
	manifest.DB = db
	manifest.APIMode = apiMode

	for _, md := range models {
		fields, err := scaffoldparser.ParseFields(md.fields)
		if err != nil {
			t.Fatalf("ParseFields(%v): %v", md.fields, err)
		}
		model, err := scaffoldparser.BuildModel(md.name, fields, nil, manifest, "", false)
		if err != nil {
			t.Fatalf("BuildModel(%s): %v", md.name, err)
		}
		manifest.Models[md.name] = model.ManifestEntry()
		g := generator.New(dir, module, manifest, false)
		if _, err := g.Scaffold(model); err != nil {
			t.Fatalf("Scaffold(%s): %v", md.name, err)
		}
	}

	// gRPC projects require protoc/buf to generate the pb package — skip full compile.
	if apiMode == "grpc" {
		t.Log("gRPC: skipping full compile (pb package requires make proto)")
		return dir
	}

	// SSR projects render with templ — generate the *_templ.go files before build.
	if apiMode == "ssr" {
		requiresTempl(t)
		gen := exec.Command("templ", "generate")
		gen.Dir = dir
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("templ generate:\n%s\n%v", out, err)
		}
	}

	// go mod tidy downloads all dependencies
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy:\n%s\n%v", out, err)
	}

	// go build ./... verifies the project compiles
	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./...:\n%s\n%v", out, err)
	}

	return dir
}

// TestCompile_SSR_SkipOps generates an SSR project whose model skips create &
// delete, then verifies the routes are gone AND the gated handler+templ compile.
func TestCompile_SSR_SkipOps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compilation test in short mode")
	}
	requiresNetwork(t)
	requiresTempl(t)

	dir := t.TempDir()
	const module = "github.com/test/compilechk"

	if err := boilerplate.Generate(dir, module, "sqlite", "ssr", "templ"); err != nil {
		t.Fatalf("boilerplate.Generate: %v", err)
	}
	manifest, err := scaffoldparser.LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	manifest.Module, manifest.DB, manifest.APIMode = module, "sqlite", "ssr"

	fields, err := scaffoldparser.ParseFields([]string{"title:string!"})
	if err != nil {
		t.Fatal(err)
	}
	model, err := scaffoldparser.BuildModel("Note", fields, nil, manifest, "", false)
	if err != nil {
		t.Fatal(err)
	}
	model.Ops = scaffoldparser.OpsFromSkipped([]string{"create", "delete"})
	manifest.Models["Note"] = model.ManifestEntry()
	if _, err := generator.New(dir, module, manifest, false).Scaffold(model); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	handler, err := os.ReadFile(filepath.Join(dir, "internal/adapters/http/note_handler_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(handler), "h.Create)") || strings.Contains(string(handler), "r.Delete(") {
		t.Errorf("skipped routes still registered:\n%s", handler)
	}
	if !strings.Contains(string(handler), "h.List)") {
		t.Error("list route should remain")
	}

	for _, c := range [][]string{{"templ", "generate"}, {"go", "mod", "tidy"}, {"go", "build", "./..."}} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v:\n%s\n%v", c, out, err)
		}
	}
}

// TestCompile_InitOnly_SSR verifies a freshly-initialised project (no models)
// compiles — i.e. the boilerplate's empty routes_gen.go + app.go wiring is valid.
func TestCompile_InitOnly_SSR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compilation test in short mode")
	}
	requiresNetwork(t)
	requiresTempl(t)

	dir := t.TempDir()
	if err := boilerplate.Generate(dir, "github.com/test/initonly", "sqlite", "ssr", "templ"); err != nil {
		t.Fatalf("boilerplate.Generate: %v", err)
	}
	for _, c := range [][]string{{"templ", "generate"}, {"go", "mod", "tidy"}, {"go", "build", "./..."}} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v:\n%s\n%v", c, out, err)
		}
	}
}

func TestCompile_SSR_SQLite(t *testing.T) {
	initAndBuild(t, "sqlite", "ssr", []modelDef{
		{"Product", []string{"name:string!", "price:float!", "description:string"}},
		{"Category", []string{"label:string!", "slug:string{unique}!"}},
	})
}

func TestCompile_REST_SQLite(t *testing.T) {
	initAndBuild(t, "sqlite", "rest", []modelDef{
		{"Article", []string{"title:string!", "body:string!", "views:int"}},
	})
}

func TestCompile_REST_Postgres(t *testing.T) {
	initAndBuild(t, "postgres", "rest", []modelDef{
		{"User", []string{"email:string{unique}!", "name:string!"}},
	})
}

func TestCompile_SSR_Postgres(t *testing.T) {
	initAndBuild(t, "postgres", "ssr", []modelDef{
		{"Post", []string{"title:string!", "body:string!", "published:bool!"}},
	})
}

func TestCompile_GRPC_SQLite(t *testing.T) {
	initAndBuild(t, "sqlite", "grpc", []modelDef{
		{"Order", []string{"status:string!", "total:float!"}},
	})
}

// TestCompile_Arrays_SQLite verifies array fields render and compile on SQLite
// (JSON-encoded TEXT columns).
func TestCompile_Arrays_SQLite(t *testing.T) {
	initAndBuild(t, "sqlite", "ssr", []modelDef{
		{"Post", []string{
			"title:string!",
			"tags:[]string!", "scores:[]int", "ids:[]int64",
			"weights:[]float64", "flags:[]bool",
		}},
	})
}

// TestCompile_Arrays_Postgres verifies array fields render and compile on
// Postgres (native array columns scanned/inserted by pgx).
func TestCompile_Arrays_Postgres(t *testing.T) {
	initAndBuild(t, "postgres", "rest", []modelDef{
		{"Post", []string{
			"title:string!",
			"tags:[]string!", "scores:[]int", "ids:[]int64",
			"weights:[]float64", "flags:[]bool",
		}},
	})
}

// TestCompile_Arrays_GRPC verifies repeated proto fields and the []int<->[]int32
// bridges render and compile.
func TestCompile_Arrays_GRPC(t *testing.T) {
	initAndBuild(t, "sqlite", "grpc", []modelDef{
		{"Post", []string{
			"title:string!",
			"tags:[]string!", "scores:[]int", "ids:[]int64",
			"weights:[]float64", "flags:[]bool",
		}},
	})
}

// TestCompile_AllFieldTypes verifies every supported Go type renders and compiles.
func TestCompile_AllFieldTypes(t *testing.T) {
	initAndBuild(t, "sqlite", "ssr", []modelDef{
		{"Everything", []string{
			"s_req:string!", "s_opt:string",
			"i_req:int!", "i_opt:int",
			"i64_req:int64!", "i64_opt:int64",
			"f_req:float!", "f_opt:float",
			"b_req:bool!", "b_opt:bool",
			"t_req:time!", "t_opt:time",
			"j_req:json!",
		}},
	})
}

// TestBoilerplate_SSR_RunsAndServesHealth starts a generated SQLite/SSR project
// and verifies it responds on /health.
func TestBoilerplate_SSR_RunsAndServesHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping run test in short mode")
	}
	requiresNetwork(t)

	const addr = "127.0.0.1:17432"

	dir := initAndBuild(t, "sqlite", "ssr", []modelDef{
		{"Product", []string{"name:string!", "price:float!"}},
	})

	// build the server binary
	bin := filepath.Join(dir, "server")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build:\n%s\n%v", out, err)
	}

	// write .env
	env := "DATABASE_URL=file:test.db?_journal_mode=WAL&_foreign_keys=on\n" +
		"SECRET_KEY=test-secret-for-health-check\n" +
		"ADDR=" + addr + "\n" +
		"APP_ENV=development\n"
	os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0644)

	// start server
	srv := exec.Command(bin)
	srv.Dir = dir
	srv.Stdout = os.Stdout
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })

	// wait until TCP port is open (max 5s)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// verify /health responds 200
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health: got status %d want 200", resp.StatusCode)
	}
}

// rbac-contract-check compares the reviewed P0 matrix with source registrations.
// It never generates permissions from HTTP verbs or grants runtime authority.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"micro-one-api/domain/authorization"
)

// First six columns are source facts; remaining columns require human review.
var header = []string{"kind", "service", "entry", "source", "handler", "source_digest", "caller", "operations", "fields", "scopes", "facts_owner", "legacy_guard", "test_contract"}

func render(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, fset, n)
	return b.String()
}

func inventory(root string) ([][]string, error) {
	// Keep traversal and reads under one root, including parser input, so
	// symlinks swapped during inventory cannot redirect reads outside it.
	repo, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	repoFS := repo.FS()
	rows := [][]string{}
	fset := token.NewFileSet()
	err = fs.WalkDir(repoFS, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if slices.Contains([]string{".git", "node_modules", "vendor", ".codex"}, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if rel == "scripts/test-e2e-flow.sh" {
			data, err := repo.ReadFile(rel)
			if err != nil {
				return err
			}
			rows = append(rows, []string{"SOURCE", "identity", rel, rel, "test-only SQL writer", fmt.Sprintf("%x", sha256.Sum256(data))})
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || strings.HasSuffix(rel, ".pb.go") || strings.HasSuffix(rel, "wire_gen.go") {
			return nil
		}
		if !strings.HasPrefix(rel, "app/") && !strings.HasPrefix(rel, "internal/server/") && rel != "cmd/admin-reset/main.go" {
			return nil
		}
		data, err := repo.ReadFile(rel)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), data, 0)
		if err != nil {
			return err
		}
		service := "relay"
		if strings.HasPrefix(rel, "app/") {
			service = strings.Split(rel, "/")[1]
		}
		if rel == "cmd/admin-reset/main.go" {
			service = "identity"
		}
		// Hash package source as well as registration: helper branch changes must
		// prompt matrix review even when the outer route declaration is unchanged.
		writer := strings.HasPrefix(rel, "app/identity/internal/biz/") || strings.HasPrefix(rel, "app/identity/internal/data/") || rel == "app/billing/internal/data/account_repo.go" || rel == "cmd/admin-reset/main.go"
		if strings.Contains(rel, "/server/") || strings.Contains(rel, "/service/") || strings.HasSuffix(rel, "admin_helpers.go") || writer {
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(render(fset, f))))
			rows = append(rows, []string{"SOURCE", service, rel, rel, "package", digest})
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if strings.HasPrefix(name, "Register") && strings.HasSuffix(name, "ServiceServer") {
				rows = append(rows, []string{"GRPC_REGISTER", service, name, fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line), render(fset, call), fmt.Sprintf("%x", sha256.Sum256([]byte(render(fset, call))))})
				return true
			}
			index := 0
			if name == "handleFunc" || name == "handlePrefix" {
				index = 1
			} else if !slices.Contains([]string{"Handle", "HandleFunc", "HandlePrefix", "GET", "POST", "PUT", "PATCH", "DELETE"}, name) {
				return true
			}
			if len(call.Args) <= index {
				return true
			}
			// Variable path parameters occur in shared registration wrappers only.
			if _, ok := call.Args[index].(*ast.Ident); ok {
				return true
			}
			entry := strings.Join(strings.Fields(render(fset, call.Args[index])), " ")
			if lit, ok := call.Args[index].(*ast.BasicLit); ok {
				entry, _ = strconv.Unquote(lit.Value)
			}
			handlers := []string{}
			ast.Inspect(call.Args[len(call.Args)-1], func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if strings.HasPrefix(x.Name, "handle") || strings.HasPrefix(x.Name, "oauth") {
						handlers = append(handlers, x.Name)
					}
				case *ast.SelectorExpr:
					if strings.HasPrefix(x.Sel.Name, "Handle") || strings.HasPrefix(x.Sel.Name, "handle") || x.Sel.Name == "ServeHTTP" || strings.HasPrefix(x.Sel.Name, "relayOrchestrator") {
						handlers = append(handlers, render(fset, x))
					}
				}
				return true
			})
			slices.Sort(handlers)
			handlers = slices.Compact(handlers)
			if len(handlers) == 0 {
				handlers = []string{"inline"}
			}
			rows = append(rows, []string{"HTTP", service, name + " " + entry, fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line), strings.Join(handlers, " "), fmt.Sprintf("%x", sha256.Sum256([]byte(render(fset, call))))})
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	files, err := fs.Glob(repoFS, "api/*/v1/*.proto")
	if err != nil {
		return nil, err
	}
	packageRE := regexp.MustCompile(`(?m)^package\s+(\S+);`)
	serviceRE := regexp.MustCompile(`(?m)^service\s+(\w+)\s*\{`)
	rpcRE := regexp.MustCompile(`\brpc\s+(\w+)\s*\([^;{]+[;{]`)
	for _, rel := range files {
		data, err := repo.ReadFile(rel)
		if err != nil {
			return nil, err
		}
		pkg, services := packageRE.FindSubmatch(data), serviceRE.FindAllSubmatch(data, -1)
		if len(services) == 0 {
			continue
		}
		if len(services) != 1 || len(pkg) == 0 {
			return nil, fmt.Errorf("review service parser for %s (expected one service)", filepath.Join(root, filepath.FromSlash(rel)))
		}
		srv := services[0]
		owner := strings.Split(rel, "/")[1]
		for _, m := range rpcRE.FindAllSubmatch(data, -1) {
			rows = append(rows, []string{"RPC", owner, "/" + string(pkg[1]) + "." + string(srv[1]) + "/" + string(m[1]), rel, string(srv[1]) + "." + string(m[1]), fmt.Sprintf("%x", sha256.Sum256(m[0]))})
		}
	}
	slices.SortFunc(rows, func(a, b []string) int { return strings.Compare(strings.Join(a, "\x00"), strings.Join(b, "\x00")) })
	return rows, nil
}

func check(rows, reviewed [][]string) error {
	if len(reviewed) != len(rows)+1 || !slices.Equal(reviewed[0], header) {
		return fmt.Errorf("matrix size/header differs; re-review registrations")
	}
	codeRE := regexp.MustCompile(`(?:admin|identity|channel|monitor|billing|subscription|log|system|notify|iam|organization)\.[a-z_]+(?:\.[a-z_]+)+`)
	for i, row := range rows {
		actual := reviewed[i+1]
		if len(actual) != len(header) || !slices.Equal(row, actual[:6]) {
			return fmt.Errorf("source drift at row %d: %v", i+2, row[:5])
		}
		for j := 6; j < len(header); j++ {
			if strings.TrimSpace(actual[j]) == "" {
				return fmt.Errorf("unclassified row %d: %s", i+2, header[j])
			}
		}
		if !slices.Contains([]string{"public", "self", "admin", "system", "mixed", "source"}, actual[6]) {
			return fmt.Errorf("unknown caller row %d", i+2)
		}
		for _, code := range codeRE.FindAllString(actual[7], -1) {
			if _, ok := authorization.Lookup(code); !ok {
				return fmt.Errorf("unknown operation %s at row %d", code, i+2)
			}
		}
		if (actual[6] == "admin" || actual[6] == "mixed") && actual[7] == "none" {
			return fmt.Errorf("missing operation row %d", i+2)
		}
	}
	return nil
}

func main() {
	dump := flag.Bool("dump", false, "print source facts for review (does not classify)")
	root := flag.String("root", ".", "repository root")
	matrix := flag.String("matrix", "docs/design/rbac/entry-matrix.csv", "reviewed P0 matrix")
	flag.Parse()
	rows, err := inventory(*root)
	if err == nil && *dump {
		w := csv.NewWriter(os.Stdout)
		_ = w.Write(header[:6])
		_ = w.WriteAll(rows)
		w.Flush()
		err = w.Error()
	} else if err == nil {
		var f *os.File
		f, err = os.Open(filepath.Join(*root, *matrix))
		if err == nil {
			var reviewed [][]string
			reviewed, err = csv.NewReader(f).ReadAll()
			_ = f.Close()
			if err == nil {
				err = check(rows, reviewed)
			}
		}
		if err == nil {
			fmt.Printf("RBAC P0 contract: %d source/HTTP/RPC/registration rows reviewed\n", len(rows))
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

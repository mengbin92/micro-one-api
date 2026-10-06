package main

import (
	"encoding/csv"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"micro-one-api/domain/authorization"
)

func TestFrontendOperationReferencesAreRegistered(t *testing.T) {
	root := filepath.Join("..", "..", "web", "src")
	codeRE := regexp.MustCompile(`["']((?:admin|identity|channel|monitor|billing|subscription|log|system|notify|iam|organization)\.[a-z_]+(?:\.[a-z_]+)+)["']`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (!strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx")) || strings.HasSuffix(path, ".test.ts") || strings.HasSuffix(path, ".test.tsx") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range codeRE.FindAllSubmatch(data, -1) {
			if _, ok := authorization.Lookup(string(match[1])); !ok {
				t.Errorf("%s references an unregistered operation %s", path, match[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewedSourceAndNegativeDrift(t *testing.T) {
	root := filepath.Join("..", "..")
	rows, err := inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, "docs/design/rbac/entry-matrix.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reviewed, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if err = check(rows, reviewed); err != nil {
		t.Fatal(err)
	}
	clone := func() [][]string {
		out := make([][]string, len(reviewed))
		for i, r := range reviewed {
			out[i] = slices.Clone(r)
		}
		return out
	}
	for _, mutate := range []func([][]string){
		func(r [][]string) { r[1][5] = "changed helper body" },
		func(r [][]string) { r[1][6] = "" },
		func(r [][]string) { r[1][7] = "identity.unknown.write" },
		func(r [][]string) { r[1][6] = "unrecognized" },
	} {
		bad := clone()
		mutate(bad)
		if check(rows, bad) == nil {
			t.Fatal("unreviewed matrix accepted")
		}
	}
	if check(append(rows, []string{"HTTP", "identity", "/new", "file", "handler", "hash"}), reviewed) == nil {
		t.Fatal("new route accepted")
	}
}

func TestInventoryIncludesAliasesAlternateRegistrationAndRPC(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app", "admin", "internal", "server")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := `package server
func f(){ srv.HandleFunc("/a",handler); srv.HandlePrefix("/a/",handler); s.handleFunc(srv,"/custom",declaration,handler); v1.RegisterAdminServiceServer(srv,svc) }`
	if err := os.WriteFile(filepath.Join(dir, "http.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	protoDir := filepath.Join(root, "api", "admin", "v1")
	if err := os.MkdirAll(protoDir, 0700); err != nil {
		t.Fatal(err)
	}
	proto := "package api.admin.v1;\nservice AdminService {\n rpc One (R) returns (R); rpc Two(R) returns (R);\n}"
	if err := os.WriteFile(filepath.Join(protoDir, "admin.proto"), []byte(proto), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r[0]]++
		if r[0] == "RPC" && !strings.HasPrefix(r[2], "/api.admin.v1.AdminService/") {
			t.Fatal(r)
		}
	}
	if counts["HTTP"] != 3 || counts["GRPC_REGISTER"] != 1 || counts["RPC"] != 2 || counts["SOURCE"] != 1 {
		t.Fatal(counts)
	}
}

func TestInventoryRejectsSymlinksOutsideRoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		content string
		dirLink bool
	}{
		{name: "script", path: "scripts/test-e2e-flow.sh", content: "#!/bin/sh\n"},
		{name: "Go source", path: "app/admin/internal/server/http.go", content: "package server\n"},
		{name: "proto file", path: "api/admin/v1/admin.proto", content: "package api.admin.v1;\nservice AdminService { rpc One(R) returns (R); }"},
		{name: "proto directory", path: "api/admin/v1", content: "package api.admin.v1;\nservice AdminService { rpc One(R) returns (R); }", dirLink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, "source")
			if tc.dirLink {
				target = outside
			}
			file := target
			if tc.dirLink {
				file = filepath.Join(target, "admin.proto")
			}
			if err := os.WriteFile(file, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			rows, err := inventory(root)
			// Glob may omit an inaccessible directory; either result must prevent
			// external source from becoming trusted inventory rows.
			if err == nil && (!tc.dirLink || len(rows) != 0) {
				t.Fatalf("inventory accepted an external symlink: %v", rows)
			}
		})
	}
}

func TestInventoryAllowsSymlinksWithinRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.sh"), []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../source.sh", filepath.Join(root, "scripts/test-e2e-flow.sh")); err != nil {
		t.Fatal(err)
	}
	rows, err := inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][3] != "scripts/test-e2e-flow.sh" {
		t.Fatalf("unexpected inventory: %v", rows)
	}
}

func TestInventoryIncludesOnlyRegisteredProtoHTTP(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app", "admin", "internal", "server")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "http.go"), []byte(`package server
 func f(){ v1.RegisterIAMAdminServiceHTTPServer(srv,svc) }`), 0600); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(root, "api", "admin", "v1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"IAMAdminService", "UnregisteredService"} {
		body := `package api.admin.v1;
service ` + name + ` {
 rpc GetRole(R) returns (R) { option (google.api.http) = { get: "/roles/{id}" }; }
}`
		if err := os.WriteFile(filepath.Join(dir, name+".proto"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := inventory(root)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row[0] == "HTTP_PROTO" {
			count++
			if row[2] != "GET /roles/{id}" || row[4] != "IAMAdminService.GetRole" {
				t.Fatal(row)
			}
		}
	}
	if count != 1 {
		t.Fatalf("HTTP inventory: %v", rows)
	}
}

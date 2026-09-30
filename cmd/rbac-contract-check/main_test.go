package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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

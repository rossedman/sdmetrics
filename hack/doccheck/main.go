// Command doccheck reports functions and methods without a doc comment, and
// package directories without a package comment.
//
// Usage: go run ./hack/doccheck <dir>...
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs are directory names that are never walked.
var skipDirs = map[string]bool{".git": true, "site": true, "dist": true, "testdata": true}

// main runs doccheck on the command line arguments and exits with its status.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run checks every dir in args and returns the exit code: 0 when clean, 1 when
// problems were reported, 2 on usage or I/O errors.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: doccheck <dir>...")
		return 2
	}
	bad := false
	for _, dir := range args {
		problems, err := check(dir)
		if err != nil {
			fmt.Fprintln(stderr, "doccheck:", err)
			return 2
		}
		for _, p := range problems {
			fmt.Fprintln(stdout, p)
			bad = true
		}
	}
	if bad {
		return 1
	}
	return 0
}

// check walks root and returns one message per undocumented function and per
// package directory that lacks a package comment.
func check(root string) ([]string, error) {
	var problems []string
	pkgDoc := map[string]bool{} // dir -> has a package comment
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if ast.IsGenerated(f) {
			return nil
		}
		dir := filepath.Dir(path)
		if f.Doc != nil {
			pkgDoc[dir] = true
		} else if _, ok := pkgDoc[dir]; !ok {
			pkgDoc[dir] = false
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc != nil {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s:%d: func %s has no doc comment",
				path, fset.Position(fn.Pos()).Line, funcName(fn)))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var dirs []string
	for dir, ok := range pkgDoc {
		if !ok {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		problems = append(problems, fmt.Sprintf("%s: package has no package comment", dir))
	}
	return problems, nil
}

// funcName returns Name for functions and Recv.Name for methods.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return recvName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

// recvName returns the type name of a receiver expression, ignoring pointers
// and type parameters.
func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

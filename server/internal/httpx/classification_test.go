package httpx

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryOutboundClientDeclaresItsTransportClass walks the whole server
// tree and fails on any http.Client or httputil.ReverseProxy literal that does
// not set Transport, plus the shortcuts that would bypass the choice
// (http.DefaultClient, http.Get and friends, new(http.Client), a value-typed
// http.Client). A client that picks no class silently inherits the default
// transport: internet-bound traffic would then miss the admin's proxy, and
// LAN traffic would ride an env-var proxy it cannot reach through. The
// convention is recorded in AGENTS.md ("Outbound traffic declares its class").
func TestEveryOutboundClientDeclaresItsTransportClass(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	literals := 0
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "testdata" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		httpName, httputilName := importNames(file)
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				if isSelector(node.Type, httpName, "Client") || isSelector(node.Type, httputilName, "ReverseProxy") {
					literals++
					if !hasKey(node, "Transport") {
						offenders = append(offenders, fmt.Sprintf("%s:%d: %s literal without Transport", rel, fset.Position(node.Pos()).Line, exprString(node.Type)))
					}
				}
			case *ast.CallExpr:
				if fun, ok := node.Fun.(*ast.Ident); ok && fun.Name == "new" && len(node.Args) == 1 && isSelector(node.Args[0], httpName, "Client") {
					offenders = append(offenders, fmt.Sprintf("%s:%d: new(http.Client)", rel, fset.Position(node.Pos()).Line))
				}
				for _, shortcut := range []string{"Get", "Post", "Head", "PostForm"} {
					if isSelector(node.Fun, httpName, shortcut) {
						offenders = append(offenders, fmt.Sprintf("%s:%d: http.%s uses the default client", rel, fset.Position(node.Pos()).Line, shortcut))
					}
				}
			case *ast.SelectorExpr:
				if isSelector(node, httpName, "DefaultClient") {
					offenders = append(offenders, fmt.Sprintf("%s:%d: http.DefaultClient", rel, fset.Position(node.Pos()).Line))
				}
			case *ast.Field:
				if isSelector(node.Type, httpName, "Client") {
					offenders = append(offenders, fmt.Sprintf("%s:%d: value-typed http.Client field", rel, fset.Position(node.Pos()).Line))
				}
			case *ast.ValueSpec:
				if isSelector(node.Type, httpName, "Client") {
					offenders = append(offenders, fmt.Sprintf("%s:%d: value-typed http.Client variable", rel, fset.Position(node.Pos()).Line))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if literals < 20 {
		t.Fatalf("found only %d client literals under %s; the scan is looking in the wrong place", literals, root)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("every outbound HTTP client must declare its transport class -- httpx.External() for internet hosts, httpx.Internal() for cluster-internal ones (AGENTS.md, \"Outbound traffic declares its class\"):\n  %s", strings.Join(offenders, "\n  "))
	}
}

// importNames returns the local identifiers this file uses for net/http and
// net/http/httputil ("" when not imported), honouring aliases.
func importNames(file *ast.File) (httpName, httputilName string) {
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		switch path {
		case "net/http":
			httpName = name
		case "net/http/httputil":
			httputilName = name
		}
	}
	return httpName, httputilName
}

func isSelector(expr ast.Expr, pkg, sel string) bool {
	if pkg == "" || expr == nil {
		return false
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg && selector.Sel.Name == sel
}

func hasKey(lit *ast.CompositeLit, key string) bool {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == key {
				return true
			}
		}
	}
	return false
}

func exprString(expr ast.Expr) string {
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		if ident, ok := selector.X.(*ast.Ident); ok {
			return ident.Name + "." + selector.Sel.Name
		}
	}
	return fmt.Sprintf("%T", expr)
}

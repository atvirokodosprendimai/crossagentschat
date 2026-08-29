package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestServerHasNoWriteTimeout pins the one setting whose failure is invisible.
//
// A non-zero WriteTimeout kills every SSE stream at the deadline with no error
// on either side, minutes in. The symptom is "the agent stopped responding",
// which nobody debugs as a server configuration line — so it is asserted here
// rather than left to review.
func TestServerHasNoWriteTimeout(t *testing.T) {
	if got := newServer(defaultAddr).WriteTimeout; got != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (any deadline silently kills long-lived SSE streams)", got)
	}
}

// TestServerDefaultAddrIsAllInterfaces checks the documented default and that
// an explicit address replaces it.
func TestServerDefaultAddrIsAllInterfaces(t *testing.T) {
	if defaultAddr != "0.0.0.0:8080" {
		t.Errorf("defaultAddr = %q, want 0.0.0.0:8080 (ADR-001: two machines work with no flag)", defaultAddr)
	}
	if got := newServer("127.0.0.1:1234").Addr; got != "127.0.0.1:1234" {
		t.Errorf("Addr = %q, want the address passed in", got)
	}
}

// TestServerMountsAllRoutes proves the composition root wires the real mux.
//
// Every package passes its own tests with the httpapi.NewMux argument removed,
// because nothing else in the repository runs this file. This is the only test
// that goes red for a binary that starts and serves nothing.
func TestServerMountsAllRoutes(t *testing.T) {
	h := newServer(defaultAddr).Handler
	if h == nil {
		t.Fatal("server has no handler")
	}

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/llm.txt"},
		{http.MethodGet, "/events?channel=&name="},
		{http.MethodPost, "/send?channel=&name="},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s = 404, want the route to resolve", tc.method, tc.path)
		}
	}

	// Without this the test would pass on a handler that answers everything.
	t.Run("falsifiable", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/not-a-route", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("unregistered path = %d, want 404", rec.Code)
		}
	})
}

// TestNoURLLoggingInSource guards against a logger that discloses the channel.
//
// It is a source check rather than a behavioural one on purpose: the risk is a
// FUTURE contributor adding a log line, and no runtime assertion catches code
// that has not been written yet. It is deliberately narrow — it cannot catch a
// URL formatted into a variable first and logged later — and claims no more.
func TestNoURLLoggingInSource(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "httpapi")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		if len(files) == 0 {
			t.Fatalf("no Go files under %s — the guard would pass by scanning nothing", dir)
		}
		for _, file := range files {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			if where := findURLLogging(t, file, string(src)); where != "" {
				t.Errorf("%s: %s logs the request URL, which carries the channel credential", file, where)
			}
		}
	}

	// The guard must be able to fail, or it asserts nothing.
	t.Run("falsifiable", func(t *testing.T) {
		const violation = `package p
import ("fmt"; "net/http")
func h(w http.ResponseWriter, r *http.Request) { fmt.Println("req", r.URL) }
`
		if findURLLogging(t, "synthetic.go", violation) == "" {
			t.Error("guard did not flag a logging call that receives r.URL")
		}
	})
}

// findURLLogging reports the first logging call in src whose arguments read the
// request URL, or "" when there is none. Parsed with go/parser rather than
// grepped so a comment or a string mentioning r.URL does not trip it.
func findURLLogging(t *testing.T, filename, src string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}

	var found string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found != "" || !isLoggingCall(call.Fun) {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "URL", "RequestURI", "RawQuery":
					found = "a call at line " + fset.Position(call.Pos()).String()
				}
				return true
			})
		}
		return true
	})
	return found
}

// isLoggingCall recognises the standard-library ways to write a line out.
func isLoggingCall(fun ast.Expr) bool {
	switch fn := fun.(type) {
	case *ast.Ident:
		return fn.Name == "print" || fn.Name == "println"
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		if !ok {
			return false
		}
		switch pkg.Name {
		case "log", "slog", "logger":
			return true
		case "fmt":
			name := fn.Sel.Name
			return strings.HasPrefix(name, "Print") || strings.HasPrefix(name, "Fprint")
		}
	}
	return false
}

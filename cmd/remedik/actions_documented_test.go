package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// docs/actions.md is the only place a strategy author can learn what a step
// takes. The CRD says as much in its own schema — "each action documents and
// validates its own keys" — and for a while only the validating half was
// true: thirty-three parameters lived in the source as constants, five of
// them mentioned in no document or example at all.
//
// Prose cannot be kept in step by intention, so it is kept in step by this.
// Add an action or a parameter and the build says the page is behind, in the
// same way the dashboard's own tests say a class was renamed on one side of a
// pair.
const actionsDoc = "../../docs/actions.md"

func readActionsDoc(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(actionsDoc)
	if err != nil {
		t.Fatalf("read %s: %v", actionsDoc, err)
	}
	return string(body)
}

// Every action the operator can run has an entry.
func TestEveryActionIsDocumented(t *testing.T) {
	registry, err := buildRegistry(registryDeps{}, nil)
	if err != nil {
		t.Fatalf("buildRegistry() error = %v", err)
	}

	doc := readActionsDoc(t)
	names := registry.Names()
	if len(names) == 0 {
		t.Fatal("the registry is empty; this test is checking nothing")
	}

	for _, name := range names {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("action %q is not in docs/actions.md", name)
		}
	}
}

// Every parameter an action reads is named on the page, because a parameter
// nobody can discover is a parameter that does not exist.
func TestEveryActionParameterIsDocumented(t *testing.T) {
	doc := readActionsDoc(t)

	params := parameterNames(t, "../../internal/action")
	if len(params) < 20 {
		t.Fatalf("found only %d parameters; the scan is not finding them", len(params))
	}

	for name, where := range params {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("parameter %q (%s) is not in docs/actions.md", name, where)
		}
	}
}

// And every switch the chart offers, so "enable it" is answerable from the
// same page as "what does it take".
func TestEveryActionFlagIsDocumented(t *testing.T) {
	values, err := os.ReadFile("../../charts/remedik/values.yaml")
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}

	doc := readActionsDoc(t)
	var inActions bool
	var found int

	for _, line := range strings.Split(string(values), "\n") {
		switch {
		case strings.HasPrefix(line, "actions:"):
			inActions = true
		case inActions && len(line) > 0 && line[0] != ' ':
			inActions = false
		case inActions && strings.HasPrefix(line, "  ") && strings.HasSuffix(strings.TrimSpace(line), ":") &&
			!strings.HasPrefix(strings.TrimSpace(line), "#"):
			key := strings.TrimSuffix(strings.TrimSpace(line), ":")
			if key == "enabled" {
				continue
			}
			found++
			if !strings.Contains(doc, "actions."+key+".enabled") {
				t.Errorf("chart flag actions.%s.enabled is not in docs/actions.md", key)
			}
		}
	}

	if found == 0 {
		t.Fatal("no action flags found in values.yaml; the scan is not finding them")
	}
}

// parameterNames collects every `…Param = "…"` constant under a directory,
// mapped to the file that declares it.
//
// The convention is the contract: a step parameter is a constant whose name
// ends in Param, so that this scan can exist at all. One that is not declared
// that way is one this test cannot see — which is worth knowing when adding
// an action.
func parameterNames(t *testing.T, root string) map[string]string {
	t.Helper()
	names := map[string]string{}

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
					continue
				}
				if !strings.HasSuffix(value.Names[0].Name, "Param") {
					continue
				}
				literal, ok := value.Values[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(literal.Value)
				if err != nil {
					continue
				}
				names[unquoted] = filepath.Base(path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return names
}

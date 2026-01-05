package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"strings"
	"text/template"
)

// Engine configuration
type Engine struct {
	Name    string // e.g. "sqlite"
	Package string // e.g. "sqlitedb"
}

var engines = []Engine{
	{Name: "sqlite", Package: "sqlitedb"},
	{Name: "postgres", Package: "postgresdb"},
	{Name: "mysql", Package: "mysqldb"},
}

// MethodInfo holds extracted data from the AST
type MethodInfo struct {
	Name       string
	Params     []Param
	Returns    []Return
	IsCreate   bool
	ReturnElem string // For slice returns or single struct returns
}

type Param struct {
	Name string
	Type string
}

type Return struct {
	Type string
}

func main() {
	// 1. Parse the interface definition
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "pkg/database/querier.go", nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}

	var methods []MethodInfo

	// 2. Extract methods from the "Querier" interface
	ast.Inspect(node, func(n ast.Node) bool {
		typeSpec, ok := n.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "Querier" {
			return true
		}
		interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}

		for _, field := range interfaceType.Methods.List {
			m := MethodInfo{Name: field.Names[0].Name}
			funcType := field.Type.(*ast.FuncType)

			// Parse Params
			for _, param := range funcType.Params.List {
				typeStr := exprToString(param.Type)
				for _, name := range param.Names {
					m.Params = append(m.Params, Param{Name: name.Name, Type: typeStr})
				}
			}

			// Parse Returns
			if funcType.Results != nil {
				for _, res := range funcType.Results.List {
					typeStr := exprToString(res.Type)
					m.Returns = append(m.Returns, Return{Type: typeStr})

					// Detect return element type (for slices or structs)
					cleanType := strings.TrimPrefix(typeStr, "[]")
					if isStruct(cleanType) {
						m.ReturnElem = cleanType
					}
				}
			}

			// Detect if it's a Create method (for MySQL handling)
			if strings.HasPrefix(m.Name, "Create") && m.ReturnElem != "" {
				m.IsCreate = true
			}

			methods = append(methods, m)
		}
		return false
	})

	// 3. Generate files for each engine
	for _, engine := range engines {
		generateFile(engine, methods)
	}
}

// generateFile writes the wrapper_*.go file
func generateFile(engine Engine, methods []MethodInfo) {
	t := template.Must(template.New("wrapper").Funcs(template.FuncMap{
		"joinParams": func(params []Param) string {
			var p []string
			for _, param := range params {
				p = append(p, param.Name)
			}
			return strings.Join(p, ", ")
		},
		"castParam": func(p Param, engPkg string) string {
			// If param is a struct (starts with capital), cast it.
			// Otherwise pass as is.
			if isStruct(p.Type) {
				return fmt.Sprintf("%s.%s(%s)", engPkg, p.Type, p.Name)
			}
			// Special handling for int casts if needed, but assuming types match now
			return p.Name
		},
	}).Parse(wrapperTemplate))

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Engine":  engine,
		"Methods": methods,
	}

	if err := t.Execute(&buf, data); err != nil {
		log.Fatalf("executing template: %v", err)
	}

	// Format code
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		log.Println(buf.String()) // print raw on error
		log.Fatalf("formatting code: %v", err)
	}

	filename := fmt.Sprintf("pkg/database/wrapper_%s.go", engine.Name)
	if err := os.WriteFile(filename, formatted, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Generated %s\n", filename)
}

// Helper to determine if a type is likely a struct we defined (starts with Uppercase, not slice/map)
func isStruct(t string) bool {
	return len(t) > 0 && t[0] >= 'A' && t[0] <= 'Z' && !strings.Contains(t, ".")
}

// Simple AST expression to string converter
func exprToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprToString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + exprToString(t.X)
	case *ast.ArrayType:
		return "[]" + exprToString(t.Elt)
	default:
		return "unknown"
	}
}

const wrapperTemplate = `
package database

import (
	"context"
	"database/sql"

	"github.com/kalbasit/ncps/pkg/database/{{.Engine.Package}}"
)

// {{.Engine.Name}}Wrapper wraps the {{.Engine.Name}} adapter
type {{.Engine.Name}}Wrapper struct {
	adapter *{{.Engine.Package}}.Queries
}

{{range .Methods}}
func (w *{{$.Engine.Name}}Wrapper) {{.Name}}({{range .Params}}{{.Name}} {{.Type}}, {{end}}) ({{range .Returns}}{{.Type}}, {{end}}) {
	{{- /* --- MySQL CREATE Special Handling --- */ -}}
	{{if and $.Engine.IsMySQL .IsCreate}}
		// MySQL does not support RETURNING for INSERTs. 
		// We insert, get LastInsertId, and then fetch the object.
		res, err := w.adapter.{{.Name}}({{range .Params}}{{castParam . $.Engine.Package}}, {{end}})
		if err != nil {
			return {{.ReturnElem}}{}, err
		}

		id, err := res.LastInsertId()
		if err != nil {
			return {{.ReturnElem}}{}, err
		}

		return w.Get{{.ReturnElem}}ByID(ctx, id)

	{{- else -}}

	{{- /* --- Standard Handling --- */ -}}
		{{if .ReturnElem}}res{{else}}_{{end}}, err := w.adapter.{{.Name}}({{range .Params}}{{castParam . $.Engine.Package}}, {{end}})
		if err != nil {
			{{- if .ReturnElem}}
				return nil, err
			{{- else}}
				return 0, err
			{{- end}}
		}

		{{- /* Case 1: Return is a Slice */}}
		{{- if and (eq (index .Returns 0).Type "[]" .ReturnElem) }}
			items := make([]{{.ReturnElem}}, len(res))
			for i, v := range res {
				items[i] = {{.ReturnElem}}(v)
			}
			return items, nil

		{{- /* Case 2: Return is a Struct */}}
		{{- else if .ReturnElem}}
			return {{.ReturnElem}}(res), nil

		{{- /* Case 3: Return is Primitive (e.g. int64) */}}
		{{- else}}
			return res, nil
		{{- end}}

	{{- end}}
}
{{end}}
`

// Note: IsMySQL is a helper I need to add to the template data or struct logic
func (e Engine) IsMySQL() bool { return e.Name == "mysql" }

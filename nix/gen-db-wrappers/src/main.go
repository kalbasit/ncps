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
	"path/filepath"
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
	Name         string
	Params       []Param
	Returns      []Return
	IsCreate     bool   // Special handling for MySQL Create
	ReturnElem   string // The underlying type (e.g. "NarFile" or "string")
	ReturnsError bool   // Does the method return an error?
	ReturnsSelf  bool   // Does it return the wrapper type (like WithTx)?
	HasValue     bool   // Does it return a value (non-error)?
}

type Param struct {
	Name string
	Type string
}

type Return struct {
	Type string
}

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("USAGE: %s /path/to/querier.go", os.Args[0])
	}

	querierPath := os.Args[1]

	if _, err := os.Stat(querierPath); err != nil {
		log.Fatalf("stat(%q): %s", querierPath, err)
	}

	// 1. Parse the interface definition
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, querierPath, nil, parser.ParseComments)
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

					if typeStr == "error" {
						m.ReturnsError = true
					} else if typeStr == "Querier" {
						m.ReturnsSelf = true
						m.HasValue = true
					} else {
						m.HasValue = true
						// Capture element type for both Slices and Singles
						m.ReturnElem = strings.TrimPrefix(typeStr, "[]")
					}
				}
			}

			// Detect if it's a Create method returning a Domain struct (for MySQL)
			if strings.HasPrefix(m.Name, "Create") && isDomainStruct(m.ReturnElem) {
				m.IsCreate = true
			}

			methods = append(methods, m)
		}
		return false
	})

	baseDir := filepath.Dir(querierPath)

	// 3. Generate files for each engine
	for _, engine := range engines {
		generateFile(baseDir, engine, methods)
	}
}

// generateFile writes the wrapper_*.go file
func generateFile(dir string, engine Engine, methods []MethodInfo) {
	t := template.Must(template.New("wrapper").Funcs(template.FuncMap{
		"joinParamsSignature": func(params []Param) string {
			var p []string
			for _, param := range params {
				p = append(p, fmt.Sprintf("%s %s", param.Name, param.Type))
			}
			return strings.Join(p, ", ")
		},
		"joinParamsCall": func(params []Param, engPkg string) string {
			var p []string
			for _, param := range params {
				if isDomainStruct(param.Type) {
					p = append(p, fmt.Sprintf("%s.%s(%s)", engPkg, param.Type, param.Name))
				} else {
					p = append(p, param.Name)
				}
			}
			return strings.Join(p, ", ")
		},
		"joinReturns": func(returns []Return) string {
			var r []string
			for _, ret := range returns {
				r = append(r, ret.Type)
			}
			return strings.Join(r, ", ")
		},
		"isSlice": func(retType string) bool {
			return strings.HasPrefix(retType, "[]")
		},
		"firstReturnType": func(returns []Return) string {
			if len(returns) > 0 {
				return returns[0].Type
			}
			return ""
		},
		"isDomainStruct": isDomainStruct,
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
		log.Println(buf.String())
		log.Fatalf("formatting code: %v", err)
	}

	filename := fmt.Sprintf("wrapper_%s.go", engine.Name)
	if err := os.WriteFile(filepath.Join(dir, filename), formatted, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Generated %s\n", filename)
}

// Helper to determine if a type is a Domain Struct (defined in models.go)
func isDomainStruct(t string) bool {
	// Heuristic: Uppercase start, no dots, not standard interface/type
	return len(t) > 0 && t[0] >= 'A' && t[0] <= 'Z' && !strings.Contains(t, ".") && t != "Querier"
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

// Helper for template
func (e Engine) IsMySQL() bool { return e.Name == "mysql" }

const wrapperTemplate = `
package database

import (
	"context"
	"database/sql"

	"github.com/kalbasit/ncps/pkg/database/{{.Engine.Package}}"
)

// {{.Engine.Name}}Wrapper wraps the {{.Engine.Name}} adapter
type {{.Engine.Name}}Wrapper struct {
	adapter *{{.Engine.Package}}.Adapter
}

{{range .Methods}}
func (w *{{$.Engine.Name}}Wrapper) {{.Name}}({{joinParamsSignature .Params}}) ({{joinReturns .Returns}}) {
	{{- /* --- MySQL CREATE Special Handling --- */ -}}
	{{if and $.Engine.IsMySQL .IsCreate}}
		// MySQL does not support RETURNING for INSERTs.
		// We insert, get LastInsertId, and then fetch the object.
		res, err := w.adapter.{{.Name}}({{joinParamsCall .Params $.Engine.Package}})
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
		{{- $retType := firstReturnType .Returns -}}

		{{/* 1. CALL ADAPTER */}}
		{{/* FIX: Only assign 'res' if there is actually a return value */}}
		{{- if .HasValue -}}
			res{{if .ReturnsError}}, err{{end}} := w.adapter.{{.Name}}({{joinParamsCall .Params $.Engine.Package}})
		{{- else -}}
			err := w.adapter.{{.Name}}({{joinParamsCall .Params $.Engine.Package}})
		{{- end -}}

		{{/* 2. HANDLE ERROR */}}
		{{- if .ReturnsError}}
		if err != nil {
			{{- if .ReturnsSelf}}
				return nil // returns Querier
			{{- else if not .HasValue}}
				return err
			{{- else if isSlice $retType}}
				return nil, err
			{{- else if isDomainStruct .ReturnElem}}
				return {{.ReturnElem}}{}, err
			{{- else}}
				// Primitive return (int64, etc)
				return 0, err
			{{- end}}
		}
		{{- end}}

		{{/* 3. RETURN RESULTS */}}
		{{- if .ReturnsSelf}}
			// Wrap the returned adapter (for WithTx)
			return &{{$.Engine.Name}}Wrapper{adapter: res}

		{{- else if isSlice $retType }}
			{{- if isDomainStruct .ReturnElem}}
				// Convert Slice of Domain Structs
				items := make([]{{.ReturnElem}}, len(res))
				for i, v := range res {
					items[i] = {{.ReturnElem}}(v)
				}
				return items{{if .ReturnsError}}, nil{{end}}
			{{- else}}
				// Return Slice of Primitives (direct match)
				return res{{if .ReturnsError}}, nil{{end}}
			{{- end}}

		{{- else if isDomainStruct .ReturnElem}}
			// Convert Single Domain Struct
			return {{.ReturnElem}}(res){{if .ReturnsError}}, nil{{end}}

		{{- else if .HasValue}}
			// Return Primitive / *sql.DB / etc
			return res{{if .ReturnsError}}, nil{{end}}

		{{- else}}
			// No return value (void)
			{{if .ReturnsError}}return nil{{end}}
		{{- end}}

	{{- end}}
}
{{end}}
`

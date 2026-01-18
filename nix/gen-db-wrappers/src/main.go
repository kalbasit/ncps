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
	"sort"
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
	Docs         []string
}

type Param struct {
	Name string
	Type string
}

type Return struct {
	Type string
}

type StructInfo struct {
	Name   string
	Fields []FieldInfo
}

type FieldInfo struct {
	Name string
	Type string
	Tag  string
}

func main() {
	var querierPath string
	// Handle cases where go run might pass "--"
	for _, arg := range os.Args[1:] {
		if arg != "--" && !strings.HasPrefix(arg, "-") {
			querierPath = arg
			break
		}
	}

	if querierPath == "" {
		log.Printf("DEBUG: Args len=%d, Args=%v", len(os.Args), os.Args)
		log.Fatalf("USAGE: %s /path/to/source/querier.go", os.Args[0])
	}

	if _, err := os.Stat(querierPath); err != nil {
		log.Fatalf("stat(%q): %s", querierPath, err)
	}

	sourceDir := filepath.Dir(querierPath)
	targetDir := filepath.Dir(sourceDir) // Parent of postgresdb is pkg/database

	// 1. Parse all files in sourceDir to find struct definitions and Querier interface
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, sourceDir, nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}

	var methods []MethodInfo
	structs := make(map[string]StructInfo)

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				typeSpec, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}

				// Handle Interface -> Querier
				if typeSpec.Name.Name == "Querier" {
					interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
					if !ok {
						return true
					}
					for _, field := range interfaceType.Methods.List {
						m := MethodInfo{Name: field.Names[0].Name}

						// Capture docs
						if field.Doc != nil {
							for _, comment := range field.Doc.List {
								m.Docs = append(m.Docs, comment.Text)
							}
						}

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
						// Heuristic: Name starts with Create, returns a struct (not primitive/slice) that is a domain struct
						if strings.HasPrefix(m.Name, "Create") && isDomainStruct(m.ReturnElem) { // Note: structs map might be incomplete here, but names are string based
							m.IsCreate = true
						}

						methods = append(methods, m)
					}
				}

				// Handle Struct Definitions
				if structType, ok := typeSpec.Type.(*ast.StructType); ok {
					s := StructInfo{Name: typeSpec.Name.Name}
					if structType.Fields != nil {
						for _, field := range structType.Fields.List {
							typeStr := exprToString(field.Type)
							tag := ""
							if field.Tag != nil {
								tag = field.Tag.Value
							}
							if len(field.Names) > 0 {
								for _, name := range field.Names {
									s.Fields = append(s.Fields, FieldInfo{Name: name.Name, Type: typeStr, Tag: tag})
								}
							} else {
								// Embedded field
								s.Fields = append(s.Fields, FieldInfo{Name: "", Type: typeStr, Tag: tag})
							}
						}
					}
					structs[s.Name] = s
				}

				return true
			})
		}
	}

	// Sort methods by name for internal consistency
	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Name < methods[j].Name
	})

	// 2. Identify used structs from methods (params and return types)
	usedStructNames := make(map[string]bool)
	for _, m := range methods {
		for _, p := range m.Params {
			cleanType := strings.TrimPrefix(p.Type, "[]")
			if _, exists := structs[cleanType]; exists {
				usedStructNames[cleanType] = true
			}
		}
		for _, r := range m.Returns {
			cleanType := strings.TrimPrefix(r.Type, "[]")
			if _, exists := structs[cleanType]; exists {
				usedStructNames[cleanType] = true
			}
		}
	}

	// Convert used structs map to slice and sort
	var sortedStructs []StructInfo
	for name := range usedStructNames {
		sortedStructs = append(sortedStructs, structs[name])
	}
	sort.Slice(sortedStructs, func(i, j int) bool {
		return sortedStructs[i].Name < sortedStructs[j].Name
	})

	// 3. Generate models.go
	generateModels(targetDir, sortedStructs)

	// 4. Generate querier.go
	generateQuerier(targetDir, methods)

	// 5. Generate wrappers
	// We need to re-evaluate IsCreate now that we have full struct knowledge, or just rely on naming convention
	// The previous isDomainStruct check relied on string pattern, which is still valid.
	for _, engine := range engines {
		generateWrapper(targetDir, engine, methods)
	}
}

func generateModels(dir string, structs []StructInfo) {
	t := template.Must(template.New("models").Parse(modelsTemplate))
	var buf bytes.Buffer
	if err := t.Execute(&buf, structs); err != nil {
		log.Fatalf("executing models template: %v", err)
	}
	writeFile(dir, "models.go", buf.Bytes())
}

func generateQuerier(dir string, methods []MethodInfo) {
	t := template.Must(template.New("querier").Funcs(template.FuncMap{
		"joinParamsSignature": joinParamsSignature,
		"joinReturns":         joinReturns,
	}).Parse(querierTemplate))

	var buf bytes.Buffer
	if err := t.Execute(&buf, methods); err != nil {
		log.Fatalf("executing querier template: %v", err)
	}
	writeFile(dir, "querier.go", buf.Bytes())
}

func generateWrapper(dir string, engine Engine, methods []MethodInfo) {
	t := template.Must(template.New("wrapper").Funcs(template.FuncMap{
		"joinParamsSignature": joinParamsSignature,
		"joinParamsCall":      joinParamsCall,
		"joinReturns":         joinReturns,
		"isSlice":             isSlice,
		"firstReturnType":     firstReturnType,
		"isDomainStruct":      isDomainStructFunc,
		"zeroValue":           zeroValue,
	}).Parse(wrapperTemplate))

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Engine":  engine,
		"Methods": methods,
	}

	if err := t.Execute(&buf, data); err != nil {
		log.Fatalf("executing wrapper template: %v", err)
	}
	writeFile(dir, fmt.Sprintf("wrapper_%s.go", engine.Name), buf.Bytes())
}

func writeFile(dir, filename string, content []byte) {
	formatted, err := format.Source(content)
	if err != nil {
		log.Println(string(content))
		log.Fatalf("formatting %s: %v", filename, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), formatted, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Generated %s\n", filename)
}

// Helpers

func joinParamsSignature(params []Param) string {
	var p []string
	for _, param := range params {
		p = append(p, fmt.Sprintf("%s %s", param.Name, param.Type))
	}
	return strings.Join(p, ", ")
}

func joinParamsCall(params []Param, engPkg string) string {
	var p []string
	for _, param := range params {
		if isDomainStructFunc(param.Type) {
			p = append(p, fmt.Sprintf("%s.%s(%s)", engPkg, param.Type, param.Name))
		} else {
			p = append(p, param.Name)
		}
	}
	return strings.Join(p, ", ")
}

func joinReturns(returns []Return) string {
	var r []string
	for _, ret := range returns {
		r = append(r, ret.Type)
	}
	return strings.Join(r, ", ")
}

func isSlice(retType string) bool {
	return strings.HasPrefix(retType, "[]")
}

func firstReturnType(returns []Return) string {
	if len(returns) > 0 {
		return returns[0].Type
	}
	return ""
}

// isDomainStructFunc checks if type is a "Domain Struct" based on naming convention
// This is used inside templates where we don't have the struct map handy,
// but we know domain structs are Uppercase and no dot (unless it's in this package).
func isDomainStructFunc(t string) bool {
	// Remove slice prefix
	t = strings.TrimPrefix(t, "[]")
	// Uppercase start, no dots (implies local type), not Querier, not primitive
	return len(t) > 0 && t[0] >= 'A' && t[0] <= 'Z' && !strings.Contains(t, ".") && t != "Querier"
}

// isDomainStruct is used during parsing, same logic
func isDomainStruct(t string) bool {
	// In parsing phase we might not have map fully populated, but string check is robust enough
	return isDomainStructFunc(t)
}

func zeroValue(t string) string {
	if isNumeric(t) {
		return "0"
	}
	switch t {
	case "bool":
		return "false"
	case "string":
		return `""`
	case "error":
		return "nil"
	}
	if strings.HasPrefix(t, "*") || strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[") || t == "interface{}" {
		return "nil"
	}
	if t == "sql.Result" || t == "Querier" {
		return "nil"
	}
	return fmt.Sprintf("%s{}", t)
}

func isNumeric(t string) bool {
	switch t {
	case "int", "int8", "int16", "int32", "int64":
		return true
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	case "float32", "float64", "complex64", "complex128":
		return true
	case "byte", "rune":
		return true
	}
	return false
}

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
		// Fallback for types we missed or complex types
		return fmt.Sprintf("%s", t)
	}
}

// Templates

const modelsTemplate = `// Code generated by gen-db-wrappers. DO NOT EDIT.
package database

import (
	"database/sql"
	"time"
)

{{range .}}
type {{.Name}} struct {
{{- range .Fields}}
	{{.Name}} {{.Type}} {{if .Tag}}` + "`{{.Tag}}`" + `{{end}}
{{- end}}
}
{{end}}
`

const querierTemplate = `// Code generated by gen-db-wrappers. DO NOT EDIT.
package database

import (
	"context"
	"database/sql"
)

type Querier interface {
{{- range .}}
	{{range .Docs}}
	{{.}}
	{{- end}}
	{{.Name}}({{joinParamsSignature .Params}}) ({{joinReturns .Returns}})
{{- end}}

	WithTx(tx *sql.Tx) Querier
	DB() *sql.DB
}
`

const wrapperTemplate = `// Code generated by gen-db-wrappers. DO NOT EDIT.
package database

import (
	"context"
	"database/sql"

	"github.com/kalbasit/ncps/pkg/database/{{.Engine.Package}}"
)

// {{.Engine.Name}}Wrapper wraps the {{.Engine.Name}} adapter.
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
				// Primitive return (int64, string, etc)
				return {{zeroValue $retType}}, err
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

func (w *{{.Engine.Name}}Wrapper) WithTx(tx *sql.Tx) Querier {
	res := w.adapter.WithTx(tx)
	return &{{.Engine.Name}}Wrapper{adapter: res}
}

func (w *{{.Engine.Name}}Wrapper) DB() *sql.DB {
	return w.adapter.DB()
}
`

func (e Engine) IsMySQL() bool { return e.Name == "mysql" }

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
	"strconv"
	"strings"
	"text/template"

	"github.com/jinzhu/inflection"
)

const generatedFilePrefix = "generated_"

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
	BulkFor      string // Extracted from @bulk-for annotation
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

type PackageData struct {
	Methods []MethodInfo
	Structs map[string]StructInfo
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
		log.Fatalf("USAGE: %s /path/to/source/querier.go", os.Args[0])
	}

	if _, err := os.Stat(querierPath); err != nil {
		log.Fatalf("stat(%q): %s", querierPath, err)
	}

	sourceDir := filepath.Dir(querierPath)
	targetDir := filepath.Dir(sourceDir) // Parent of postgresdb is pkg/database

	// 1. Parse source package
	sourceData := parsePackage(sourceDir)

	// 2. Identify used structs from source methods
	usedStructNames := make(map[string]bool)
	for _, m := range sourceData.Methods {
		for _, p := range m.Params {
			cleanType := strings.TrimPrefix(p.Type, "[]")
			if _, exists := sourceData.Structs[cleanType]; exists {
				usedStructNames[cleanType] = true
			}
		}
		for _, r := range m.Returns {
			cleanType := strings.TrimPrefix(r.Type, "[]")
			if _, exists := sourceData.Structs[cleanType]; exists {
				usedStructNames[cleanType] = true
			}
		}
	}

	var sortedStructs []StructInfo
	for name := range usedStructNames {
		sortedStructs = append(sortedStructs, sourceData.Structs[name])
	}
	sort.Slice(sortedStructs, func(i, j int) bool {
		return sortedStructs[i].Name < sortedStructs[j].Name
	})

	// 3. Generate models.go and querier.go
	globalStructs = sourceData.Structs
	generateModels(targetDir, sortedStructs)
	generateQuerier(targetDir, sourceData.Methods)

	// 4. Parse all target packages
	engineData := make(map[string]PackageData)
	for _, engine := range engines {
		engineDir := filepath.Join(targetDir, engine.Package)
		engineData[engine.Name] = parsePackage(engineDir)
	}

	// 5. Generate wrappers
	for _, engine := range engines {
		generateWrapper(targetDir, engine, sourceData.Methods, sourceData.Structs, engineData[engine.Name])
	}
}

func parsePackage(dir string) PackageData {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
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

				if typeSpec.Name.Name == "Querier" {
					interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
					if !ok {
						return true
					}
					for _, field := range interfaceType.Methods.List {
						m := MethodInfo{Name: field.Names[0].Name}
						if field.Doc != nil {
							for _, comment := range field.Doc.List {
								m.Docs = append(m.Docs, comment.Text)
								if strings.Contains(comment.Text, "@bulk-for") {
									if bulkFor := extractBulkFor(comment.Text); bulkFor != "" {
										m.BulkFor = bulkFor
									}
								}
							}
						}

						funcType := field.Type.(*ast.FuncType)
						for _, param := range funcType.Params.List {
							typeStr := exprToString(param.Type)
							for _, name := range param.Names {
								m.Params = append(m.Params, Param{Name: name.Name, Type: typeStr})
							}
						}

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
									m.ReturnElem = strings.TrimPrefix(typeStr, "[]")
								}
							}
						}
						m.IsCreate = strings.HasPrefix(m.Name, "Create") && isDomainStruct(m.ReturnElem)
						methods = append(methods, m)
					}
				}

				if structType, ok := typeSpec.Type.(*ast.StructType); ok {
					s := StructInfo{Name: typeSpec.Name.Name}
					if structType.Fields != nil {
						for _, field := range structType.Fields.List {
							typeStr := exprToString(field.Type)
							tag := ""
							if field.Tag != nil {
								unquoted, err := strconv.Unquote(field.Tag.Value)
								if err != nil {
									log.Fatalf("failed to unquote struct tag %s: %v", field.Tag.Value, err)
								}
								tag = unquoted
							}
							if len(field.Names) > 0 {
								for _, name := range field.Names {
									s.Fields = append(s.Fields, FieldInfo{Name: name.Name, Type: typeStr, Tag: tag})
								}
							} else {
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

	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Name < methods[j].Name
	})

	return PackageData{Methods: methods, Structs: structs}
}

func generateModels(dir string, structs []StructInfo) {
	t := template.Must(template.New("models").Parse(modelsTemplate))
	var buf bytes.Buffer
	if err := t.Execute(&buf, structs); err != nil {
		log.Fatalf("executing models template: %v", err)
	}
	writeFile(dir, generatedFilePrefix+"models.go", buf.Bytes())
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
	writeFile(dir, generatedFilePrefix+"querier.go", buf.Bytes())
}

func generateWrapper(dir string, engine Engine, methods []MethodInfo, structs map[string]StructInfo, engData PackageData) {
	t := template.Must(template.New("wrapper").Funcs(template.FuncMap{
		"joinParamsSignature": joinParamsSignature,
		"joinReturns":         joinReturns,
		"isSlice":             isSlice,
		"firstReturnType":     firstReturnType,
		"isDomainStruct":      isDomainStructFunc,
		"zeroValue":           zeroValue,
		"getStruct":           func(name string) StructInfo { return structs[name] },
		"hasSliceField":       hasSliceField,
		"getSliceField":       getSliceField,
		"toSingular":          toSingular,
		"trimPrefix":          strings.TrimPrefix,
		"getTargetMethod": func(name string) MethodInfo {
			for _, m := range engData.Methods {
				if m.Name == name {
					return m
				}
			}
			return MethodInfo{}
		},
		"getTargetStruct": func(name string) StructInfo {
			if engData.Structs == nil {
				return StructInfo{}
			}
			return engData.Structs[name]
		},
		"joinParamsCall": func(params []Param, engPkg string, targetMethodName string) (string, error) {
			targetMethod := MethodInfo{}
			if engData.Methods != nil {
				for _, m := range engData.Methods {
					if m.Name == targetMethodName {
						targetMethod = m
						break
					}
				}
			}
			return joinParamsCall(params, engPkg, targetMethod, engData.Structs)
		},
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"hasSuffix": strings.HasSuffix,
	}).Parse(wrapperTemplate))

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Engine":  engine,
		"Methods": methods,
		"Structs": structs,
	}

	if err := t.Execute(&buf, data); err != nil {
		log.Fatalf("executing wrapper template: %v", err)
	}
	writeFile(dir, fmt.Sprintf("%swrapper_%s.go", generatedFilePrefix, engine.Name), buf.Bytes())
}

func extractBulkFor(comment string) string {
	parts := strings.Fields(comment)
	for i, p := range parts {
		if p == "@bulk-for" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func toSingular(s string) string { return inflection.Singular(s) }

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

func joinParamsCall(params []Param, engPkg string, targetMethod MethodInfo, targetStructs map[string]StructInfo) (string, error) {
	var p []string
	for i, param := range params {
		if isDomainStructFunc(param.Type) {
			if strings.HasPrefix(param.Type, "[]") {
				return "", fmt.Errorf("unsupported parameter type: slice of domain struct %s. Slices of domain structs are not supported as direct parameters, as they require a conversion loop to be generated. The auto-looping for bulk inserts handles this by operating on a struct parameter containing a slice.", param.Type)
			} else {
				// Check if the target method has the same type for this parameter
				targetParamType := ""
				if i < len(targetMethod.Params) {
					targetParamType = targetMethod.Params[i].Type
				}

				if targetParamType != "" {
					// Always use field-by-field conversion for domain structs to handle cases where
					// the structs have the same name but different field types (e.g., int32 vs int64).
					sourceStruct := getStructByName(param.Type)
					targetStruct := targetStructs[targetParamType]

					var fields []string
					for _, targetField := range targetStruct.Fields {
						// Find matching field in source struct
						var sourceField FieldInfo
						found := false
						for _, sf := range sourceStruct.Fields {
							if sf.Name == targetField.Name {
								sourceField = sf
								found = true
								break
							}
						}

						if found {
							if sourceField.Type == targetField.Type {
								fields = append(fields, fmt.Sprintf("%s: %s.%s", targetField.Name, param.Name, sourceField.Name))
							} else {
								// Type cast if needed
								fields = append(fields, fmt.Sprintf("%s: %s(%s.%s)", targetField.Name, targetField.Type, param.Name, sourceField.Name))
							}
						}
					}
					p = append(p, fmt.Sprintf("%s.%s{\n%s,\n}", engPkg, targetParamType, strings.Join(fields, ",\n")))
				} else {
					// No target param type info? Fallback to direct conversion (best we can do)
					p = append(p, fmt.Sprintf("%s.%s(%s)", engPkg, param.Type, param.Name))
				}
			}
		} else {
			// Primitive
			targetParamType := ""
			if i < len(targetMethod.Params) {
				targetParamType = targetMethod.Params[i].Type
			}

			if targetParamType != "" && targetParamType != param.Type {
				p = append(p, fmt.Sprintf("%s(%s)", targetParamType, param.Name))
			} else {
				p = append(p, param.Name)
			}
		}
	}
	return strings.Join(p, ", "), nil
}

// Global structs map for getStructByName (set during main execution)
var globalStructs map[string]StructInfo

func getStructByName(name string) StructInfo {
	return globalStructs[name]
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
	case *ast.InterfaceType:
		return "interface{}"
	default:
		// Fallback for types we missed or complex types
		panic(fmt.Sprintf("unhandled expression type: %T", t))
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
	{{.Name}} {{.Type}} {{if .Tag}}` + "`" + `{{.Tag}}` + "`" + `{{end}}
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
	{{- range .Docs}}
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
{{- $methodParams := .Params }}
func (w *{{$.Engine.Name}}Wrapper) {{.Name}}({{joinParamsSignature .Params}}) ({{joinReturns .Returns}}) {
	{{- /* --- Auto-Loop for Bulk Insert on Non-Postgres --- */ -}}
	{{- $isAutoLoop := false -}}
	{{- $singularMethodName := "" -}}
	{{- $paramType := "" -}}
	{{- $sliceField := dict "Name" "" -}}
	{{- if and (not $.Engine.IsPostgres) (gt (len .Params) 1) -}}
		{{- $pType := (index .Params 1).Type -}}
		{{- $sInfo := getStruct $pType -}}
		{{- if hasSliceField $sInfo -}}
			{{- if .BulkFor -}}
				{{- $isAutoLoop = true -}}
				{{- $singularMethodName = .BulkFor -}}
				{{- $paramType = $pType -}}
				{{- $sliceField = getSliceField $sInfo -}}
			{{- else if hasSuffix .Name "s" -}}
				{{- $singularMethodName = toSingular .Name -}}
				{{- if ne $singularMethodName .Name -}}
					{{- $singularParamType := printf "%sParams" $singularMethodName -}}
					{{- $sInfoSingular := getStruct $singularParamType -}}
					{{- if ne $sInfoSingular.Name "" -}}
						{{- $isAutoLoop = true -}}
						{{- $paramType = $pType -}}
						{{- $sliceField = getSliceField $sInfo -}}
					{{- end -}}
				{{- end -}}
			{{- end -}}
		{{- end -}}
	{{- end -}}

	{{- if $isAutoLoop -}}
		{{- $singularParamType := printf "%sParams" $singularMethodName -}}
		{{- $structInfo := getStruct $singularParamType -}}
		{{- $targetSingularParamType := $singularParamType -}} {{/* Assume same name for now */}}
		{{- $targetStructInfo := getTargetStruct $targetSingularParamType -}}
		for _, v := range {{(index $methodParams 1).Name}}.{{$sliceField.Name}} {
			err := w.adapter.{{$singularMethodName}}({{(index $methodParams 0).Name}}, {{$.Engine.Package}}.{{$targetSingularParamType}}{
				{{- $sliceElemType := trimPrefix $sliceField.Type "[]" -}}
				{{- range $targetStructField := $targetStructInfo.Fields }}
					{{- /* Find matching field in source struct */ -}}
					{{- $sourceField := dict "Name" "" -}}
					{{- range $structInfo.Fields }}
						{{- if eq .Name $targetStructField.Name }}
							{{- $sourceField = . }}
						{{- end }}
					{{- end }}
					{{- if ne $sourceField.Name "" }}
						{{- if eq $sourceField.Type $sliceElemType }}
							{{- if eq $sourceField.Type $targetStructField.Type }}
								{{$targetStructField.Name}}: v,
							{{- else }}
								{{$targetStructField.Name}}: {{$targetStructField.Type}}(v),
							{{- end }}
						{{- else }}
							{{- if eq $sourceField.Type $targetStructField.Type }}
								{{$targetStructField.Name}}: {{(index $methodParams 1).Name}}.{{$sourceField.Name}},
							{{- else }}
								{{$targetStructField.Name}}: {{$targetStructField.Type}}({{(index $methodParams 1).Name}}.{{$sourceField.Name}}),
							{{- end }}
						{{- end }}
					{{- end }}
				{{- end }}
				},
			)
			if err != nil {
				if IsDuplicateKeyError(err) {
					continue
				}
				return err
			}
		}
		return nil
	{{- else -}}
		{{- template "standardBody" (dict "Method" . "Engine" $.Engine) -}}
	{{- end -}}
}
{{end}}

{{define "standardBody"}}
	{{- /* --- MySQL CREATE Special Handling --- */ -}}
	{{if and .Engine.IsMySQL .Method.IsCreate}}
		// MySQL does not support RETURNING for INSERTs.
		// We insert, get LastInsertId, and then fetch the object.
		res, err := w.adapter.{{.Method.Name}}({{joinParamsCall .Method.Params .Engine.Package .Method.Name}})
		if err != nil {
			return {{.Method.ReturnElem}}{}, err
		}

		id, err := res.LastInsertId()
		if err != nil {
			return {{.Method.ReturnElem}}{}, err
		}

		return w.Get{{.Method.ReturnElem}}ByID(ctx, id)

	{{- else -}}

	{{- /* --- Standard Handling --- */ -}}
		{{- $retType := firstReturnType .Method.Returns -}}
		{{- $targetMethod := getTargetMethod .Method.Name -}}
		{{- $targetRetType := firstReturnType $targetMethod.Returns -}}

		{{/* 1. CALL ADAPTER */}}
		{{- if .Method.HasValue -}}
			res{{if .Method.ReturnsError}}, err{{end}} := w.adapter.{{.Method.Name}}({{joinParamsCall .Method.Params .Engine.Package .Method.Name}})
		{{- else -}}
			err := w.adapter.{{.Method.Name}}({{joinParamsCall .Method.Params .Engine.Package .Method.Name}})
		{{- end -}}

		{{/* 2. HANDLE ERROR */}}
		{{- if .Method.ReturnsError}}
		if err != nil {
			{{- if .Method.ReturnsSelf}}
				return nil // returns Querier
			{{- else if not .Method.HasValue}}
				return err
			{{- else if isSlice $retType}}
				return nil, err
			{{- else if isDomainStruct .Method.ReturnElem}}
				return {{.Method.ReturnElem}}{}, err
			{{- else}}
				// Primitive return (int64, string, etc)
				return {{zeroValue $retType}}, err
			{{- end}}
		}
		{{- end}}

		{{/* 3. RETURN RESULTS */}}
		{{- if .Method.ReturnsSelf}}
			// Wrap the returned adapter (for WithTx)

			return &{{.Engine.Name}}Wrapper{adapter: res}

		{{- else if isSlice $retType }}
			{{- if isDomainStruct .Method.ReturnElem}}
				// Convert Slice of Domain Structs
				items := make([]{{.Method.ReturnElem}}, len(res))
				for i, v := range res {
					items[i] = {{.Method.ReturnElem}}(v)
				}

				return items{{if .Method.ReturnsError}}, nil{{end}}
			{{- else}}
				// Return Slice of Primitives (direct match)
				return res{{if .Method.ReturnsError}}, nil{{end}}
			{{- end}}

		{{- else if isDomainStruct .Method.ReturnElem}}
			// Convert Single Domain Struct

			return {{.Method.ReturnElem}}(res){{if .Method.ReturnsError}}, nil{{end}}

		{{- else if .Method.HasValue}}
			// Return Primitive / *sql.DB / etc
			{{- if and (eq $retType "bool") (eq $targetRetType "int64") }}
				return res != 0{{if .Method.ReturnsError}}, nil{{end}}
			{{- else if and (ne $retType $targetRetType) (ne $targetRetType "")}}
				return {{$retType}}(res){{if .Method.ReturnsError}}, nil{{end}}
			{{- else}}
				return res{{if .Method.ReturnsError}}, nil{{end}}
			{{- end}}

		{{- else}}
			// No return value (void)
			{{if .Method.ReturnsError}}return nil{{end}}
		{{- end}}

	{{- end}}
{{end}}

func (w *{{.Engine.Name}}Wrapper) WithTx(tx *sql.Tx) Querier {
	res := w.adapter.WithTx(tx)
	return &{{.Engine.Name}}Wrapper{adapter: res}
}

func (w *{{.Engine.Name}}Wrapper) DB() *sql.DB {
	return w.adapter.DB()
}
`

func (e Engine) IsMySQL() bool    { return e.Name == "mysql" }
func (e Engine) IsPostgres() bool { return e.Name == "postgres" }

func hasSliceField(s StructInfo) bool {
	for _, f := range s.Fields {
		if strings.HasPrefix(f.Type, "[]") && f.Type != "[]byte" {
			return true
		}
	}
	return false
}

func getSliceField(s StructInfo) FieldInfo {
	for _, f := range s.Fields {
		if strings.HasPrefix(f.Type, "[]") && f.Type != "[]byte" {
			return f
		}
	}
	return FieldInfo{}
}

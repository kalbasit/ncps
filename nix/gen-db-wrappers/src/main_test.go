package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"strings"
	"testing"
	"text/template"
)

func TestExprToString(t *testing.T) {
	tests := []struct {
		name     string
		expr     ast.Expr
		expected string
		panics   bool
	}{
		{
			name:     "Ident",
			expr:     &ast.Ident{Name: "int"},
			expected: "int",
		},
		{
			name:     "StarExpr",
			expr:     &ast.StarExpr{X: &ast.Ident{Name: "String"}},
			expected: "*String",
		},
		{
			name:     "ArrayType",
			expr:     &ast.ArrayType{Elt: &ast.Ident{Name: "byte"}},
			expected: "[]byte",
		},
		{
			name:     "SelectorExpr",
			expr:     &ast.SelectorExpr{X: &ast.Ident{Name: "sql"}, Sel: &ast.Ident{Name: "NullString"}},
			expected: "sql.NullString",
		},
		{
			name:   "Unhandled MapType",
			expr:   &ast.MapType{Key: &ast.Ident{Name: "string"}, Value: &ast.Ident{Name: "int"}},
			panics: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					if !tt.panics {
						t.Errorf("exprToString panicked unexpectedly: %v", r)
					}
				} else if tt.panics {
					t.Errorf("exprToString expected panic but did not panic")
				}
			}()

			result := exprToString(tt.expr)
			if !tt.panics && result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsDomainStructFunc(t *testing.T) {
	tests := []struct {
		inputType string
		want      bool
	}{
		{"User", true},
		{"[]User", true},
		{"sql.NullString", false},
		{"int", false},
		{"string", false},
		{"Querier", false},
		{"pkg.User", false},
		{"user", false},
	}

	for _, tt := range tests {
		if got := isDomainStructFunc(tt.inputType); got != tt.want {
			t.Errorf("isDomainStructFunc(%q) = %v, want %v", tt.inputType, got, tt.want)
		}
	}
}

func TestZeroValue(t *testing.T) {
	tests := []struct {
		typeName string
		want     string
	}{
		{"int", "0"},
		{"string", `""`},
		{"bool", "false"},
		{"error", "nil"},
		{"*User", "nil"},
		{"[]byte", "nil"},
		{"MyStruct", "MyStruct{}"},
	}

	for _, tt := range tests {
		if got := zeroValue(tt.typeName); got != tt.want {
			t.Errorf("zeroValue(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestExtractBulkFor(t *testing.T) {
	tests := []struct {
		comment string
		want    string
	}{
		{"// CreateUsers creates users @bulk-for CreateUser", "CreateUser"},
		{"// @bulk-for CreateUser", "CreateUser"},
		{"// No annotation here", ""},
		{"// Multiple @bulk-for First @bulk-for Second", "First"},
		{"// @bulk-for", ""},
	}

	for _, tt := range tests {
		if got := extractBulkFor(tt.comment); got != tt.want {
			t.Errorf("extractBulkFor(%q) = %q, want %q", tt.comment, got, tt.want)
		}
	}
}

func TestToSingular(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Users", "User"},
		{"Process", "Process"},
		{"GetStatus", "GetStatus"},
		{"Status", "Status"},
		{"Addresses", "Address"},
	}

	for _, tt := range tests {
		if got := toSingular(tt.input); got != tt.want {
			t.Errorf("toSingular(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestJoinParamsCall(t *testing.T) {
	tests := []struct {
		name    string
		params  []Param
		engPkg  string
		want    string
		wantErr bool
	}{
		{
			name: "Simple Params",
			params: []Param{
				{Name: "ctx", Type: "context.Context"},
				{Name: "id", Type: "int64"},
			},
			engPkg: "sqlitedb",
			want:   "ctx, id",
		},
		{
			name: "Domain Struct Param",
			params: []Param{
				{Name: "user", Type: "User"},
			},
			engPkg: "postgresdb",
			want:   "postgresdb.User(user)",
		},
		{
			name: "Unsupported Slice of Domain Struct",
			params: []Param{
				{Name: "users", Type: "[]User"},
			},
			engPkg:  "postgresdb",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := joinParamsCall(tt.params, tt.engPkg, MethodInfo{}, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("joinParamsCall() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("joinParamsCall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWrapperTemplate(t *testing.T) {
	// Mock engines
	sqlite := Engine{Name: "sqlite", Package: "sqlitedb"}

	// Mock structs
	structs := map[string]StructInfo{
		"CreateUserParams": {
			Name: "CreateUserParams",
			Fields: []FieldInfo{
				{Name: "Username", Type: "string"},
			},
		},
		"CreateUsersParams": {
			Name: "CreateUsersParams",
			Fields: []FieldInfo{
				{Name: "Usernames", Type: "[]string"},
			},
		},
	}

	// Mock methods
	methods := []MethodInfo{
		{
			Name: "CreateUsers",
			Params: []Param{
				{Name: "ctx", Type: "context.Context"},
				{Name: "arg", Type: "CreateUsersParams"},
			},
			Returns: []Return{{Type: "error"}},
			Docs:    []string{"// CreateUsers creates users"},
		},
	}

	// Helper functions as defined in main.go
	funcMap := template.FuncMap{
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
		"getTargetMethod": func(name string) MethodInfo {
			if name == "CreateUsers" {
				return MethodInfo{
					Name: "CreateUsers",
					Params: []Param{
						{Name: "ctx", Type: "context.Context"},
						{Name: "arg", Type: "CreateUsersParams"},
					},
					Returns: []Return{{Type: "error"}},
				}
			}
			return MethodInfo{}
		},
		"getTargetStruct": func(name string) StructInfo { return structs[name] },
		"joinParamsCall": func(params []Param, engPkg string, targetMethodName string) (string, error) {
			targetMethod := MethodInfo{}
			if targetMethodName == "CreateUsers" {
				targetMethod = MethodInfo{
					Name: "CreateUsers",
					Params: []Param{
						{Name: "ctx", Type: "context.Context"},
						{Name: "arg", Type: "CreateUsersParams"},
					},
					Returns: []Return{{Type: "error"}},
				}
			}
			return joinParamsCall(params, engPkg, targetMethod, structs)
		},
		"hasSuffix": strings.HasSuffix,
	}

	tmpl, err := template.New("wrapper").Funcs(funcMap).Parse(wrapperTemplate)
	if err != nil {
		t.Fatalf("failed to parse template: %v", err)
	}

	data := map[string]interface{}{
		"Engine":  sqlite,
		"Methods": methods,
		"Structs": structs,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	output := buf.String()

	// Verify auto-looping was triggered
	if !strings.Contains(output, "for _, v := range arg.Usernames") {
		t.Errorf("expected output to contain loop over arg.Usernames, but it didn't\n%s", output)
	}

	// Verify field mapping by type
	// CreateUsers -> singular is CreateUser. CreateUserParams has Username (string).
	// arg.Usernames is []string. So v is string.
	// We expect Username: v
	if !strings.Contains(output, "Username: v,") {
		t.Errorf("expected output to contain 'Username: v,', but it didn't\n%s", output)
	}

	// 2. Test GetStatus (should NOT loop because GetStatuParams does not exist)
	methods = []MethodInfo{
		{
			Name: "GetStatus",
			Params: []Param{
				{Name: "ctx", Type: "context.Context"},
				{Name: "hash", Type: "string"},
			},
			Returns: []Return{{Type: "Status"}, {Type: "error"}},
			Docs:    []string{"// GetStatus gets status"},
		},
	}

	data["Methods"] = methods
	buf.Reset()
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	output = buf.String()
	if strings.Contains(output, "for _, v := range") {
		t.Errorf("expected output NOT to contain loop for GetStatus, but it did\n%s", output)
	}
}

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
			got, err := joinParamsCall(tt.params, tt.engPkg, MethodInfo{}, nil, nil)
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
			return joinParamsCall(params, engPkg, targetMethod, structs, structs)
		},
		"hasSuffix":               strings.HasSuffix,
		"generateFieldConversion": generateFieldConversion,
		"zeroReturn": func(m MethodInfo) string {
			// simplified for test
			if m.ReturnsSelf {
				return "nil"
			}
			return "0"
		},
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
	if !strings.Contains(output, "for i, v := range arg.Usernames") {
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

	// 3. Test ReturnsSelf (WithTx)
	methods = []MethodInfo{
		{
			Name:         "WithTx",
			Params:       []Param{{Name: "tx", Type: "*sql.Tx"}},
			Returns:      []Return{{Type: "Querier"}, {Type: "error"}},
			ReturnsSelf:  true,
			ReturnsError: true,
			HasValue:     true,
			Docs:         []string{"// WithTx returns a new Querier with transaction"},
		},
	}

	data["Methods"] = methods
	buf.Reset()
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	output = buf.String()
	if !strings.Contains(output, "nil, ErrNotFound") {
		t.Errorf("expected output to contain 'nil, ErrNotFound' for WithTx, but it didn't\n%s", output)
	}
	if !strings.Contains(output, "nil, err") {
		t.Errorf("expected output to contain 'nil, err' for WithTx, but it didn't\n%s", output)
	}

	// 4. Test sql.NullString conversion
	methods = []MethodInfo{
		{
			Name: "CreateUser",
			Params: []Param{
				{Name: "ctx", Type: "context.Context"},
				{Name: "arg", Type: "CreateUserParams"},
			},
			Returns: []Return{{Type: "error"}},
			Docs:    []string{"// CreateUser creates a user"},
		},
	}

	// Update structs to have NullString
	structs["CreateUserParams"] = StructInfo{
		Name: "CreateUserParams",
		Fields: []FieldInfo{
			{Name: "Bio", Type: "sql.NullString"},
		},
	}
	// Add domain struct User with regular string
	structs["User"] = StructInfo{
		Name: "User",
		Fields: []FieldInfo{
			{Name: "Bio", Type: "string"},
		},
	}

	// Mock getTargetStruct to return the User struct when asked (simulating domain struct)
	// But wait, joinParamsCall uses sourceStructs and targetStructs.
	// In the test, we passed `structs` for both.
	// We need to simulate that we are passing a domain struct "User" but the method takes "CreateUserParams".
	// The template uses `joinParamsCall`.
	// Let's adjust the method params to use "User" as input, and the target method to use "CreateUserParams".

	methods = []MethodInfo{
		{
			Name: "CreateUser",
			Params: []Param{
				{Name: "ctx", Type: "context.Context"},
				{Name: "user", Type: "User"},
			},
			Returns: []Return{{Type: "error"}},
		},
	}

	funcMap["getTargetMethod"] = func(name string) MethodInfo {
		if name == "CreateUser" {
			return MethodInfo{
				Name: "CreateUser",
				Params: []Param{
					{Name: "ctx", Type: "context.Context"},
					{Name: "arg", Type: "CreateUserParams"},
				},
				Returns: []Return{{Type: "error"}},
			}
		}
		return MethodInfo{}
	}

	// Re-parse with updated funcMap (captured closure needs update? No, funcMap is map, but we created new template)
	// We need to re-create template because we changed funcMap values or we need to ensure the closure uses current values.
	// tailored funcMap for this test case
	funcMap["joinParamsCall"] = func(params []Param, engPkg string, targetMethodName string) (string, error) {
		targetMethod := MethodInfo{
			Name: "CreateUser",
			Params: []Param{
				{Name: "ctx", Type: "context.Context"},
				{Name: "arg", Type: "CreateUserParams"},
			},
			Returns: []Return{{Type: "error"}},
		}
		return joinParamsCall(params, engPkg, targetMethod, structs, structs)
	}

	tmpl, err = template.New("wrapper").Funcs(funcMap).Parse(wrapperTemplate)
	if err != nil {
		t.Fatalf("failed to parse template: %v", err)
	}

	data["Methods"] = methods
	buf.Reset()
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	output = buf.String()
	expectedConversion := "Bio: sql.NullString{String: user.Bio, Valid: true}"
	if !strings.Contains(output, expectedConversion) {
		t.Errorf("expected output to contain '%s', but it didn't\n%s", expectedConversion, output)
	}
}

func TestGenerateFieldConversion(t *testing.T) {
	tests := []struct {
		name            string
		targetFieldName string
		targetFieldType string
		sourceFieldType string
		sourceExpr      string
		want            string
	}{
		{
			name:            "Same Types",
			targetFieldName: "ID",
			targetFieldType: "int64",
			sourceFieldType: "int64",
			sourceExpr:      "user.ID",
			want:            "ID: user.ID",
		},
		{
			name:            "String to NullString",
			targetFieldName: "Bio",
			targetFieldType: "sql.NullString",
			sourceFieldType: "string",
			sourceExpr:      "user.Bio",
			want:            "Bio: sql.NullString{String: user.Bio, Valid: true}",
		},
		{
			name:            "Int64 to NullInt64",
			targetFieldName: "Age",
			targetFieldType: "sql.NullInt64",
			sourceFieldType: "int64",
			sourceExpr:      "user.Age",
			want:            "Age: sql.NullInt64{Int64: user.Age, Valid: true}",
		},
		{
			name:            "NullString to String",
			targetFieldName: "Bio",
			targetFieldType: "string",
			sourceFieldType: "sql.NullString",
			sourceExpr:      "row.Bio",
			want:            "Bio: row.Bio.String",
		},
		{
			name:            "NullInt32 to NullInt64",
			targetFieldName: "Count",
			targetFieldType: "sql.NullInt64",
			sourceFieldType: "sql.NullInt32",
			sourceExpr:      "src.Count",
			want:            "Count: sql.NullInt64{Int64: int64(src.Count.Int32), Valid: src.Count.Valid}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateFieldConversion(tt.targetFieldName, tt.targetFieldType, tt.sourceFieldType, tt.sourceExpr)
			if got != tt.want {
				t.Errorf("generateFieldConversion() = %v, want %v", got, tt.want)
			}
		})
	}
}

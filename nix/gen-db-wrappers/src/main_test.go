package main

import (
	"go/ast"
	"testing"
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

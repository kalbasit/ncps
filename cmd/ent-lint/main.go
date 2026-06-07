// Command ent-lint enforces invariants on the Ent schema source tree.
//
// It walks ent/schema/*.go (or any tree passed via --schema-dir) via the
// Go AST and reports violations of the codegen invariants documented in
// the migrate-to-ent-and-atlas design (D10b and ent-schema-lint capability):
//
//   - A1: field-level entsql.Check(...) annotations are forbidden (Ent
//     silently drops them; CHECKs MUST live on table-level Annotations()).
//   - A2: entsql.OnDelete(...) annotations on edge.From(...) are forbidden
//     (Ent silently ignores them; OnDelete MUST live on edge.To(...)).
//   - A4: every edge.To declaration in any schema must have a reciprocal
//     edge.From(...).Ref(...) on the target schema (otherwise Ent fabricates
//     a phantom FK column on the target).
//   - A6: a CURRENT_TIMESTAMP DB default declared via
//     entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"} is forbidden (Ent
//     emits a parenthesized RawExpr that Atlas's SQLite inspector does not
//     round-trip, causing a perpetual phantom table rebuild — issue #1328);
//     use entsql.Default("CURRENT_TIMESTAMP") instead.
//
// Future invariants enforced by this binary (tracked in the
// ent-schema-lint spec but not yet implemented):
//
//   - A3: no field-level .Unique() on a column also bound by edge.From().Field()
//   - A5: every *_ciphertext bytes field must chain .Sensitive()
//   - snake_case enum-type naming
//   - expand-contract DDL ban on the newest migration files
//   - CHECK presence cross-validation between schema annotations and
//     generated SQL
//
// Output is checklist-formatted: one line per invariant per schema, prefixed
// with [PASS] or [FAIL]. The binary exits non-zero if any line is [FAIL].
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type result struct {
	pass   bool
	id     string
	detail string
}

func main() {
	root := flag.String("root", ".", "repository root (contains ent/schema and migrations)")
	schemaDir := flag.String("schema-dir", "", "override path to the ent/schema directory (default: <root>/ent/schema)")

	flag.Parse()

	dir := *schemaDir
	if dir == "" {
		dir = filepath.Join(*root, "ent", "schema")
	}

	schemas, err := loadSchemas(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ent-lint: load schemas: %v\n", err)
		os.Exit(2)
	}

	results := make([]result, 0, len(schemas))

	results = append(results, checkA1(schemas)...)
	results = append(results, checkA2(schemas)...)
	results = append(results, checkA4(schemas)...)
	results = append(results, checkA6(schemas)...)

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].id != results[j].id {
			return results[i].id < results[j].id
		}

		return results[i].detail < results[j].detail
	})

	var failed int

	for _, r := range results {
		prefix := "[PASS]"
		if !r.pass {
			prefix = "[FAIL]"
			failed++
		}

		fmt.Printf("%s %-3s %s\n", prefix, r.id, r.detail)
	}

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\nent-lint: %d FAIL line(s)\n", failed)
		os.Exit(1)
	}
}

// schemaFile holds the parsed AST of one ent/schema/*.go file along with
// the schema-type metadata extracted from it.
type schemaFile struct {
	path  string
	fset  *token.FileSet
	file  *ast.File
	types []schemaType // one per `type X struct { ent.Schema }` declaration
}

type schemaType struct {
	name string // Go type name (e.g. "NarInfo")
}

func loadSchemas(dir string) ([]schemaFile, error) {
	var out []schemaFile

	walkErr := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			return nil
		}

		if !strings.HasSuffix(p, ".go") {
			return nil
		}

		full := filepath.Join(dir, p)
		fset := token.NewFileSet()

		f, err := parser.ParseFile(fset, full, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", full, err)
		}

		sf := schemaFile{path: full, fset: fset, file: f}
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}

			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}

			if !embedsEntSchema(st) {
				return true
			}

			sf.types = append(sf.types, schemaType{name: ts.Name.Name})

			return true
		})

		if len(sf.types) > 0 {
			out = append(out, sf)
		}

		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	return out, nil
}

// embedsEntSchema reports whether the struct embeds either `ent.Schema` or
// a mixin (we only care about ent.Schema for the lint targets, but mixins
// are skipped silently).
func embedsEntSchema(st *ast.StructType) bool {
	for _, fld := range st.Fields.List {
		// Embedded fields have no names; the Type is the embedded type.
		if len(fld.Names) > 0 {
			continue
		}

		sel, ok := fld.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}

		x, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}

		if x.Name == "ent" && sel.Sel.Name == "Schema" {
			return true
		}
	}

	return false
}

// ---------- A1: field-level entsql.Check is forbidden ----------

// checkA1 walks each schema's Fields() method body looking for
// `.Annotations(... entsql.Check(...) ...)` chained on a field builder.
func checkA1(schemas []schemaFile) []result {
	var out []result

	for _, sf := range schemas {
		methods := methodsByName(sf, "Fields")
		for _, m := range methods {
			// Skip top-level helper functions named Fields — only
			// method receivers can be Ent schema methods.
			if receiverTypeName(m) == "" {
				continue
			}

			violations := findFieldEntsqlCheck(sf, m)
			if len(violations) == 0 {
				out = append(out, result{
					pass: true, id: "A1",
					detail: fmt.Sprintf("%s: no field-level entsql.Check", relPath(sf.path)),
				})

				continue
			}

			for _, v := range violations {
				out = append(out, result{
					pass: false, id: "A1",
					detail: fmt.Sprintf(
						"%s:%d field-level entsql.Check is forbidden (use Annotations() on the schema)",
						relPath(sf.path), v,
					),
				})
			}
		}
	}

	return out
}

func findFieldEntsqlCheck(sf schemaFile, fn *ast.FuncDecl) []int {
	var lines []int

	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if sel.Sel.Name != annotationsMethod {
			return true
		}
		// Found a `.Annotations(...)` call; if any argument is `entsql.Check(...)`,
		// flag it.
		for _, arg := range call.Args {
			if isEntsqlCheckCall(arg) {
				lines = append(lines, sf.fset.Position(arg.Pos()).Line)
			}
		}

		return true
	})

	return lines
}

func isEntsqlCheckCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return x.Name == entsqlPkg && sel.Sel.Name == "Check"
}

// ---------- A2: entsql.OnDelete on edge.From is forbidden ----------

// checkA2 walks each schema's Edges() method body and inspects every
// `edge.From(...).Ref(...).Annotations(...)` chain. If any argument to
// Annotations is `entsql.OnDelete(...)`, that's an A2 violation.
func checkA2(schemas []schemaFile) []result {
	var out []result

	for _, sf := range schemas {
		methods := methodsByName(sf, "Edges")
		for _, m := range methods {
			// Skip top-level helper functions named Edges.
			if receiverTypeName(m) == "" {
				continue
			}

			violations := findEdgeFromOnDelete(sf, m)
			if len(violations) == 0 {
				out = append(out, result{
					pass: true, id: "A2",
					detail: fmt.Sprintf("%s: no OnDelete on edge.From", relPath(sf.path)),
				})

				continue
			}

			for _, v := range violations {
				out = append(out, result{
					pass: false, id: "A2",
					detail: fmt.Sprintf("%s:%d entsql.OnDelete on edge.From is forbidden (move to edge.To)", relPath(sf.path), v),
				})
			}
		}
	}

	return out
}

func findEdgeFromOnDelete(sf schemaFile, fn *ast.FuncDecl) []int {
	var lines []int

	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if sel.Sel.Name != annotationsMethod {
			return true
		}
		// Look at the receiver of .Annotations(...). If anywhere in the
		// chain there's a call to edge.From(...), the .Annotations() call
		// applies to that From-side edge.
		if !chainContainsEdgeFrom(sel.X) {
			return true
		}

		for _, arg := range call.Args {
			if isEntsqlOnDeleteCall(arg) {
				lines = append(lines, sf.fset.Position(arg.Pos()).Line)
			}
		}

		return true
	})

	return lines
}

func chainContainsEdgeFrom(e ast.Expr) bool {
	for {
		switch v := e.(type) {
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == edgePkg && sel.Sel.Name == "From" {
					return true
				}

				e = sel.X

				continue
			}

			return false
		case *ast.SelectorExpr:
			e = v.X
		default:
			return false
		}
	}
}

func isEntsqlOnDeleteCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return x.Name == entsqlPkg && sel.Sel.Name == "OnDelete"
}

// ---------- A4: every edge.To needs a reciprocal edge.From().Ref() ----------

// checkA4 catalogues every edge.To declaration across all schemas and
// every edge.From().Ref() declaration, then reports an A4 violation for
// any edge.To(name, T) without a matching edge.From(...,T.Type).Ref(name).
func checkA4(schemas []schemaFile) []result {
	type edgeTo struct {
		source   string // declaring schema's Go type name
		edgeName string // first arg of edge.To
		target   string // second arg target type
		line     int
		path     string
	}

	type edgeFromRef struct {
		source string // declaring schema (target side from the To side's perspective)
		ref    string // first arg of .Ref(...)
		target string // second arg of edge.From — the parent type
	}

	var (
		tos   []edgeTo
		froms []edgeFromRef
	)

	for _, sf := range schemas {
		for _, m := range methodsByName(sf, "Edges") {
			schemaName := receiverTypeName(m)
			// Skip top-level helper functions named Edges (no receiver).
			if schemaName == "" {
				continue
			}

			ast.Inspect(m, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}

				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				// edge.To(...)
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == edgePkg {
					if sel.Sel.Name == "To" && len(call.Args) >= 2 {
						target := typeRefName(call.Args[1])
						// Skip edges whose target isn't a simple T.Type
						// expression — they don't match Ent's edge surface
						// and would produce confusing failure messages
						// (empty target name).
						if target == "" {
							return true
						}

						tos = append(tos, edgeTo{
							source:   schemaName,
							edgeName: stringLitValue(call.Args[0]),
							target:   target,
							line:     sf.fset.Position(call.Pos()).Line,
							path:     sf.path,
						})
					}

					return true
				}
				// .Ref(...) — walk inward to find the matching edge.From.
				if sel.Sel.Name == "Ref" && len(call.Args) >= 1 {
					ref := stringLitValue(call.Args[0])

					fromTarget := findInnerEdgeFromTarget(sel.X)
					if fromTarget != "" {
						froms = append(froms, edgeFromRef{
							source: schemaName,
							ref:    ref,
							target: fromTarget,
						})
					}
				}

				return true
			})
		}
	}

	var out []result

	for _, t := range tos {
		matched := false

		for _, f := range froms {
			if f.source == t.target && f.target == t.source && f.ref == t.edgeName {
				matched = true

				break
			}
		}

		if matched {
			out = append(out, result{
				pass: true, id: "A4",
				detail: fmt.Sprintf(
					"%s:%d edge.To(%q, %s.Type) has reciprocal edge.From().Ref()",
					relPath(t.path), t.line, t.edgeName, t.target,
				),
			})
		} else {
			out = append(out, result{
				pass: false, id: "A4",
				detail: fmt.Sprintf(
					"%s:%d edge.To(%q, %s.Type) has no reciprocal edge.From(%s.Type).Ref(%q) on %s — "+
						"Ent will fabricate a phantom FK column",
					relPath(t.path), t.line, t.edgeName, t.target,
					t.source, t.edgeName, t.target,
				),
			})
		}
	}

	return out
}

// findInnerEdgeFromTarget walks the receiver chain of a SelectorExpr (the
// "X" side of `.Ref(...)`) inward looking for an `edge.From(name, T.Type)`
// call. Returns the type name "T" if found, or "" otherwise.
func findInnerEdgeFromTarget(e ast.Expr) string {
	for {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return ""
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return ""
		}

		if id, ok := sel.X.(*ast.Ident); ok && id.Name == edgePkg && sel.Sel.Name == "From" {
			if len(call.Args) >= 2 {
				return typeRefName(call.Args[1])
			}

			return ""
		}

		e = sel.X
	}
}

// ---------- A6: entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"} is forbidden ----------

// checkA6 walks each schema's Fields() method body looking for a
// `.Annotations(entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"})` chain.
// Ent emits this form as a parenthesized Atlas RawExpr that Atlas's SQLite
// inspector does not round-trip (it strips the parens), producing a
// perpetual phantom ModifyColumn (ChangeDefault) and a destructive table
// rebuild on every generated SQLite migration (issue #1328). The
// round-trippable form is `entsql.Default("CURRENT_TIMESTAMP")`.
func checkA6(schemas []schemaFile) []result {
	var out []result

	for _, sf := range schemas {
		for _, m := range methodsByName(sf, "Fields") {
			// Skip top-level helper functions named Fields — only method
			// receivers can be Ent schema methods.
			if receiverTypeName(m) == "" {
				continue
			}

			violations := findDefaultExprCurrentTimestamp(sf, m)
			if len(violations) == 0 {
				out = append(out, result{
					pass: true, id: "A6",
					detail: fmt.Sprintf("%s: no DefaultExpr CURRENT_TIMESTAMP (use entsql.Default)", relPath(sf.path)),
				})

				continue
			}

			for _, v := range violations {
				out = append(out, result{
					pass: false, id: "A6",
					detail: fmt.Sprintf(
						"%s:%d entsql.Annotation{DefaultExpr: \"CURRENT_TIMESTAMP\"} is forbidden "+
							"(use entsql.Default(\"CURRENT_TIMESTAMP\") — Atlas's SQLite inspector does not "+
							"round-trip the parenthesized RawExpr; issue #1328)",
						relPath(sf.path), v,
					),
				})
			}
		}
	}

	return out
}

func findDefaultExprCurrentTimestamp(sf schemaFile, fn *ast.FuncDecl) []int {
	var lines []int

	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if sel.Sel.Name != annotationsMethod {
			return true
		}

		for _, arg := range call.Args {
			if pos := entsqlAnnotationDefaultExpr(arg); pos != token.NoPos {
				lines = append(lines, sf.fset.Position(pos).Line)
			}
		}

		return true
	})

	return lines
}

// entsqlAnnotationDefaultExpr reports the position of a
// `DefaultExpr: "CURRENT_TIMESTAMP"` field inside an `entsql.Annotation{...}`
// composite literal, or token.NoPos if the expression is not such a literal.
func entsqlAnnotationDefaultExpr(e ast.Expr) token.Pos {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return token.NoPos
	}

	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return token.NoPos
	}

	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != entsqlPkg || sel.Sel.Name != "Annotation" {
		return token.NoPos
	}

	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "DefaultExpr" {
			continue
		}

		if isCurrentTimestampLit(kv.Value) {
			return kv.Pos()
		}
	}

	return token.NoPos
}

func isCurrentTimestampLit(e ast.Expr) bool {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return false
	}

	return strings.EqualFold(stringLitValue(bl), "CURRENT_TIMESTAMP")
}

// ---------- helpers ----------

func methodsByName(sf schemaFile, name string) []*ast.FuncDecl {
	var out []*ast.FuncDecl

	for _, d := range sf.file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}

		if fd.Name.Name == name {
			out = append(out, fd)
		}
	}

	return out
}

// edgePkg is the canonical Ent edge package import name.
const edgePkg = "edge"

// entsqlPkg is the canonical Ent entsql package import name; annotationsMethod
// is the field/edge builder method that attaches schema annotations.
const (
	entsqlPkg         = "entsql"
	annotationsMethod = "Annotations"
)

// receiverTypeName returns the bare type name of a method's receiver
// (e.g. "Child" for `func (Child) Edges() ...` or `func (c Child) Edges() ...`).
func receiverTypeName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}

	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}

	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}

	return ""
}

func stringLitValue(e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	if !ok {
		return ""
	}

	if bl.Kind != token.STRING {
		return ""
	}
	// Strip surrounding quotes.
	if len(bl.Value) >= 2 {
		return bl.Value[1 : len(bl.Value)-1]
	}

	return bl.Value
}

// typeRefName extracts the Go type name from an expression of the form
// `TypeName.Type` (Ent's convention for referring to other schemas in edges).
func typeRefName(e ast.Expr) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	if sel.Sel.Name != "Type" {
		return ""
	}

	if x, ok := sel.X.(*ast.Ident); ok {
		return x.Name
	}

	return ""
}

func relPath(p string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, p); err == nil {
			return rel
		}
	}

	return p
}

// An extension for the `gofmt` code formatter.
//
// Command blanklines applies the blank line rules gofmt leaves open:
// a return is separated from the code above it, a block statement is
// followed by a blank line, a block never starts with a blank line, and
// a documented member of a struct, interface or const/var block gets a
// blank line before its comment. Two ifs with the same condition and no
// init statement stay together, they are one derive and guard decision.
//
// Vendor, testdata and hidden directories are skipped.
//
// Usage: go run ./hack/blanklines [-l] dir...
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	list := flag.Bool("l", false, "only list the files that would change")
	flag.Parse()
	changed := 0
	for _, root := range flag.Args() {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() && path != root && (d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}

			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}

			out, ok, err := process(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}

			if !ok {
				return nil
			}

			changed++
			if *list {
				fmt.Println(path)

				return nil
			}

			return os.WriteFile(path, out, 0o644)
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if *list && changed > 0 {
		os.Exit(1)
	}
}

// edits collects line changes: insert a blank line after a line, or
// delete a blank line.
type edits struct {
	insertAfter map[int]bool
	deleteLine  map[int]bool
}

func process(path string) ([]byte, bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, false, err
	}

	lines := strings.Split(string(src), "\n")
	ed := edits{insertAfter: map[int]bool{}, deleteLine: map[int]bool{}}
	v := &visitor{fset: fset, file: file, src: src, lines: lines, ed: ed}
	ast.Inspect(file, v.visit)
	if len(ed.insertAfter) == 0 && len(ed.deleteLine) == 0 {
		return nil, false, nil
	}

	var out []string
	for i, line := range lines {
		n := i + 1
		if ed.deleteLine[n] {
			continue
		}

		out = append(out, line)
		if ed.insertAfter[n] {
			out = append(out, "")
		}
	}

	formatted, err := format.Source([]byte(strings.Join(out, "\n")))
	if err != nil {
		return nil, false, err
	}

	return formatted, !bytes.Equal(formatted, src), nil
}

type visitor struct {
	fset  *token.FileSet
	file  *ast.File
	src   []byte
	lines []string
	ed    edits
}

func (v *visitor) line(p token.Pos) int {
	return v.fset.Position(p).Line
}

func (v *visitor) blank(n int) bool {
	return n >= 1 && n <= len(v.lines) && strings.TrimSpace(v.lines[n-1]) == ""
}

// startLine is the first line of a node including the comment groups
// stacked directly above it, never reaching back to line after.
func (v *visitor) startLine(after int, n ast.Node) int {
	start := v.line(n.Pos())
	for {
		moved := false
		for _, cg := range v.file.Comments {
			if v.line(cg.End()) == start-1 && v.line(cg.Pos()) > after {
				start = v.line(cg.Pos())
				moved = true
			}
		}

		if !moved {
			return start
		}
	}
}

// blanksBetween counts blank lines strictly between two lines.
func (v *visitor) blanksBetween(a, b int) int {
	n := 0
	for i := a + 1; i < b; i++ {
		if v.blank(i) {
			n++
		}
	}

	return n
}

func (v *visitor) visit(n ast.Node) bool {
	switch node := n.(type) {
	case *ast.BlockStmt:
		v.list(v.line(node.Lbrace), node.List)
	case *ast.CaseClause:
		v.list(v.line(node.Colon), node.Body)
	case *ast.CommClause:
		v.list(v.line(node.Colon), node.Body)
	case *ast.StructType:
		v.fields(node.Fields)
	case *ast.InterfaceType:
		v.fields(node.Methods)
	case *ast.GenDecl:
		if node.Lparen.IsValid() {
			for i, spec := range node.Specs {
				if i == 0 {
					continue
				}

				if doc := specDoc(spec); doc != nil {
					v.ensureBlankBefore(v.line(doc.Pos()))
				}
			}
		}
	}

	return true
}

func specDoc(spec ast.Spec) *ast.CommentGroup {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		return s.Doc
	case *ast.TypeSpec:
		return s.Doc
	}

	return nil
}

func (v *visitor) fields(list *ast.FieldList) {
	if list == nil {
		return
	}

	for i, f := range list.List {
		if i > 0 && f.Doc != nil {
			v.ensureBlankBefore(v.line(f.Doc.Pos()))
		}
	}
}

// ensureBlankBefore inserts a blank line before line n unless one is
// there already.
func (v *visitor) ensureBlankBefore(n int) {
	if !v.blank(n - 1) {
		v.ed.insertAfter[n-1] = true
	}
}

// list applies the statement rules to one block body opening on line
// open.
func (v *visitor) list(open int, stmts []ast.Stmt) {
	if len(stmts) == 0 {
		return
	}

	// A block never starts with a blank line.
	first := v.startLine(open, stmts[0])
	for i := open + 1; i < first; i++ {
		if v.blank(i) {
			v.ed.deleteLine[i] = true
		}
	}

	for i := 1; i < len(stmts); i++ {
		prev, cur := stmts[i-1], stmts[i]
		prevEnd := v.line(prev.End())
		if !v.wantsBlank(prev, cur) {
			continue
		}

		start := v.startLine(prevEnd, cur)
		if v.blanksBetween(prevEnd, start) == 0 {
			v.ed.insertAfter[prevEnd] = true
		}
	}
}

func (v *visitor) wantsBlank(prev, cur ast.Stmt) bool {
	if _, ok := cur.(*ast.ReturnStmt); ok {
		return true
	}

	if !isBlock(prev) {
		return false
	}

	// Derive then guard: two ifs testing the same condition stay tight.
	if a, ok := prev.(*ast.IfStmt); ok {
		if b, ok := cur.(*ast.IfStmt); ok && a.Init == nil && b.Init == nil && v.text(a.Cond) == v.text(b.Cond) {
			return false
		}
	}

	return true
}

func isBlock(s ast.Stmt) bool {
	if l, ok := s.(*ast.LabeledStmt); ok {
		s = l.Stmt
	}

	switch s.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.BlockStmt:
		return true
	}

	return false
}

func (v *visitor) text(n ast.Node) string {
	return string(v.src[v.fset.Position(n.Pos()).Offset:v.fset.Position(n.End()).Offset])
}

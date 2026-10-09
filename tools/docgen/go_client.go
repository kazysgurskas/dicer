// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/doc/comment"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// goClientIntro opens the page.
const goClientIntro = "Every exported identifier of `" + module + "`, the Go client of the " +
	"[API]({{< relref \"/docs/reference/api\" >}}). The page is generated from the package's doc " +
	"comments. [Using the API]({{< relref \"/docs/guides/using-the-api#from-go\" >}}) walks " +
	"through the client with examples.\n"

// writeGoClient writes the Go client reference from the doc comments of the
// package at the module's root. An exported identifier without a doc comment
// fails it.
func writeGoClient(root, dir string) error {
	fset := token.NewFileSet()

	pkg, err := loadGoClient(fset, root)
	if err != nil {
		return err
	}

	if err := checkGoClientDocs(pkg); err != nil {
		return err
	}

	r := &goClientRenderer{fset: fset, pkg: pkg}

	return writePage(filepath.Join(dir, "go-client.md"), frontMatter{
		title: "Go client", weight: 3, icon: "puzzle",
		description: "Every type and call of the Go client, from its doc comments.",
		related:     []string{"/docs/guides/using-the-api"},
	}, r.render())
}

// loadGoClient parses the package at the module's root, without its tests,
// and returns its exported identifiers.
func loadGoClient(fset *token.FileSet, root string) (*doc.Package, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(root, name), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}

	return doc.NewFromFiles(fset, files, module)
}

// checkGoClientDocs returns an error naming every exported identifier of pkg
// without a doc comment. A constant or variable is documented by its group's
// comment or its own, and a struct field or an interface method by its own,
// above it or at the end of its line.
func checkGoClientDocs(pkg *doc.Package) error {
	var missing []string

	if pkg.Doc == "" {
		missing = append(missing, "the package")
	}

	checkValues := func(values []*doc.Value) {
		for _, v := range values {
			if v.Doc != "" {
				continue
			}
			for _, spec := range v.Decl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || vs.Doc != nil || vs.Comment != nil {
					continue
				}
				for _, name := range vs.Names {
					if name.IsExported() {
						missing = append(missing, name.Name)
					}
				}
			}
		}
	}
	checkFuncs := func(funcs []*doc.Func) {
		for _, f := range funcs {
			if f.Doc != "" {
				continue
			}
			if f.Recv != "" {
				missing = append(missing, strings.TrimPrefix(f.Recv, "*")+"."+f.Name)
				continue
			}
			missing = append(missing, f.Name)
		}
	}

	checkValues(pkg.Consts)
	checkValues(pkg.Vars)
	checkFuncs(pkg.Funcs)

	for _, t := range pkg.Types {
		if t.Doc == "" {
			missing = append(missing, t.Name)
		}
		checkValues(t.Consts)
		checkValues(t.Vars)
		checkFuncs(t.Funcs)
		checkFuncs(t.Methods)

		var fields *ast.FieldList
		switch typ := t.Decl.Specs[0].(*ast.TypeSpec).Type.(type) {
		case *ast.StructType:
			fields = typ.Fields
		case *ast.InterfaceType:
			fields = typ.Methods
		}
		if fields == nil {
			continue
		}
		for _, f := range fields.List {
			if f.Doc != nil || f.Comment != nil {
				continue
			}
			for _, name := range f.Names {
				if name.IsExported() {
					missing = append(missing, t.Name+"."+name.Name)
				}
			}
		}
	}

	if len(missing) == 0 {
		return nil
	}

	return fmt.Errorf("no doc comment on %s, so it would go undocumented", strings.Join(missing, ", "))
}

// goClientRenderer writes the Go client reference in the order pkg.go.dev
// does: the package's doc comment, its constants, variables and functions,
// then each type with its own.
type goClientRenderer struct {
	fset *token.FileSet
	pkg  *doc.Package
	out  bytes.Buffer
}

// render returns the page's body.
func (r *goClientRenderer) render() []byte {
	r.out.WriteString(goClientIntro)

	r.out.WriteString("\n## Overview {#overview}\n\n")
	r.writeDoc(r.pkg.Doc)

	if len(r.pkg.Consts) > 0 {
		r.out.WriteString("\n## Constants {#constants}\n")
		r.writeValues(r.pkg.Consts)
	}

	if len(r.pkg.Vars) > 0 {
		r.out.WriteString("\n## Variables {#variables}\n")
		r.writeValues(r.pkg.Vars)
	}

	if len(r.pkg.Funcs) > 0 {
		r.out.WriteString("\n## Functions {#functions}\n")
		r.writeFuncs(r.pkg.Funcs, "###")
	}

	if len(r.pkg.Types) > 0 {
		r.out.WriteString("\n## Types {#types}\n")
	}
	for _, t := range r.pkg.Types {
		fmt.Fprintf(&r.out, "\n### type %s {#%s}\n\n", t.Name, goClientAnchor(t.Name))
		r.writeDecl(t.Decl)
		r.writeDoc(t.Doc)
		r.writeValues(t.Consts)
		r.writeValues(t.Vars)
		r.writeFuncs(t.Funcs, "####")
		r.writeFuncs(t.Methods, "####")
	}

	return r.out.Bytes()
}

// writeValues writes each group of constants or variables: its declaration,
// then its doc comment.
func (r *goClientRenderer) writeValues(values []*doc.Value) {
	for _, v := range values {
		r.out.WriteString("\n")
		r.writeDecl(v.Decl)
		r.writeDoc(v.Doc)
	}
}

// writeFuncs writes each function or method under a heading of the given
// level.
func (r *goClientRenderer) writeFuncs(funcs []*doc.Func, level string) {
	for _, f := range funcs {
		name, heading := f.Name, "func "+f.Name
		if f.Recv != "" {
			name = strings.TrimPrefix(f.Recv, "*") + "." + f.Name
			heading = fmt.Sprintf("func (%s) %s", f.Recv, f.Name)
		}

		fmt.Fprintf(&r.out, "\n%s %s {#%s}\n\n", level, heading, goClientAnchor(name))
		r.writeDecl(f.Decl)
		r.writeDoc(f.Doc)
	}
}

// writeDecl writes a declaration as Go, with the comments inside it. go/doc
// has taken its doc comment out of it, and go/printer notes where it left out
// a struct's unexported fields.
func (r *goClientRenderer) writeDecl(decl ast.Decl) {
	var b bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&b, r.fset, decl); err != nil {
		// A declaration go/parser read always prints.
		panic(err)
	}

	fmt.Fprintf(&r.out, "```go\n%s\n```\n\n", b.Bytes())
}

// writeDoc writes a doc comment as Markdown. Its code blocks are fenced as
// Go, to be highlighted, and its links to the package's own identifiers go
// to their anchors on the page, and to pkg.go.dev for any other package's.
func (r *goClientRenderer) writeDoc(text string) {
	if text == "" {
		return
	}

	d := r.pkg.Parser().Parse(text)
	p := r.pkg.Printer()
	p.DocLinkURL = func(link *comment.DocLink) string {
		if link.ImportPath != "" {
			return link.DefaultURL("https://pkg.go.dev")
		}
		if link.Recv != "" {
			return "#" + goClientAnchor(link.Recv+"."+link.Name)
		}
		return "#" + goClientAnchor(link.Name)
	}

	for i, block := range d.Content {
		if i > 0 {
			r.out.WriteString("\n")
		}
		if code, ok := block.(*comment.Code); ok {
			fmt.Fprintf(&r.out, "```go\n%s```\n", code.Text)
			continue
		}

		// Doc comments have no inline code, so the printer escapes
		// backticks. This package's comments put commands in them, which
		// the page shows as code.
		md := p.Markdown(&comment.Doc{Content: []comment.Block{block}})
		r.out.Write(bytes.ReplaceAll(md, []byte("\\`"), []byte("`")))
	}
}

// goClientAnchor returns the anchor of an identifier's heading: Client is
// client, and Client.Close is client-close. Hugo lowercases an anchor and
// drops its dots where it links to it from the page's table of contents, so
// an anchor that keeps either is never scrolled to.
func goClientAnchor(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, ".", "-"))
}

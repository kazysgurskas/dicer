// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package main

import (
	"go/doc"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadGoClientSource loads a Go client package whose only file holds src.
func loadGoClientSource(t *testing.T, src string) (*token.FileSet, *doc.Package) {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "client.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	pkg, err := loadGoClient(fset, root)
	if err != nil {
		t.Fatal(err)
	}

	return fset, pkg
}

func TestUndocumentedGoClientIdentifiersFailGeneration(t *testing.T) {
	_, pkg := loadGoClientSource(t, `// Package dicer is a client.
package dicer

// The states.
const (
	StateA = "a"
	StateB = "b"
)

const (
	// LimitA is documented.
	LimitA = 1
	LimitB = 2
)

// Client is documented.
type Client struct {
	// Name is documented.
	Name    string
	Address string
}

func (c *Client) Close() error { return nil }

type Option int

func NewOption() Option { return 0 }
`)

	err := checkGoClientDocs(pkg)
	if err == nil {
		t.Fatal("checkGoClientDocs() = nil, want an error")
	}

	for _, want := range []string{"LimitB", "Client.Close", "Client.Address", "Option", "NewOption"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkGoClientDocs() = %q, want it to name %s", err, want)
		}
	}
	for _, documented := range []string{"StateA", "StateB", "LimitA", "Client.Name"} {
		if strings.Contains(err.Error(), documented) {
			t.Errorf("checkGoClientDocs() = %q, which names %s, documented", err, documented)
		}
	}
}

func TestGoClientDocCommentsRenderAsMarkdown(t *testing.T) {
	fset, pkg := loadGoClientSource(t, "// Package dicer is a client.\n"+
		"//\n"+
		"// Make a [Client] and [Client.Close] it. Match errors with [errors.Is].\n"+
		"// A token is made with `dicer token create`.\n"+
		"//\n"+
		"//\tc, err := dicer.NewClient()\n"+
		"package dicer\n"+
		"\n"+
		"// Client is a connection.\n"+
		"type Client struct {\n"+
		"\t// Name is shown.\n"+
		"\tName string\n"+
		"\tconn int\n"+
		"}\n"+
		"\n"+
		"// Close closes it.\n"+
		"func (c *Client) Close() error { return nil }\n")

	r := &goClientRenderer{fset: fset, pkg: pkg}
	page := string(r.render())

	tests := []struct {
		name string
		want string
	}{
		{"link to a type", "[Client](#client)"},
		{"link to a method", "[Client.Close](#client-close)"},
		{"link to another package", "[errors.Is](https://pkg.go.dev/errors#Is)"},
		{"backticks as code", "made with `dicer token create`."},
		{"code block", "```go\nc, err := dicer.NewClient()\n```"},
		{"type anchor", "### type Client {#client}"},
		{"method anchor", "#### func (*Client) Close {#client-close}"},
		{"field comment", "\t// Name is shown.\n\tName string\n"},
		{"unexported fields", "// contains filtered or unexported fields"},
		{"method without body", "```go\nfunc (c *Client) Close() error\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(page, tt.want) {
				t.Errorf("page:\n%s\nwant it to contain %q", page, tt.want)
			}
		})
	}
}

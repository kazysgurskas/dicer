// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/docker/go-units"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/konradasb/dicer/internal/cli/printer"
)

// formatUsage describes --format.
const formatUsage = "Output format: table, json, yaml, or a Go template of the table's columns, e.g. '{{.Name}}'"

// addOutputFlags adds --format, and --columns and --quiet when the output is
// a table of several rows.
func addOutputFlags(cmd *cobra.Command, columns bool) {
	cmd.Flags().String("format", "table", formatUsage)
	_ = cmd.RegisterFlagCompletionFunc("format", completeFormats)

	if columns {
		cmd.Flags().StringSliceP("columns", "c", nil,
			"Columns of the table to display, comma-separated and in any case (default: all)")
		cmd.Flags().BoolP("quiet", "q", false, "Only display names, one a line")
		cmd.MarkFlagsMutuallyExclusive("quiet", "columns")
		cmd.MarkFlagsMutuallyExclusive("quiet", "format")
	}
}

// completeFormats completes --format with the named formats of a command
// that prints a list of records. A template is the user's to write.
func completeFormats(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{
		"table\tAligned columns for reading",
		"json\tThe records, as a JSON array",
		"yaml\tThe records, as a YAML sequence",
	}, cobra.ShellCompDirectiveNoFileComp
}

// completeObjectFormats completes --format for a command that prints one
// record.
func completeObjectFormats(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{
		"table\tLaid out for reading",
		"json\tA JSON object",
		"yaml\tA YAML mapping",
	}, cobra.ShellCompDirectiveNoFileComp
}

// render writes p to the command's output in the form the flags added by
// addOutputFlags ask for.
func render(cmd *cobra.Command, p printer.Printable) error {
	return renderTo(cmd, cmd.OutOrStdout(), p)
}

// renderTo writes p to w as render does.
func renderTo(cmd *cobra.Command, w io.Writer, p printer.Printable) error {
	format, _ := cmd.Flags().GetString("format")

	var columns []string
	if cmd.Flags().Lookup("columns") != nil {
		columns, _ = cmd.Flags().GetStringSlice("columns")
	}

	if quiet, _ := cmd.Flags().GetBool("quiet"); quiet {
		return printNames(w, p)
	}

	wide, _ := cmd.Flags().GetBool("wide")

	return printer.Print(p, w, printer.Options{
		Format:     format,
		Columns:    columns,
		AllColumns: wide,
	})
}

// printNames writes what identifies each row -- its Name, or its first
// column if it has none -- one a line, for 'dicer rm $(dicer ps -q)'.
func printNames(w io.Writer, p printer.Printable) error {
	key := p.Columns()[0]
	for _, c := range p.Columns() {
		if c == "Name" {
			key = c
			break
		}
	}

	for _, row := range p.Rows() {
		if _, err := fmt.Fprintln(w, row[key]); err != nil {
			return err
		}
	}
	return nil
}

// confirm asks a yes-or-no question, defaulting to no. Without a terminal it
// returns yes.
func confirm(cmd *cobra.Command, question string) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return true, nil
	}

	cmd.Printf("%s [y/N] ", question)
	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// readSecret reads a secret, such as a token: from a prompt that does not
// echo it on a terminal, or else all of standard input. Surrounding space is
// dropped.
func readSecret(cmd *cobra.Command, prompt string) (string, error) {
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		cmd.PrintErr(prompt)
		secret, err := term.ReadPassword(fd)
		cmd.PrintErrln()
		if err != nil {
			return "", fmt.Errorf("read the secret: %w", err)
		}
		return strings.TrimSpace(string(secret)), nil
	}

	secret, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("read the secret from standard input: %w", err)
	}
	return strings.TrimSpace(string(secret)), nil
}

// age renders a timestamp as a duration before now, e.g. "3 hours ago".
func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	return units.HumanDuration(time.Since(t)) + " ago"
}

// orDash renders an empty string as "-", so a table cell is never blank.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// writeStructured writes v as JSON or YAML, as format asks, for a command
// whose table is not a Printable's.
func writeStructured(w io.Writer, format string, v any) error {
	if !printer.IsStructured(format) {
		return fmt.Errorf("unsupported format %q: want table, json or yaml", format)
	}
	return printer.PrintStructured(v, w, format)
}

// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/cli/printer"
	"github.com/konradasb/dicer/internal/cli/remote"
	"github.com/konradasb/dicer/internal/naming"
)

type printableToken struct {
	Tokens []dicer.Token
}

func (p *printableToken) Columns() []string {
	return []string{"Name", "Scopes", "Created", "Last Used"}
}

func (p *printableToken) Rows() []map[string]any {
	rows := make([]map[string]any, 0, len(p.Tokens))
	for _, t := range p.Tokens {
		rows = append(rows, map[string]any{
			"Name":      t.Name,
			"Scopes":    scopeList(t.Scopes),
			"Created":   age(t.CreateTime),
			"Last Used": age(t.LastUseTime),
		})
	}
	return rows
}

// scopeList writes scopes as --scopes takes them, comma-separated.
func scopeList(scopes []dicer.Scope) string {
	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, string(scope))
	}
	return strings.Join(names, ",")
}

func newTokenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "token",
		Short:   "Manage the tokens the daemon's TCP listener accepts",
		Aliases: []string{"tokens"},
		Long: "Manage the tokens the daemon's TCP listener accepts.\n\n" +
			"Every call to the daemon over TCP needs a token. Make one here, on the\n" +
			"daemon's host, and give it to 'dicer remote create' on the machine that\n" +
			"will use it. The daemon serves the API over TCP only when server.listen\n" +
			"is set in its configuration.",
	}

	cmd.AddCommand(
		newTokenCreateCommand(),
		newTokenListCommand(),
		newTokenRotateCommand(),
		newTokenDeleteCommand(),
	)

	return cmd
}

func newTokenCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Make a token for the daemon's TCP listener",
		Long: "Makes a token for the daemon's TCP listener, and prints it. It is shown\n" +
			"only this once: the daemon keeps only the SHA-256 of its secret.\n\n" +
			"--scopes says what the token allows: * for everything, which is the\n" +
			"default, or RESOURCE:ACTION, such as instances:write. RESOURCE is\n" +
			"instances, snapshots, networks, volumes, images, kernels, tokens or\n" +
			"events, and ACTION is read or write. Events have only read. A write scope\n" +
			"allows reading too. A token can make only tokens its own scopes allow.\n\n" +
			"--secret-stdin reads the token's secret instead of having the daemon make\n" +
			"one: at least 32 letters and digits. It is for a tool that must know the\n" +
			"token before it exists.",
		Example: "  dicer token create laptop\n" +
			"  dicer token create ci --scopes instances:write,images:write",
		Args: one("a name for the token"),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := dicer.TokenSpec{Name: args[0]}
			scopes, _ := cmd.Flags().GetStringSlice("scopes")
			for _, scope := range scopes {
				spec.Scopes = append(spec.Scopes, dicer.Scope(scope))
			}
			if fromStdin, _ := cmd.Flags().GetBool("secret-stdin"); fromStdin {
				secret, err := readSecret(cmd, "Secret: ")
				if err != nil {
					return err
				}
				spec.Secret = secret
			}

			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			issued, err := client.Tokens.Create(cmd.Context(), spec)
			if err != nil {
				return err
			}

			return writeIssuedToken(cmd, client, issued, "created")
		},
	}

	cmd.Flags().StringSlice("scopes", nil, "What the token allows, comma-separated (default: * for everything)")
	cmd.Flags().Bool("secret-stdin", false, "Read the token's secret from standard input")
	cmd.Flags().String("format", "table", "Output format: table for the token alone, json or yaml")
	_ = cmd.RegisterFlagCompletionFunc("format", completeFormats)

	return cmd
}

func newTokenListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List tokens",
		Args:    noArgs,
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			list, err := client.Tokens.List(cmd.Context())
			if err != nil {
				return err
			}

			return render(cmd, &printableToken{Tokens: list})
		},
	}

	addOutputFlags(cmd, true)

	return cmd
}

func newTokenRotateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate NAME",
		Short: "Give a token a new secret",
		Long: "Gives a token a new secret, and prints the token anew. The old one stops\n" +
			"working at once. Give the new one to whatever used the old, with\n" +
			"'dicer remote delete' and 'dicer remote create'.",
		Args:              one("a token name"),
		ValidArgsFunction: complete(1, listTokens),
		RunE: func(cmd *cobra.Command, args []string) error {
			var secret string
			if fromStdin, _ := cmd.Flags().GetBool("secret-stdin"); fromStdin {
				var err error
				if secret, err = readSecret(cmd, "Secret: "); err != nil {
					return err
				}
			}

			client, cleanup, err := newClient(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			issued, err := client.Tokens.Rotate(cmd.Context(), args[0], secret)
			if err != nil {
				return err
			}

			return writeIssuedToken(cmd, client, issued, "rotated")
		},
	}

	cmd.Flags().Bool("secret-stdin", false, "Read the token's new secret from standard input")
	cmd.Flags().String("format", "table", "Output format: table for the token alone, json or yaml")
	_ = cmd.RegisterFlagCompletionFunc("format", completeFormats)

	return cmd
}

func newTokenDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete (NAME... | --all)",
		Short: "Delete one or more tokens, or all of them",
		Long: "Deletes the tokens named, or with --all every token, asking first on a\n" +
			"terminal. A client using a deleted token is refused from its next call.",
		Args:              namesOrAll("token name"),
		Aliases:           []string{"rm", "remove"},
		ValidArgsFunction: complete(0, listTokens),
		RunE: func(cmd *cobra.Command, args []string) error {
			return eachNameOrAll(cmd, args, listTokens, "tokens", func(client *dicer.Client, name string) error {
				if err := client.Tokens.Delete(cmd.Context(), name); err != nil {
					return err
				}

				succeeded(cmd, "Token %s deleted", name)
				return nil
			})
		},
	}
	addDeleteAllFlags(cmd, "tokens")

	return cmd
}

// writeIssuedToken writes a token just made or rotated: as a record with
// --format json or yaml, or else its value alone on standard output, for a
// script to capture, and on standard error the command that uses it.
func writeIssuedToken(cmd *cobra.Command, client *dicer.Client, issued dicer.IssuedToken, done string) error {
	if format, _ := cmd.Flags().GetString("format"); !printer.IsTable(format) {
		r, err := record(issued)
		if err != nil {
			return err
		}
		return writeStructured(cmd.OutOrStdout(), format, r)
	}

	if _, err := fmt.Fprintln(cmd.OutOrStdout(), issued.Value); err != nil {
		return err
	}

	name, address, others := remoteToCreate(cmd, client)
	succeeded(cmd, "Token %s %s. It is not shown again. To reach this daemon from another machine, run there:\n\n"+
		"  dicer remote create %s %s --token %s\n", issued.Name, done, name, address, issued.Value)
	switch {
	case address == "HOST:PORT":
		succeeded(cmd, "Replace HOST:PORT with the address that machine reaches this daemon's TCP listener at.")
	case len(others) > 0:
		succeeded(cmd, "The daemon can also be reached at %s.", strings.Join(others, ", "))
	}
	return nil
}

// remoteToCreate returns the name and address another machine adds the
// daemon as a remote by, and the daemon's other addresses. The name is its
// host's, if that is a valid remote name. The address is the one this
// command reached it at over TCP, or else the first the daemon reports. A
// part it cannot tell is NAME or HOST:PORT.
func remoteToCreate(cmd *cobra.Command, client *dicer.Client) (name, address string, others []string) {
	name, address = "NAME", "HOST:PORT"

	host, err := client.HostInfo(cmd.Context())
	if err != nil {
		return name, address, nil
	}
	if short, _, _ := strings.Cut(host.Hostname, "."); naming.Validate(short) == nil && short != remote.Local {
		name = short
	}

	if t, err := resolveTarget(cmd); err == nil && !t.remote.IsSocket() {
		return name, t.remote.Address, nil
	}
	if len(host.ListenerAddresses) > 0 {
		return name, host.ListenerAddresses[0], host.ListenerAddresses[1:]
	}
	return name, address, nil
}

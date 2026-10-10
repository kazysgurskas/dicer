// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer/internal/cli/remote"
)

type printableRemote struct {
	Remotes []namedRemote
}

// namedRemote is a remote with its name, and whether it is the current one.
type namedRemote struct {
	name    string
	remote  remote.Remote
	current bool
}

// remoteRecord is a remote as JSON and YAML show it, without its token.
type remoteRecord struct {
	Name    string `json:"name"`
	Address string `json:"address"`

	// Auth is socket or token, or empty for neither.
	Auth string `json:"auth,omitempty"`

	Current bool `json:"current"`
}

func (p *printableRemote) Records() any {
	records := make([]remoteRecord, 0, len(p.Remotes))
	for _, r := range p.Remotes {
		records = append(records, remoteRecord{
			Name: r.name, Address: r.remote.Address, Auth: authSummary(r.remote), Current: r.current,
		})
	}
	return records
}

func (p *printableRemote) Columns() []string {
	return []string{"Name", "Address", "Auth", "Current"}
}

func (p *printableRemote) Rows() []map[string]any {
	rows := make([]map[string]any, 0, len(p.Remotes))
	for _, r := range p.Remotes {
		current := ""
		if r.current {
			current = "*"
		}
		rows = append(rows, map[string]any{
			"Name":    r.name,
			"Address": r.remote.Address,
			"Auth":    orDash(authSummary(r.remote)),
			"Current": current,
		})
	}
	return rows
}

// authSummary says in a word how a remote is let in: by the socket's file
// permissions, or with a token. It is empty for neither.
func authSummary(r remote.Remote) string {
	switch {
	case r.IsSocket():
		return "socket"
	case r.Token != "":
		return "token"
	default:
		return ""
	}
}

func newRemoteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remote",
		Short:   "Manage the daemons this client talks to",
		Aliases: []string{"remotes"},
		Long: "Manage the daemons this client talks to.\n\n" +
			"The built-in remote \"" + remote.Local + "\" is the daemon on this machine, on its " +
			"socket. A daemon on another machine is added with the address of its TCP " +
			"listener and a token, which 'dicer token create' makes on the daemon's host.",
	}

	cmd.AddCommand(
		newRemoteCreateCommand(),
		newRemoteListCommand(),
		newRemoteDeleteCommand(),
		newRemoteUseCommand(),
	)

	return cmd
}

func newRemoteCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create NAME ADDRESS",
		Short: "Add a daemon to talk to",
		Long: "Add a daemon to talk to.\n\n" +
			"A daemon's TCP listener, HOST:PORT, is reached with a token, which " +
			"'dicer token create' makes on the daemon's host, printing this command to " +
			"run with it. Give it with --token, paste it when asked, or pipe it in. " +
			"--token keeps it in the shell's history, which the other two do not. The " +
			"token is kept with the remote. It also says how to check that the daemon " +
			"is the one that made it.\n\n" +
			"A unix:// socket takes no token. It is controlled by its file permissions: " +
			"whoever can open it may do anything.",
		Example: "  dicer remote create prod dicer1.example.com:7443\n" +
			"  echo \"$DICER_PROD_TOKEN\" | dicer remote create prod dicer1.example.com:7443\n" +
			"  dicer remote create test unix:///run/dicer-test/dicer.sock",
		Args:    needs([]string{"a name for the remote", "an address: HOST:PORT or unix:///PATH"}),
		Aliases: []string{"new", "add"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, address := args[0], args[1]

			r, err := remote.Parse(address)
			if err != nil {
				return err
			}
			r.Token, _ = cmd.Flags().GetString("token")
			if !r.IsSocket() && r.Token == "" {
				if r.Token, err = readSecret(cmd, "Token: "); err != nil {
					return err
				}
				if r.Token == "" {
					return errors.New("no token given: make one on the daemon's host with " +
						"'dicer token create NAME', and paste it here")
				}
			}

			err = updateRemoteConfig(func(cfg *remote.Config) error {
				if _, err := cfg.Remote(name); err == nil {
					return fmt.Errorf("remote %q already exists", name)
				}
				return cfg.Create(name, r)
			})
			if err != nil {
				return err
			}

			succeeded(cmd, "Remote %s created. Use it with: dicer remote use %s", name, name)

			return nil
		},
	}

	cmd.Flags().String("token", "", "The token to reach a TCP address with, instead of reading it")

	return cmd
}

func newRemoteListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List remotes",
		Args:    noArgs,
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadRemoteConfig()
			if err != nil {
				return err
			}

			current := cfg.CurrentName()
			p := &printableRemote{}
			for _, name := range cfg.Names() {
				r, _ := cfg.Remote(name)
				p.Remotes = append(p.Remotes, namedRemote{name: name, remote: r, current: name == current})
			}

			return render(cmd, p)
		},
	}

	addOutputFlags(cmd, true)

	return cmd
}

func newRemoteDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete (NAME | --all)",
		Short: "Forget a remote, or all of them",
		Long: "Forgets a remote, or with --all every remote added, asking first on a\n" +
			"terminal. The built-in " + remote.Local + " remote cannot be forgotten.",
		Args: func(cmd *cobra.Command, args []string) error {
			if all, _ := cmd.Flags().GetBool("all"); all {
				return noArgs(cmd, args)
			}
			return one("a remote name")(cmd, args)
		},
		Aliases:           []string{"rm", "remove"},
		ValidArgsFunction: completeRemotes,
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateRemoteConfig(func(cfg *remote.Config) error {
				names := args
				if all, _ := cmd.Flags().GetBool("all"); all {
					names = slices.DeleteFunc(cfg.Names(), func(n string) bool { return n == remote.Local })
					if ok, err := confirmDeleteAll(cmd, "remotes", names); err != nil || !ok {
						return err
					}
				}

				for _, name := range names {
					if err := cfg.Delete(name); err != nil {
						return err
					}
					succeeded(cmd, "Remote %s deleted", name)
				}
				return nil
			})
		},
	}
	addDeleteAllFlags(cmd, "remotes")

	return cmd
}

func newRemoteUseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Make a remote the current one",
		Long: "Make a remote the one commands talk to unless told otherwise " +
			"with --remote or $" + remoteEnv + ".",
		Args:              one("a remote name"),
		ValidArgsFunction: completeRemotes,
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateRemoteConfig(func(cfg *remote.Config) error {
				if err := cfg.Use(args[0]); err != nil {
					return err
				}
				succeeded(cmd, "Now using remote %s", args[0])
				return nil
			})
		},
	}
}

// loadRemoteConfig loads the remotes this client knows.
func loadRemoteConfig() (*remote.Config, error) {
	dir, err := remote.ConfigDir()
	if err != nil {
		return nil, err
	}
	return remote.Load(dir)
}

// updateRemoteConfig loads the remotes, changes them and saves them. Nothing
// is saved if change fails.
func updateRemoteConfig(change func(*remote.Config) error) error {
	dir, err := remote.ConfigDir()
	if err != nil {
		return err
	}
	cfg, err := remote.Load(dir)
	if err != nil {
		return err
	}

	if err := change(cfg); err != nil {
		return err
	}

	return cfg.Save(dir)
}

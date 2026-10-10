// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/konradasb/dicer"
	"github.com/konradasb/dicer/internal/humanize"
)

// printableProcess lists the processes in an instance's guest, a row each.
// Its column names are also a --format template's fields and JSON's keys.
type printableProcess struct {
	Processes []dicer.Process
}

func (p *printableProcess) Records() any { return p.Processes }

func (p *printableProcess) Columns() []string {
	return []string{"PID", "PPID", "User", "State", "Started", "CPUTime", "RSS", "Command"}
}

func (p *printableProcess) Rows() []map[string]any {
	rows := make([]map[string]any, 0, len(p.Processes))
	for _, process := range p.Processes {
		// A process without a command line, such as a zombie, is shown by
		// its name in brackets.
		command := strings.Join(process.Command, " ")
		if command == "" {
			command = "[" + process.Name + "]"
		}
		rows = append(rows, map[string]any{
			"PID":     strconv.Itoa(process.PID),
			"PPID":    strconv.Itoa(process.PPID),
			"User":    process.User,
			"State":   process.State,
			"Started": age(process.StartTime),
			"CPUTime": process.CPUTime.Round(10 * time.Millisecond).String(),
			"RSS":     humanize.Bytes(process.ResidentMemoryBytes),
			"Command": command,
		})
	}
	return rows
}

func newInstanceTopCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "top NAME",
		Short: "List the processes running in an instance",
		Long: "Lists the processes running in a running instance's guest, as its guest\n" +
			"agent reads them from the guest's /proc. Kernel threads are left out;\n" +
			"dicer-init and the agent are shown, since they run in the guest too.\n\n" +
			"PIDs are the guest's, the ones a command run with dicer exec sees. State is\n" +
			"the kernel's code for the process: R running, S sleeping, D waiting on I/O,\n" +
			"Z zombie, T stopped. CPUTime is the CPU time used since the process started,\n" +
			"and RSS the guest memory it has resident.\n\n" +
			"The column names are also the fields of a --format template. --format json\n" +
			"and yaml give each process's record instead, with times and sizes as numbers.",
		Example: "  dicer top web\n" +
			"  dicer top web --format '{{.PID}}\\t{{.Command}}'\n" +
			"  dicer top web --format json",
		Args:              one("an instance name"),
		ValidArgsFunction: complete(1, instancesIn(dicer.InstanceStateRunning)),
		RunE:              runInstanceTopCommand,
	}

	addOutputFlags(cmd, true)

	return cmd
}

func runInstanceTopCommand(cmd *cobra.Command, args []string) error {
	client, cleanup, err := newClient(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	processes, err := client.Instances.Processes(contextOf(cmd), args[0])
	if err != nil {
		return err
	}
	return render(cmd, &printableProcess{Processes: processes})
}

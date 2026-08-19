// Command qianshou is the single binary of the Qianshou control plane
// (ADR 0001). Subcommands separate the two runtime roles:
//
//	qianshou serve  central control server; owns SQLite and the API
//	qianshou run    runner process; registers with the server (M2-02)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/depscli"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
	"github.com/ai-daming/qianshou/apps/control/internal/runner"
	"github.com/ai-daming/qianshou/apps/control/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := server.Serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			os.Exit(1)
		}
	case "can-start":
		depscli.Main()
	case "run":
		if err := runner.Run(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "run:", err)
			os.Exit(1)
		}
	case "inspect-frame":
		if err := inspectFrame(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "inspect-frame:", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: qianshou <serve|run|can-start|inspect-frame> [flags]")
}

func inspectFrame(args []string) error {
	flags := flag.NewFlagSet("inspect-frame", flag.ContinueOnError)
	home := flags.String("home", config.DefaultHome(), "central Qianshou home (server must be stopped)")
	runID := flags.String("run", "", "AgentRun id")
	sequence := flags.Int("sequence", 0, "Vendor frame sequence")
	if err := flags.Parse(args); err != nil {
		return err
	}
	raw, err := ledger.ReadRawFrame(context.Background(), *home, *runID, *sequence)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(raw)
	return err
}

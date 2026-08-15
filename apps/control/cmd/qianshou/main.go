// Command qianshou is the single binary of the Qianshou control plane
// (ADR 0001). Subcommands separate the two runtime roles:
//
//	qianshou serve  central control server; owns SQLite and the API
//	qianshou run    runner process; registers with the server (M2-02)
package main

import (
	"fmt"
	"os"

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
	case "run":
		if err := runner.Run(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "run:", err)
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
	fmt.Fprintln(os.Stderr, "usage: qianshou <serve|run> [flags]")
}

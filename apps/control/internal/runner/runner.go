// Package runner hosts the Qianshou runner role. During M1 agent commands
// execute inside the server process behind the executor interface; this
// standalone `qianshou run` process becomes functional in M2-02 when it
// registers with the central server over the idempotent command protocol.
package runner

import (
	"flag"
	"fmt"
)

func Run(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	serverURL := flags.String("server", "http://127.0.0.1:41727", "central server URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return fmt.Errorf("runner process is not implemented before M2-02 (would register with %s)", *serverURL)
}

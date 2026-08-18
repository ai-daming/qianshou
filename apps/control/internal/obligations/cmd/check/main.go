// Command check validates the ghfacts obligations manifest (schema,
// package-qualified referential integrity, README agreement, full test
// classification) and runs every cited test. CI entry point.
package main

import (
	"fmt"
	"os"

	"github.com/ai-daming/qianshou/apps/control/internal/obligations"
)

func main() {
	if err := obligations.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "check-obligations:", err)
		os.Exit(1)
	}
	fmt.Println("check-obligations: manifest verified and all citations pass")
}

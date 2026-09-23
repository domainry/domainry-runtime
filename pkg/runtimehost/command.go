package runtimehost

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/domainry/domainry-runtime/pkg/coderuntime"
)

// RunCommand validates the stable Runtime process command line before starting
// the server. Runtime configuration remains environment-owned; command
// arguments are intentionally unsupported.
func RunCommand(arguments []string, stdout io.Writer, stderr io.Writer, options Options) int {
	if len(arguments) == 1 && arguments[0] == coderuntime.WorkerArgument {
		return coderuntime.RunWorker(os.Stdin, stdout, stderr)
	}
	return runCommand(arguments, stdout, stderr, func() error { return Run(options) })
}

func runCommand(arguments []string, stdout io.Writer, stderr io.Writer, serve func() error) int {
	flags := flag.NewFlagSet("domainry-runtime", flag.ContinueOnError)
	flags.SetOutput(stderr)
	writeUsage := func(output io.Writer) {
		_, _ = fmt.Fprintln(output, "Usage: domainry-runtime")
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Runtime configuration is supplied through environment variables.")
	}
	flags.Usage = func() { writeUsage(stderr) }
	if len(arguments) == 1 && (arguments[0] == "-h" || arguments[0] == "--help") {
		writeUsage(stdout)
		return 0
	}
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "domainry-runtime: unexpected arguments: %v\n", flags.Args())
		flags.Usage()
		return 2
	}
	if err := serve(); err != nil {
		_, _ = fmt.Fprintf(stderr, "domainry-runtime: %v\n", err)
		return 1
	}
	return 0
}

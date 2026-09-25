package runtimehost

import (
	"encoding/json"
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
	if len(arguments) > 0 && arguments[0] == "check" {
		return runCheckCommand(
			arguments[1:],
			stdout,
			stderr,
			func() ProjectCheckResult { return CheckProject(options) },
			func() ProjectCheckResult { return CheckModel(options) },
		)
	}
	return runCommand(arguments, stdout, stderr, func() error { return Run(options) })
}

func runCommand(arguments []string, stdout io.Writer, stderr io.Writer, serve func() error) int {
	flags := flag.NewFlagSet("domainry-runtime", flag.ContinueOnError)
	flags.SetOutput(stderr)
	writeUsage := func(output io.Writer) {
		_, _ = fmt.Fprintln(output, "Usage: domainry-runtime [check]")
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Runtime configuration is supplied through environment variables.")
		_, _ = fmt.Fprintln(output, "The check command validates backend/model.json and project definitions without starting Runtime.")
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

func runCheckCommand(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	checkProject func() ProjectCheckResult,
	checkModel func() ProjectCheckResult,
) int {
	flags := flag.NewFlagSet("domainry-runtime check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	modelOnly := flags.Bool("model-only", false, "validate only backend/model.json before code-owned definitions exist")
	writeUsage := func(output io.Writer) {
		_, _ = fmt.Fprintln(output, "Usage: domainry-runtime check [--model-only]")
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Validates the project model and, by default, code-owned definitions without starting Runtime.")
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
		_, _ = fmt.Fprintf(stderr, "domainry-runtime check: unexpected arguments: %v\n", flags.Args())
		flags.Usage()
		return 2
	}
	check := checkProject
	if *modelOnly {
		check = checkModel
	}
	result := check()
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		_, _ = fmt.Fprintf(stderr, "domainry-runtime check: encode result: %v\n", err)
		return 1
	}
	if result.State != projectCheckStateValid {
		return 1
	}
	return 0
}

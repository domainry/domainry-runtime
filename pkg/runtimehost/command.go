package runtimehost

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/domainry/domainry-runtime/runtime/bootstrap"
)

// RunCommand validates the stable Runtime process command line before starting
// the server. Runtime configuration remains environment-owned; command
// arguments are intentionally unsupported.
func RunCommand(arguments []string, stdout io.Writer, stderr io.Writer, options Options) int {
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
		if exitCode, handled := writeDefinitionUpgradePlan(err, stdout, stderr); handled {
			return exitCode
		}
		_, _ = fmt.Fprintf(stderr, "domainry-runtime: %v\n", err)
		return 1
	}
	return 0
}

// writeDefinitionUpgradePlan handles DEFINITION_UPGRADE_MODE=plan: the plan
// is the only stdout output, as a single JSON document, and the process exits
// successfully without having served HTTP.
func writeDefinitionUpgradePlan(err error, stdout io.Writer, stderr io.Writer) (int, bool) {
	var planRequested *bootstrap.DefinitionUpgradePlanRequested
	if !errors.As(err, &planRequested) {
		return 0, false
	}
	payload, encodeErr := planRequested.PlanJSON()
	if encodeErr != nil {
		_, _ = fmt.Fprintf(stderr, "domainry-runtime: encode definition upgrade plan: %v\n", encodeErr)
		return 1, true
	}
	if _, writeErr := fmt.Fprintln(stdout, string(payload)); writeErr != nil {
		_, _ = fmt.Fprintf(stderr, "domainry-runtime: write definition upgrade plan: %v\n", writeErr)
		return 1, true
	}
	return 0, true
}

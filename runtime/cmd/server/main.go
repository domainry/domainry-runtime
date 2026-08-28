package main

import (
	"io"
	"os"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/pkg/runtimehost"
)

var runtimeVersion = "dev"
var runtimeExit = os.Exit

func main() {
	runtimeExit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runtimehost.RunCommand(args, stdout, stderr, runtimehost.Options{
		Identity: runtimehost.BuildIdentity{
			RuntimeVersion:            runtimeVersion,
			RuntimeextContractVersion: runtimeext.ContractVersion,
			RuntimeextContractSHA256:  runtimeext.ContractSHA256,
			ConnectorContractVersion:  connector.ContractVersion,
			ConnectorContractSHA256:   connector.ContractSHA256,
		},
	})
}

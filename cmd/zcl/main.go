package main

import (
	"os"

	"github.com/marcohefti/zero-context-lab/internal/cli"
)

var version = "0.0.0-dev"

func main() {
	r := cli.Runner{
		Version: version,
	}
	os.Exit(r.Run(os.Args[1:]))
}

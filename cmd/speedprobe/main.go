// Command speedprobe is a multi-provider internet speed test.
package main

import (
	"os"

	"github.com/akshtray/speedprobe/internal/cli"
)

// version is set at build time by GoReleaser via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Main(version, os.Args[1:], os.Stdout, os.Stderr))
}

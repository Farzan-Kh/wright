// Command wright is the entry point for the Wright CLI.
package main

import (
	"os"

	"github.com/farzan-kh/wright/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}

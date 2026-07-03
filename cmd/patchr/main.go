// Command patchr is the entry point for the Patchr CLI.
package main

import (
	"os"

	"github.com/farzan-kh/patchr/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}

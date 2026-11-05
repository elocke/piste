// Command piste is the Nix configuration management CLI.
package main

import (
	"os"

	"github.com/elocke/piste/cli/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}

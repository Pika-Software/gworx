package main

import (
	"os"

	"github.com/Pika-Software/gworx/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.Options{Version: version, GUI: runGUI}))
}

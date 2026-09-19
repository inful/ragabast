package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/ragabast/cmd"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=vX.Y.Z"
//
// Defaults to "dev" for `go run` / local builds. Surfaces via
// `ragabast --version` (wired through kong.VersionFlag in cmd.CLI).
var version = "dev"

func main() {
	var cli cmd.CLI

	ctx := kong.Parse(
		&cli,
		kong.Name("ragabast"),
		kong.Description("RAG/LLM Information Retrieval System with Docbuilder support"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
		kong.Vars{
			"version": version,
		},
	)

	err := ctx.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

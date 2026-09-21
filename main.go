package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/alecthomas/kong"
	"github.com/ragabast/cmd"
	"github.com/ragabast/internal/logging"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=vX.Y.Z"
//
// Defaults to "dev" for `go run` / local builds. Surfaces via
// `ragabast --version` (wired through kong.VersionFlag in cmd.CLI).
var version = "dev"

func main() {
	// Initialize the process-wide structured logger before
	// anything else logs. RAGABAST_LOG_FORMAT=json switches
	// the output to JSON for log shippers; the default is
	// slog.NewTextHandler, which is line-based key=value pairs
	// that stay grep-friendly for operators.
	logging.Init()

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
		slog.Error("command failed", "error", err)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/ragabast/cmd"
)

func main() {
	var cli cmd.CLI

	ctx := kong.Parse(&cli,
		kong.Name("ragabast"),
		kong.Description("RAG/LLM Information Retrieval System with Docubilder support"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
	)

	err := ctx.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

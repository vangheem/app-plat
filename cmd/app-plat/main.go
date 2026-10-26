package main

import (
	"fmt"
	"os"

	"github.com/app-plat/app-plat/internal/cli"
	"github.com/app-plat/app-plat/internal/mcp"
)

func main() {
	args := os.Args[1:]
	if cli.FirstCommand(args) == "mcp" {
		apiURL, err := cli.APIURLFlag(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		if err := mcp.Serve(os.Stdin, os.Stdout, os.Stderr, mcp.Options{APIURL: apiURL}); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}
	os.Exit(cli.Main(args))
}

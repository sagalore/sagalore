package main

import (
	"fmt"
	"os"

	"github.com/sagalore/sagalore/mcp"
)

func main() {
	if err := mcp.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

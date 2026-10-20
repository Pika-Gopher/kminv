package main

import (
	"fmt"
	"os"

	"kminv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: kminv <database-path>")
		os.Exit(2)
	}

	db, err := kminv.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "kminv: open database: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stdout, "Welcome to kminv.")
	if err := runREPL(db, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "kminv: %v\n", err)
		os.Exit(1)
	}
}

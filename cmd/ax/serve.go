package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/xnet-admin-1/ax/internal/web"
)

func runServe() {
	// Always enable debug in serve mode for now
	initDebug(cliFlags{debug: true})
	database, gw := openDB()

	webFS, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		fmt.Fprintln(os.Stderr, "embed:", err)
		os.Exit(1)
	}

	port := 8080
	bind := "0.0.0.0"
	password := ""

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p", "-P", "--port":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &port)
			}
		case "-b", "--bind":
			if i+1 < len(args) {
				i++
				bind = args[i]
			}
		case "--password":
			if i+1 < len(args) {
				i++
				password = args[i]
			}
		}
	}

	srv := web.NewServer(database, gw, webFS, bind, port, password)
	if err := srv.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}

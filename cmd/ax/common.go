// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1

package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/xnet-admin-1/ax/internal/db"
	"github.com/xnet-admin-1/ax/internal/debug"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/gateway"
	"github.com/xnet-admin-1/ax/internal/mcp"
	"github.com/xnet-admin-1/ax/tui"
)

func initDebug(f cliFlags) {
	if f.debug {
		debug.D.SetLevel(debug.Verbose)
	}
}

func openDB() (*sql.DB, *gateway.Router) {
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "db:", err)
		os.Exit(1)
	}
	gw := gateway.NewRouter(database)
	return database, gw
}

func runListModels() {
	database, gw := openDB()
	defer database.Close()
	models := gw.ListModels()
	for _, m := range models {
		fmt.Println(m)
	}
}

func runTUI(f cliFlags) {
	initDebug(f)
	database, gw := openDB()
	defer database.Close()

	eng := &engine.Engine{DB: database, Gateway: gw}
	if f.model != "" {
		eng.Model = f.model
	}

	backend := engine.NewLocal(eng.DB, eng.Gateway)
	if f.trustAll {
		backend.TrustAll = true
	}
	backend.McpMgr = mcp.NewManager(eng.DB)
	backend.McpMgr.ConnectEnabled()

	opts := tui.LaunchOpts{Agent: f.agent}
	if f.resume {
		var convID string
		database.QueryRow("SELECT id FROM conversations ORDER BY updated_at DESC LIMIT 1").Scan(&convID)
		if convID != "" {
			opts.ResumeConvID = convID
		}
	}

	m := tui.NewLocalWithOpts(backend, opts)
	if err := tui.RunWithModel(m); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

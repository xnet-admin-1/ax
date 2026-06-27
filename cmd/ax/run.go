package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/xnet-admin-1/ax/internal/db"
	"github.com/xnet-admin-1/ax/internal/debug"
	"github.com/xnet-admin-1/ax/internal/engine"
	"github.com/xnet-admin-1/ax/internal/gateway"
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

func runCLI(f cliFlags) {
	initDebug(f)
	debug.D.Info("cli: prompt=%q model=%s resume=%v", f.prompt, f.model, f.resume)
	database, gw := openDB()
	defer database.Close()

	eng := &engine.Engine{DB: database, Gateway: gw}
	if f.model != "" {
		eng.Model = f.model
	}

	var msgs []engine.Message

	if f.resume {
		var convID string
		database.QueryRow("SELECT id FROM conversations ORDER BY updated_at DESC LIMIT 1").Scan(&convID)
		if convID != "" {
			rows, err := database.Query("SELECT role,content FROM messages WHERE conv_id=? ORDER BY created_at", convID)
			if err == nil {
				for rows.Next() {
					var m engine.Message
					rows.Scan(&m.Role, &m.Content)
					if m.Role == "user" || m.Role == "assistant" {
						msgs = append(msgs, m)
					}
				}
				rows.Close()
			}
			for _, m := range msgs {
				if m.Role == "user" {
					fmt.Printf("\033[1mYou:\033[0m %s\n", m.Content)
				} else {
					fmt.Printf("\033[1mAssistant:\033[0m %s\n", m.Content)
				}
			}
			fmt.Println("---")
		}
	}

	msgs = append(msgs, engine.Message{Role: "user", Content: f.prompt})

	var resp strings.Builder
	err := eng.Chat(context.Background(), msgs, func(ev engine.Event) {
		switch ev.Type {
		case "delta":
			resp.WriteString(ev.Delta)
		case "error":
			fmt.Fprintln(os.Stderr, ev.Error)
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	output := resp.String()
	if i := strings.Index(output, "</thought>"); i >= 0 {
		output = strings.TrimSpace(output[i+len("</thought>"):])
	}
	fmt.Println(output)
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

func runServe() {
	fmt.Println("ax serve — not yet implemented")
}

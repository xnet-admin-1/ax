package main

import (
	"fmt"
	"log"
	"os"

	"github.com/charmbracelet/lipgloss"
)

func init() {
	lipgloss.SetHasDarkBackground(true)
	if f, err := os.OpenFile("/tmp/ax.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(f)
	}
}

var version = "dev"

type cliFlags struct {
	model    string
	prompt   string
	agent    string
	resume   bool
	models   bool
	trustAll bool
	serve    bool
}

func parseFlags() cliFlags {
	var f cliFlags
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "serve":
			f.serve = true
		case "--version", "-v":
			fmt.Println("ax", version)
			os.Exit(0)
		case "--help", "-h":
			printUsage()
			os.Exit(0)
		case "--models":
			f.models = true
		case "--trust-all":
			f.trustAll = true
		case "-r":
			f.resume = true
		case "-m":
			if i+1 < len(args) {
				i++
				f.model = args[i]
			}
		case "-p":
			if i+1 < len(args) {
				i++
				f.prompt = args[i]
			}
		case "-a":
			if i+1 < len(args) {
				i++
				f.agent = args[i]
			}
		}
	}
	return f
}

func main() {
	f := parseFlags()
	switch {
	case f.serve:
		runServe()
	case f.models:
		runListModels()
	case f.prompt != "":
		runCLI(f)
	default:
		runTUI(f)
	}
}

func printUsage() {
	fmt.Println(`ax - terminal AI agent

Usage:
  ax              Launch TUI
  ax serve        Start web server + API
  ax -p "prompt"  One-shot CLI mode

Flags:
  -m model        Select model (provider/model)
  -a agent        Start with agent
  -r              Resume last conversation
  --models        List available models
  --trust-all     Trust all tool executions
  -v              Print version`)
}

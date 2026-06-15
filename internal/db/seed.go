package db

import (
	"database/sql"
	"encoding/json"
	"os"
)

type gatewayConfig struct {
	Providers []struct {
		Name         string   `json:"name"`
		APIKey       string   `json:"apiKey"`
		APIBase      string   `json:"apiBase"`
		Enabled      bool     `json:"enabled"`
		CachedModels []string `json:"cachedModels"`
	} `json:"providers"`
}

func SeedProviders(db *sql.DB, configPath string) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM providers").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg gatewayConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	for _, p := range cfg.Providers {
		models, _ := json.Marshal(p.CachedModels)
		enabled := 0
		if p.Enabled {
			enabled = 1
		}
		_, _ = db.Exec("INSERT OR IGNORE INTO providers(name, api_key, api_base, enabled, models) VALUES(?,?,?,?,?)",
			p.Name, p.APIKey, p.APIBase, enabled, string(models))
	}

	_, _ = db.Exec("INSERT OR IGNORE INTO settings(key, value) VALUES('selected_model', 'google-ai-studio/models/gemma-4-31b-it')")
	// Update only if empty
	db.Exec("UPDATE settings SET value='google-ai-studio/models/gemma-4-31b-it' WHERE key='selected_model' AND value=''")
	return nil
}

func SeedFromDefaults(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM providers").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	defaults := []struct {
		name    string
		apiBase string
		enabled int
	}{
		{"google-ai-studio", "https://generativelanguage.googleapis.com/v1beta/openai", 1},
		{"zen", "https://opencode.ai/zen/v1", 1},
		{"cloudflare", "https://api.cloudflare.com/client/v4/accounts/9ca284e2c57a6df3171b4d4ded3e1434/ai/v1", 1},
		{"nvidia", "https://integrate.api.nvidia.com/v1", 1},
		{"openrouter", "https://openrouter.ai/api/v1", 1},
		{"cohere", "https://api.cohere.ai/compatibility/v1", 1},
		{"pollinations-pollen", "https://gen.pollinations.ai/v1", 1},
		{"bedrock-mantle", "https://bedrock-mantle.us-west-2.api.aws/v1", 0},
	}

	for _, d := range defaults {
		_, _ = db.Exec("INSERT OR IGNORE INTO providers(name, api_key, api_base, enabled, models) VALUES(?,?,?,?,?)",
			d.name, "", d.apiBase, d.enabled, "[]")
	}

	db.Exec("UPDATE settings SET value='google-ai-studio/models/gemma-4-31b-it' WHERE key='selected_model' AND value=''")
	return nil
}

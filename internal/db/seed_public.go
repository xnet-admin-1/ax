package db

import "database/sql"

func SeedExampleProviders(db *sql.DB) {
	// Seed example provider entries (no keys - BYOK)
	examples := []struct{ name, base string }{
		{"google-ai-studio", "https://generativelanguage.googleapis.com/v1beta/openai"},
		{"openrouter", "https://openrouter.ai/api/v1"},
		{"cloudflare", "https://api.cloudflare.com/client/v4/accounts/YOUR_ACCOUNT_ID/ai/v1"},
		{"nvidia", "https://integrate.api.nvidia.com/v1"},
		{"bedrock-mantle", "https://bedrock-mantle.us-west-2.api.aws/v1"},
		{"zen", "https://opencode.ai/zen/v1"},
		{"cohere", "https://api.cohere.ai/compatibility/v1"},
		{"pollinations-pollen", "https://gen.pollinations.ai/v1"},
	}
	for _, e := range examples {
		db.Exec("INSERT OR IGNORE INTO providers(name, api_key, api_base, enabled, models) VALUES(?,?,?,?,?)",
			e.name, "", e.base, 0, "[]")
	}
}

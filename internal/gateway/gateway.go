package gateway

import (
	"github.com/xnet-admin-1/ax/internal/debug"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Provider struct {
	Name    string   `json:"name"`
	APIKey  string   `json:"apiKey"`
	APIBase string   `json:"apiBase"`
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models"`
}

type Router struct {
	db *sql.DB
}

func NewRouter(db *sql.DB) *Router {
	return &Router{db: db}
}

func (r *Router) Resolve(displayID string) (apiBase, apiKey, model string, err error) {
	debug.D.Verbose("gateway resolve: %s", displayID)
	idx := strings.Index(displayID, "/")
	if idx <= 0 {
		return "", "", "", fmt.Errorf("invalid model ID: %s", displayID)
	}
	provName := displayID[:idx]
	model = displayID[idx+1:]

	var key, base string
	var enabled int
	err = r.db.QueryRow("SELECT api_key, api_base, enabled FROM providers WHERE name=?", provName).Scan(&key, &base, &enabled)
	if err != nil {
		return "", "", "", fmt.Errorf("provider %q not found", provName)
	}
	if enabled == 0 {
		return "", "", "", fmt.Errorf("provider %q is disabled", provName)
	}
	return base, key, model, nil
}

func (r *Router) ListModels() []string {
	rows, err := r.db.Query("SELECT name, models FROM providers WHERE enabled=1")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name, modelsJSON string
		rows.Scan(&name, &modelsJSON)
		var models []string
		json.Unmarshal([]byte(modelsJSON), &models)
		for _, m := range models {
			out = append(out, name+"/"+m)
		}
	}
	return out
}

func (r *Router) ListProviders() ([]Provider, error) {
	rows, err := r.db.Query("SELECT name, api_key, api_base, enabled, models FROM providers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		var modelsJSON string
		rows.Scan(&p.Name, &p.APIKey, &p.APIBase, &p.Enabled, &modelsJSON)
		json.Unmarshal([]byte(modelsJSON), &p.Models)
		out = append(out, p)
	}
	return out, nil
}

func (r *Router) SaveProvider(p *Provider) error {
	models, _ := json.Marshal(p.Models)
	_, err := r.db.Exec(
		"INSERT OR REPLACE INTO providers(name, api_key, api_base, enabled, models) VALUES(?,?,?,?,?)",
		p.Name, p.APIKey, p.APIBase, p.Enabled, string(models),
	)
	return err
}

func (r *Router) DeleteProvider(name string) error {
	_, err := r.db.Exec("DELETE FROM providers WHERE name=?", name)
	return err
}

func (r *Router) DiscoverModels(name string) ([]string, error) {
	var key, base string
	if err := r.db.QueryRow("SELECT api_key, api_base FROM providers WHERE name=?", name).Scan(&key, &base); err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", base+"/models", nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	var out []string
	for _, m := range result.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

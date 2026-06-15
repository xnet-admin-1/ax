package provider

import (
	"database/sql"
	"encoding/json"
	"strings"
)

type Provider struct {
	Name    string   `json:"name"`
	APIKey  string   `json:"apiKey"`
	APIBase string   `json:"apiBase"`
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models"`
}

type ModelConfig struct {
	ModelID         string   `json:"modelId"`
	ToolsOverride   *bool    `json:"toolsOverride,omitempty"`
	VisionOverride  *bool    `json:"visionOverride,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	ReasoningEffort *string  `json:"reasoningEffort,omitempty"`
	ContextTokens   int      `json:"contextTokens"`
	AutoCompact     bool     `json:"autoCompact,omitempty"`
}

type Profile struct {
	ID              string                `json:"id"`
	BuiltinID       string                `json:"builtinId"`
	Label           string                `json:"label"`
	Provider        *Provider             `json:"provider,omitempty"`
	APIKey          string                `json:"apiKey"`
	APIBase         string                `json:"apiBase"`
	SelectedModelID string                `json:"selectedModelId"`
	IsActive        bool                  `json:"isActive"`
	ModelConfigs    map[string]ModelConfig `json:"modelConfigs,omitempty"`
}

type Service struct {
	DB *sql.DB
}

func (s *Service) List() ([]Provider, error) {
	rows, err := s.DB.Query("SELECT name, api_key, api_base, enabled, models FROM providers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		var enabled int
		var models string
		if err := rows.Scan(&p.Name, &p.APIKey, &p.APIBase, &enabled, &models); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		_ = json.Unmarshal([]byte(models), &p.Models)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) Get(name string) (*Provider, error) {
	var p Provider
	var enabled int
	var models string
	err := s.DB.QueryRow("SELECT name, api_key, api_base, enabled, models FROM providers WHERE name=?", name).
		Scan(&p.Name, &p.APIKey, &p.APIBase, &enabled, &models)
	if err != nil {
		return nil, err
	}
	p.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(models), &p.Models)
	return &p, nil
}

func (s *Service) Save(p *Provider) error {
	models, _ := json.Marshal(p.Models)
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	_, err := s.DB.Exec("INSERT OR REPLACE INTO providers(name, api_key, api_base, enabled, models) VALUES(?,?,?,?,?)",
		p.Name, p.APIKey, p.APIBase, enabled, string(models))
	return err
}

func (s *Service) Delete(name string) error {
	_, err := s.DB.Exec("DELETE FROM providers WHERE name=?", name)
	return err
}

func (s *Service) Toggle(name string) error {
	_, err := s.DB.Exec("UPDATE providers SET enabled = CASE WHEN enabled=1 THEN 0 ELSE 1 END WHERE name=?", name)
	return err
}

func (s *Service) GetActive() *Provider {
	var model string
	if s.DB.QueryRow("SELECT value FROM settings WHERE key='selected_model'").Scan(&model) != nil || model == "" {
		return nil
	}
	prefix := strings.SplitN(model, "/", 2)[0]
	p, err := s.Get(prefix)
	if err != nil {
		return nil
	}
	return p
}

func (s *Service) SetActive(id string) error {
	return nil
}

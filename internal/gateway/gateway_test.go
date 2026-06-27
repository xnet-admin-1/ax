// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package gateway

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func setupDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE providers (name TEXT PRIMARY KEY, api_key TEXT, api_base TEXT, enabled INTEGER, models TEXT)`)
	db.Exec(`INSERT INTO providers VALUES('google-ai-studio','key1','https://ai.google',1,'["models/gemma-4-31b-it"]')`)
	db.Exec(`INSERT INTO providers VALUES('github-models','key2','https://github.ai',1,'["openai/gpt-5"]')`)
	db.Exec(`INSERT INTO providers VALUES('disabled-prov','key3','https://disabled.ai',0,'["m1"]')`)
	return db
}

func TestResolveValid(t *testing.T) {
	r := NewRouter(setupDB(t))
	base, key, model, err := r.Resolve("google-ai-studio/models/gemma-4-31b-it")
	if err != nil {
		t.Fatal(err)
	}
	if base != "https://ai.google" || key != "key1" || model != "models/gemma-4-31b-it" {
		t.Fatalf("got base=%s key=%s model=%s", base, key, model)
	}
}

func TestResolveMultipleSlashes(t *testing.T) {
	r := NewRouter(setupDB(t))
	base, _, model, err := r.Resolve("github-models/openai/gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	if base != "https://github.ai" || model != "openai/gpt-5" {
		t.Fatalf("got base=%s model=%s", base, model)
	}
}

func TestResolveNoSlash(t *testing.T) {
	r := NewRouter(setupDB(t))
	if _, _, _, err := r.Resolve("noslash"); err == nil {
		t.Fatal("expected error for model with no slash")
	}
}

func TestResolveDisabled(t *testing.T) {
	r := NewRouter(setupDB(t))
	if _, _, _, err := r.Resolve("disabled-prov/m1"); err == nil {
		t.Fatal("expected error for disabled provider")
	}
}

func TestResolveUnknown(t *testing.T) {
	r := NewRouter(setupDB(t))
	if _, _, _, err := r.Resolve("unknown-prov/model"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestListModels(t *testing.T) {
	r := NewRouter(setupDB(t))
	models := r.ListModels()
	expected := map[string]bool{
		"google-ai-studio/models/gemma-4-31b-it": true,
		"github-models/openai/gpt-5":             true,
	}
	if len(models) != len(expected) {
		t.Fatalf("expected %d models, got %d: %v", len(expected), len(models), models)
	}
	for _, m := range models {
		if !expected[m] {
			t.Fatalf("unexpected model: %s", m)
		}
	}
}

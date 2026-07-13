// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package db

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DataDir can be set externally (e.g. via --data-dir flag) to override default resolution.
var DataDir string

func DefaultPath() string {
	dir := resolveDataDir()
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "ax.db")
}

// resolveDataDir determines the data directory in priority order:
// 1. Explicit DataDir (set via --data-dir flag)
// 2. Portable: .ax/ directory next to the binary
// 3. Portable: ax.db file next to the binary
// 4. Default: ~/.ax/
func resolveDataDir() string {
	// 1. Explicit override
	if DataDir != "" {
		return DataDir
	}

	// 2-3. Check next to binary (portable mode)
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		exeDir := filepath.Dir(exe)

		// Check for .ax/ directory next to binary
		portableDir := filepath.Join(exeDir, ".ax")
		if info, err := os.Stat(portableDir); err == nil && info.IsDir() {
			return portableDir
		}

		// Check for ax.db next to binary
		portableDB := filepath.Join(exeDir, "ax.db")
		if _, err := os.Stat(portableDB); err == nil {
			return exeDir
		}
	}

	// 4. Default: ~/.ax/
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ax")
}

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	// Seed providers: file override → embedded → example defaults
	configPath := filepath.Join(resolveDataDir(), "gateway-config.json")
	if err := SeedProviders(db, configPath); err != nil {
		if err := SeedFromEmbedded(db); err != nil {
			SeedExampleProviders(db)
		}
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS providers (
			name    TEXT PRIMARY KEY,
			api_key TEXT NOT NULL DEFAULT '',
			api_base TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			models  TEXT NOT NULL DEFAULT '[]'
		);
		CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS settings_kv (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS conversations (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL DEFAULT 'New Chat',
			model      TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS messages (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			conv_id TEXT NOT NULL,
			role    TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			tool_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_msg_conv ON messages(conv_id, id);
		CREATE TABLE IF NOT EXISTS memories (
			key TEXT PRIMARY KEY,
			content TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT 'general',
			createdAt INTEGER NOT NULL DEFAULT (strftime('%s','now')),
			updatedAt INTEGER NOT NULL DEFAULT (strftime('%s','now'))
		);
		INSERT OR IGNORE INTO settings(key, value) VALUES('selected_model', '');
		INSERT OR IGNORE INTO settings(key, value) VALUES('search_provider_url', 'https://search.xnet.ngo');
		INSERT OR IGNORE INTO settings(key, value) VALUES('task_model_title', '');
		INSERT OR IGNORE INTO settings(key, value) VALUES('task_model_summary', '');
		INSERT OR IGNORE INTO settings(key, value) VALUES('auto_compact_threshold', '75');
	`)
	return err
}

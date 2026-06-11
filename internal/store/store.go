// Package store persists campaigns, characters, and the campaign log in
// SQLite (modernc.org/sqlite — pure Go, so the binary cross-compiles with
// CGO_ENABLED=0 into containers).
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite handle.
type Store struct {
	DB *sql.DB
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite serializes writers; a single connection avoids SQLITE_BUSY
	// churn under the MCP server's low concurrency.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying handle.
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	_, err := s.DB.Exec(schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS campaigns (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
  description TEXT NOT NULL DEFAULT '',
  setting     TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'prep' CHECK (status IN ('prep','active','archived')),
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS characters (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id),
  name        TEXT NOT NULL COLLATE NOCASE,
  player      TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'pc' CHECK (kind IN ('pc','npc')),
  species     TEXT NOT NULL DEFAULT '',
  class       TEXT NOT NULL DEFAULT '',
  background  TEXT NOT NULL DEFAULT '',
  alignment   TEXT NOT NULL DEFAULT '',
  level       INTEGER NOT NULL DEFAULT 1,
  xp          INTEGER NOT NULL DEFAULT 0,
  abilities   TEXT NOT NULL DEFAULT '{}',
  skills      TEXT NOT NULL DEFAULT '[]',
  saves       TEXT NOT NULL DEFAULT '[]',
  hp_max      INTEGER NOT NULL DEFAULT 0,
  hp_current  INTEGER NOT NULL DEFAULT 0,
  ac          INTEGER NOT NULL DEFAULT 10,
  speed       INTEGER NOT NULL DEFAULT 30,
  inventory   TEXT NOT NULL DEFAULT '[]',
  spell_slots TEXT NOT NULL DEFAULT '{}',
  features    TEXT NOT NULL DEFAULT '[]',
  notes       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (campaign_id, name)
);

CREATE TABLE IF NOT EXISTS campaign_log (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  campaign_id INTEGER NOT NULL REFERENCES campaigns(id),
  session_no  INTEGER NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  summary     TEXT NOT NULL,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE (campaign_id, session_no)
);

CREATE TABLE IF NOT EXISTS ref_cache (
  cache_key  TEXT PRIMARY KEY,
  body       TEXT NOT NULL,
  fetched_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`

// CacheGet returns a cached reference body, ok=false on miss.
func (s *Store) CacheGet(key string) (string, bool) {
	var body string
	err := s.DB.QueryRow(`SELECT body FROM ref_cache WHERE cache_key = ?`, key).Scan(&body)
	if err != nil {
		return "", false
	}
	return body, true
}

// CachePut stores a reference body (replacing any prior entry).
func (s *Store) CachePut(key, body string) error {
	_, err := s.DB.Exec(`INSERT INTO ref_cache (cache_key, body, fetched_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT (cache_key) DO UPDATE SET body = excluded.body, fetched_at = excluded.fetched_at`, key, body)
	return err
}

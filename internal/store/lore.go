package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LoreEntry is one piece of campaign world-building: a location, NPC,
// faction, plot thread, item, or event. DMSecret entries are the DM's
// private notes — the player-facing HTTP surface never serves them.
type LoreEntry struct {
	ID         int64  `json:"id"`
	CampaignID int64  `json:"campaign_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	DMSecret   bool   `json:"dm_secret"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// LoreKinds are the accepted kinds (mirrors the table CHECK).
var LoreKinds = []string{"location", "npc", "faction", "plot", "item", "event", "other"}

// CanonicalLoreKind validates/normalizes a kind; "" maps to "other".
func CanonicalLoreKind(kind string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return "other", true
	}
	for _, v := range LoreKinds {
		if v == k {
			return k, true
		}
	}
	return "", false
}

const loreCols = `id, campaign_id, kind, name, body, dm_secret, created_at, updated_at`

func scanLore(row interface{ Scan(...any) error }) (LoreEntry, error) {
	var e LoreEntry
	var secret int
	err := row.Scan(&e.ID, &e.CampaignID, &e.Kind, &e.Name, &e.Body, &secret, &e.CreatedAt, &e.UpdatedAt)
	e.DMSecret = secret != 0
	return e, err
}

// SetLore upserts a lore entry by (campaign, name).
func (s *Store) SetLore(campaignID int64, kind, name, body string, dmSecret bool) (LoreEntry, error) {
	secret := 0
	if dmSecret {
		secret = 1
	}
	var id int64
	err := s.DB.QueryRow(`INSERT INTO lore (campaign_id, kind, name, body, dm_secret)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (campaign_id, name) DO UPDATE SET
			kind = excluded.kind, body = excluded.body, dm_secret = excluded.dm_secret,
			updated_at = datetime('now')
		RETURNING id`, campaignID, kind, name, body, secret).Scan(&id)
	if err != nil {
		return LoreEntry{}, fmt.Errorf("set lore: %w", err)
	}
	return scanLore(s.DB.QueryRow(`SELECT `+loreCols+` FROM lore WHERE id = ?`, id))
}

// LoreByName fetches one entry by name (case-insensitive).
func (s *Store) LoreByName(campaignID int64, name string) (LoreEntry, error) {
	e, err := scanLore(s.DB.QueryRow(`SELECT `+loreCols+` FROM lore WHERE campaign_id = ? AND name = ?`,
		campaignID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return LoreEntry{}, ErrNotFound
	}
	return e, err
}

// LoreList lists entries, optionally filtered by kind. includeSecrets=false
// drops dm_secret rows entirely.
func (s *Store) LoreList(campaignID int64, kind string, includeSecrets bool) ([]LoreEntry, error) {
	q := `SELECT ` + loreCols + ` FROM lore WHERE campaign_id = ?`
	args := []any{campaignID}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	if !includeSecrets {
		q += ` AND dm_secret = 0`
	}
	q += ` ORDER BY kind, name`
	return s.loreQuery(q, args...)
}

// LoreSearch finds entries whose name or body contains the query.
func (s *Store) LoreSearch(campaignID int64, query string, includeSecrets bool) ([]LoreEntry, error) {
	q := `SELECT ` + loreCols + ` FROM lore WHERE campaign_id = ? AND (name LIKE ? OR body LIKE ?)`
	pat := "%" + query + "%"
	args := []any{campaignID, pat, pat}
	if !includeSecrets {
		q += ` AND dm_secret = 0`
	}
	q += ` ORDER BY kind, name LIMIT 25`
	return s.loreQuery(q, args...)
}

// DeleteLore removes an entry by name.
func (s *Store) DeleteLore(campaignID int64, name string) error {
	res, err := s.DB.Exec(`DELETE FROM lore WHERE campaign_id = ? AND name = ?`, campaignID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no lore entry named %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Store) loreQuery(q string, args ...any) ([]LoreEntry, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoreEntry
	for rows.Next() {
		e, err := scanLore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

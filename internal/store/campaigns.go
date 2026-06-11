package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Campaign is a campaign record.
type Campaign struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Setting     string `json:"setting"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// LogEntry is one archived session in a campaign's log.
type LogEntry struct {
	ID         int64  `json:"id"`
	CampaignID int64  `json:"campaign_id"`
	SessionNo  int    `json:"session_no"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	CreatedAt  string `json:"created_at"`
}

// ErrNotFound marks a missing campaign/character.
var ErrNotFound = errors.New("not found")

const campaignCols = `id, name, description, setting, status, created_at, updated_at`

func scanCampaign(row interface{ Scan(...any) error }) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Setting, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// CreateCampaign inserts a campaign; name is unique (case-insensitive).
func (s *Store) CreateCampaign(name, description, setting string) (Campaign, error) {
	res, err := s.DB.Exec(`INSERT INTO campaigns (name, description, setting) VALUES (?, ?, ?)`,
		name, description, setting)
	if err != nil {
		return Campaign{}, fmt.Errorf("create campaign: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.CampaignByID(id)
}

// CampaignByID fetches one campaign by id.
func (s *Store) CampaignByID(id int64) (Campaign, error) {
	c, err := scanCampaign(s.DB.QueryRow(`SELECT `+campaignCols+` FROM campaigns WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	return c, err
}

// CampaignByName fetches one campaign by name (case-insensitive).
func (s *Store) CampaignByName(name string) (Campaign, error) {
	c, err := scanCampaign(s.DB.QueryRow(`SELECT `+campaignCols+` FROM campaigns WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	return c, err
}

// Campaigns lists all campaigns, newest first.
func (s *Store) Campaigns() ([]Campaign, error) {
	rows, err := s.DB.Query(`SELECT ` + campaignCols + ` FROM campaigns ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveCampaign finds the campaign a tool call means. Empty name resolves
// only when unambiguous: a single campaign, or a single non-archived one.
func (s *Store) ResolveCampaign(name string) (Campaign, error) {
	if name != "" {
		c, err := s.CampaignByName(name)
		if errors.Is(err, ErrNotFound) {
			return Campaign{}, fmt.Errorf("no campaign named %q — create it with dnd_campaign_create", name)
		}
		return c, err
	}
	all, err := s.Campaigns()
	if err != nil {
		return Campaign{}, err
	}
	switch len(all) {
	case 0:
		return Campaign{}, errors.New("no campaigns exist yet — create one with dnd_campaign_create")
	case 1:
		return all[0], nil
	}
	var live []Campaign
	for _, c := range all {
		if c.Status != "archived" {
			live = append(live, c)
		}
	}
	if len(live) == 1 {
		return live[0], nil
	}
	return Campaign{}, fmt.Errorf("%d campaigns exist — pass the campaign name explicitly", len(all))
}

// SetCampaignStatus updates a campaign's status (prep|active|archived).
func (s *Store) SetCampaignStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE campaigns SET status = ?, updated_at = datetime('now') WHERE id = ?`, status, id)
	return err
}

// AppendLog appends a session entry; session_no auto-increments per campaign.
func (s *Store) AppendLog(campaignID int64, title, summary string) (LogEntry, error) {
	var next int
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(session_no), 0) + 1 FROM campaign_log WHERE campaign_id = ?`,
		campaignID).Scan(&next); err != nil {
		return LogEntry{}, err
	}
	res, err := s.DB.Exec(`INSERT INTO campaign_log (campaign_id, session_no, title, summary) VALUES (?, ?, ?, ?)`,
		campaignID, next, title, summary)
	if err != nil {
		return LogEntry{}, err
	}
	id, _ := res.LastInsertId()
	var e LogEntry
	err = s.DB.QueryRow(`SELECT id, campaign_id, session_no, title, summary, created_at FROM campaign_log WHERE id = ?`, id).
		Scan(&e.ID, &e.CampaignID, &e.SessionNo, &e.Title, &e.Summary, &e.CreatedAt)
	return e, err
}

// RecentLog returns up to n log entries for a campaign, newest first.
func (s *Store) RecentLog(campaignID int64, n int) ([]LogEntry, error) {
	rows, err := s.DB.Query(`SELECT id, campaign_id, session_no, title, summary, created_at
		FROM campaign_log WHERE campaign_id = ? ORDER BY session_no DESC LIMIT ?`, campaignID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.CampaignID, &e.SessionNo, &e.Title, &e.Summary, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

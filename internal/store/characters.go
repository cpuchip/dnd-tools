package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Item is one inventory line.
type Item struct {
	Name  string `json:"name"`
	Qty   int    `json:"qty"`
	Notes string `json:"notes,omitempty"`
}

// Attack is a structured weapon/attack entry. To-hit derives from the
// ability modifier + proficiency (+ magic bonus); damage from the dice +
// ability modifier (+ magic bonus). MagicBonus models a +1 weapon (applies
// to both rolls).
type Attack struct {
	Name       string `json:"name"`
	Ability    string `json:"ability,omitempty"` // canonical key; "" = str
	Proficient bool   `json:"proficient"`
	MagicBonus int    `json:"magic_bonus,omitempty"`
	Damage     string `json:"damage"` // dice, e.g. "1d8"
	DamageType string `json:"damage_type,omitempty"`
	Range      string `json:"range,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

// Spell is one known/prepared spell. Level 0 = cantrip (no slot).
type Spell struct {
	Name     string `json:"name"`
	Level    int    `json:"level"`
	Key      string `json:"key,omitempty"` // Open5e key for dnd_ref_get
	Prepared bool   `json:"prepared,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// Character is a full sheet. JSON-typed columns are unmarshalled on read.
// The flat shape is deliberate — close enough to common character-JSON
// exports that a D&D Beyond import could map onto it later.
type Character struct {
	ID         int64          `json:"id"`
	CampaignID int64          `json:"campaign_id"`
	Campaign   string         `json:"campaign"`
	Name       string         `json:"name"`
	Player     string         `json:"player"`
	Kind       string         `json:"kind"`
	Species    string         `json:"species"`
	Class      string         `json:"class"`
	Background string         `json:"background"`
	Alignment  string         `json:"alignment"`
	Level      int            `json:"level"`
	XP         int            `json:"xp"`
	Abilities  map[string]int `json:"abilities"`
	Skills     []string       `json:"skills"`
	Saves      []string       `json:"saves"`
	HPMax      int            `json:"hp_max"`
	HPCurrent  int            `json:"hp_current"`
	AC         int            `json:"ac"`
	Speed      int            `json:"speed"`
	Inventory  []Item         `json:"inventory"`
	SpellSlots map[string]int `json:"spell_slots"`
	Attacks    []Attack       `json:"attacks"`
	Spells     []Spell        `json:"spells"`
	Conditions []string       `json:"conditions"`
	Features   []string       `json:"features"`
	Notes      string         `json:"notes"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

const charCols = `c.id, c.campaign_id, ca.name, c.name, c.player, c.kind, c.species, c.class,
	c.background, c.alignment, c.level, c.xp, c.abilities, c.skills, c.saves,
	c.hp_max, c.hp_current, c.ac, c.speed, c.inventory, c.spell_slots,
	c.attacks, c.spells, c.conditions, c.features, c.notes,
	c.created_at, c.updated_at`

const charFrom = ` FROM characters c JOIN campaigns ca ON ca.id = c.campaign_id `

func scanCharacter(row interface{ Scan(...any) error }) (Character, error) {
	var c Character
	var abilities, skills, saves, inventory, slots, attacks, spells, conditions, features string
	err := row.Scan(&c.ID, &c.CampaignID, &c.Campaign, &c.Name, &c.Player, &c.Kind, &c.Species, &c.Class,
		&c.Background, &c.Alignment, &c.Level, &c.XP, &abilities, &skills, &saves,
		&c.HPMax, &c.HPCurrent, &c.AC, &c.Speed, &inventory, &slots,
		&attacks, &spells, &conditions, &features, &c.Notes,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	// Stored JSON is written by us; a decode failure means a corrupt row and
	// should surface, not be silently zeroed.
	for _, dec := range []struct {
		raw string
		dst any
	}{
		{abilities, &c.Abilities}, {skills, &c.Skills}, {saves, &c.Saves},
		{inventory, &c.Inventory}, {slots, &c.SpellSlots},
		{attacks, &c.Attacks}, {spells, &c.Spells}, {conditions, &c.Conditions},
		{features, &c.Features},
	} {
		if err := json.Unmarshal([]byte(dec.raw), dec.dst); err != nil {
			return c, fmt.Errorf("character %s: corrupt column json: %w", c.Name, err)
		}
	}
	return c, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // marshalling maps/slices of basic types cannot fail
	}
	return string(b)
}

// CreateCharacter inserts a sheet. Caller fills derived fields (HP, saves).
func (s *Store) CreateCharacter(c Character) (Character, error) {
	if c.Abilities == nil {
		c.Abilities = map[string]int{}
	}
	if c.SpellSlots == nil {
		c.SpellSlots = map[string]int{}
	}
	if c.Kind == "" {
		c.Kind = "pc"
	}
	res, err := s.DB.Exec(`INSERT INTO characters
		(campaign_id, name, player, kind, species, class, background, alignment, level, xp,
		 abilities, skills, saves, hp_max, hp_current, ac, speed, inventory, spell_slots,
		 attacks, spells, conditions, features, notes)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.CampaignID, c.Name, c.Player, c.Kind, c.Species, c.Class, c.Background, c.Alignment, c.Level, c.XP,
		mustJSON(c.Abilities), mustJSON(emptyToList(c.Skills)), mustJSON(emptyToList(c.Saves)),
		c.HPMax, c.HPCurrent, c.AC, c.Speed,
		mustJSON(emptyToItems(c.Inventory)), mustJSON(c.SpellSlots),
		mustJSON(emptyToAttacks(c.Attacks)), mustJSON(emptyToSpells(c.Spells)), mustJSON(emptyToList(c.Conditions)),
		mustJSON(emptyToList(c.Features)), c.Notes)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Character{}, fmt.Errorf("a character named %q already exists in this campaign", c.Name)
		}
		return Character{}, err
	}
	id, _ := res.LastInsertId()
	return s.CharacterByID(id)
}

func emptyToList(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func emptyToItems(v []Item) []Item {
	if v == nil {
		return []Item{}
	}
	return v
}

func emptyToAttacks(v []Attack) []Attack {
	if v == nil {
		return []Attack{}
	}
	return v
}

func emptyToSpells(v []Spell) []Spell {
	if v == nil {
		return []Spell{}
	}
	return v
}

// CharacterByID fetches one character.
func (s *Store) CharacterByID(id int64) (Character, error) {
	c, err := scanCharacter(s.DB.QueryRow(`SELECT `+charCols+charFrom+`WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Character{}, ErrNotFound
	}
	return c, err
}

// FindCharacter resolves a character by name, optionally scoped to a campaign.
// Without a campaign scope, a name that matches in several campaigns errors
// with the list rather than guessing.
func (s *Store) FindCharacter(name string, campaignID int64) (Character, error) {
	q := `SELECT ` + charCols + charFrom + `WHERE c.name = ?`
	args := []any{name}
	if campaignID > 0 {
		q += ` AND c.campaign_id = ?`
		args = append(args, campaignID)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return Character{}, err
	}
	defer rows.Close()
	var found []Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return Character{}, err
		}
		found = append(found, c)
	}
	if err := rows.Err(); err != nil {
		return Character{}, err
	}
	switch len(found) {
	case 0:
		return Character{}, fmt.Errorf("no character named %q%s", name, scopeHint(campaignID))
	case 1:
		return found[0], nil
	}
	var where []string
	for _, c := range found {
		where = append(where, c.Campaign)
	}
	return Character{}, fmt.Errorf("%q exists in several campaigns (%s) — pass the campaign name", name, strings.Join(where, ", "))
}

func scopeHint(campaignID int64) string {
	if campaignID > 0 {
		return " in this campaign"
	}
	return ""
}

// CharactersForCampaign lists a campaign's roster (PCs first, then name).
func (s *Store) CharactersForCampaign(campaignID int64) ([]Character, error) {
	rows, err := s.DB.Query(`SELECT `+charCols+charFrom+
		`WHERE c.campaign_id = ? ORDER BY c.kind, c.name`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveCharacter writes every mutable column of c back by id.
func (s *Store) SaveCharacter(c Character) (Character, error) {
	_, err := s.DB.Exec(`UPDATE characters SET
		player=?, kind=?, species=?, class=?, background=?, alignment=?, level=?, xp=?,
		abilities=?, skills=?, saves=?, hp_max=?, hp_current=?, ac=?, speed=?,
		inventory=?, spell_slots=?, attacks=?, spells=?, conditions=?, features=?, notes=?,
		updated_at=datetime('now')
		WHERE id=?`,
		c.Player, c.Kind, c.Species, c.Class, c.Background, c.Alignment, c.Level, c.XP,
		mustJSON(c.Abilities), mustJSON(emptyToList(c.Skills)), mustJSON(emptyToList(c.Saves)),
		c.HPMax, c.HPCurrent, c.AC, c.Speed,
		mustJSON(emptyToItems(c.Inventory)), mustJSON(c.SpellSlots),
		mustJSON(emptyToAttacks(c.Attacks)), mustJSON(emptyToSpells(c.Spells)), mustJSON(emptyToList(c.Conditions)),
		mustJSON(emptyToList(c.Features)), c.Notes,
		c.ID)
	if err != nil {
		return Character{}, err
	}
	return s.CharacterByID(c.ID)
}

// FindCharacterByPlayer resolves the character a PLAYER runs in a campaign —
// the binding /check, /attack, and the /char panel use (player = the chat
// display name). Falls back to a character NAMED like the player.
func (s *Store) FindCharacterByPlayer(player string, campaignID int64) (Character, error) {
	rows, err := s.DB.Query(`SELECT `+charCols+charFrom+
		`WHERE c.campaign_id = ? AND (c.player = ? COLLATE NOCASE OR c.name = ?) ORDER BY c.player = ? COLLATE NOCASE DESC`,
		campaignID, player, player, player)
	if err != nil {
		return Character{}, err
	}
	defer rows.Close()
	var found []Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return Character{}, err
		}
		found = append(found, c)
	}
	if err := rows.Err(); err != nil {
		return Character{}, err
	}
	if len(found) == 0 {
		return Character{}, fmt.Errorf("no character bound to player %q in this campaign — set the sheet's player field (or name a character after them)", player)
	}
	return found[0], nil
}

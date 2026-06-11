package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCampaignRoundtrip(t *testing.T) {
	s := testStore(t)

	c, err := s.CreateCampaign("Mines of Khazgar", "A delve gone wrong.", "Forgotten foothills")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "prep" {
		t.Errorf("status = %q, want prep", c.Status)
	}

	// Case-insensitive name lookup.
	got, err := s.CampaignByName("mines of khazgar")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != c.ID {
		t.Errorf("lookup id = %d, want %d", got.ID, c.ID)
	}

	// Single campaign resolves without a name.
	r, err := s.ResolveCampaign("")
	if err != nil || r.ID != c.ID {
		t.Errorf("ResolveCampaign(\"\") = %+v, %v", r, err)
	}

	// Duplicate name rejected.
	if _, err := s.CreateCampaign("MINES OF KHAZGAR", "", ""); err == nil {
		t.Error("duplicate campaign name should fail")
	}

	// Log entries auto-number.
	e1, err := s.AppendLog(c.ID, "The Door", "Found the sealed door.")
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := s.AppendLog(c.ID, "", "Opened it. Regretted it.")
	if e1.SessionNo != 1 || e2.SessionNo != 2 {
		t.Errorf("session numbers = %d, %d, want 1, 2", e1.SessionNo, e2.SessionNo)
	}
	log, err := s.RecentLog(c.ID, 5)
	if err != nil || len(log) != 2 || log[0].SessionNo != 2 {
		t.Errorf("RecentLog = %+v, %v", log, err)
	}
}

func TestCharacterRoundtrip(t *testing.T) {
	s := testStore(t)
	camp, err := s.CreateCampaign("Test", "", "")
	if err != nil {
		t.Fatal(err)
	}

	c, err := s.CreateCharacter(Character{
		CampaignID: camp.ID,
		Name:       "Thorin Oakenshield",
		Player:     "Party",
		Kind:       "pc",
		Species:    "Dwarf",
		Class:      "Fighter",
		Level:      1,
		Abilities:  map[string]int{"str": 16, "dex": 12, "con": 14, "int": 10, "wis": 11, "cha": 9},
		Skills:     []string{"athletics", "intimidation"},
		Saves:      []string{"str", "con"},
		HPMax:      12, HPCurrent: 12, AC: 16, Speed: 25,
		Inventory: []Item{{Name: "Battleaxe", Qty: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Campaign != "Test" {
		t.Errorf("joined campaign name = %q", c.Campaign)
	}

	// Case-insensitive find, unscoped.
	got, err := s.FindCharacter("thorin oakenshield", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != c.ID || got.Abilities["str"] != 16 || len(got.Inventory) != 1 {
		t.Errorf("FindCharacter = %+v", got)
	}

	// Duplicate within campaign rejected.
	if _, err := s.CreateCharacter(Character{CampaignID: camp.ID, Name: "THORIN OAKENSHIELD"}); err == nil {
		t.Error("duplicate character should fail")
	}

	// Same name in ANOTHER campaign is fine — and unscoped find then errors.
	camp2, _ := s.CreateCampaign("Other", "", "")
	if _, err := s.CreateCharacter(Character{CampaignID: camp2.ID, Name: "Thorin Oakenshield"}); err != nil {
		t.Fatalf("same name, other campaign: %v", err)
	}
	if _, err := s.FindCharacter("Thorin Oakenshield", 0); err == nil {
		t.Error("ambiguous unscoped find should error")
	}
	if _, err := s.FindCharacter("Thorin Oakenshield", camp.ID); err != nil {
		t.Errorf("scoped find: %v", err)
	}

	// Update roundtrip.
	got.HPCurrent = 5
	got.Inventory = append(got.Inventory, Item{Name: "Rope", Qty: 2, Notes: "50 ft"})
	saved, err := s.SaveCharacter(got)
	if err != nil {
		t.Fatal(err)
	}
	if saved.HPCurrent != 5 || len(saved.Inventory) != 2 {
		t.Errorf("SaveCharacter = %+v", saved)
	}

	// Missing character.
	if _, err := s.FindCharacter("Nobody", 0); err == nil {
		t.Error("missing character should error")
	}
	if _, err := s.CharacterByID(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("CharacterByID(9999) err = %v, want ErrNotFound", err)
	}
}

func TestRefCache(t *testing.T) {
	s := testStore(t)
	if _, ok := s.CacheGet("k"); ok {
		t.Error("empty cache should miss")
	}
	if err := s.CachePut("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, ok := s.CacheGet("k"); !ok || v != "v1" {
		t.Errorf("CacheGet = %q, %v", v, ok)
	}
	if err := s.CachePut("k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.CacheGet("k"); v != "v2" {
		t.Errorf("CacheGet after replace = %q", v)
	}
}

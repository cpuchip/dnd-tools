package store

import "testing"

func TestLoreAndBinding(t *testing.T) {
	s := testStore(t)
	camp, err := s.CreateCampaign("World", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Lore upsert + secret filtering.
	if _, err := s.SetLore(camp.ID, "location", "Brine Cave", "Smells of herring.", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetLore(camp.ID, "plot", "The Sealed Door", "Grimble is the lost heir.", true); err != nil {
		t.Fatal(err)
	}

	public, err := s.LoreList(camp.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 1 || public[0].Name != "Brine Cave" {
		t.Errorf("public lore = %+v", public)
	}
	all, err := s.LoreList(camp.ID, "", true)
	if err != nil || len(all) != 2 {
		t.Errorf("all lore = %+v, %v", all, err)
	}

	// Upsert replaces by (campaign, name), case-insensitive.
	if _, err := s.SetLore(camp.ID, "npc", "brine cave", "Now an npc?! Updated body.", false); err != nil {
		t.Fatal(err)
	}
	e, err := s.LoreByName(camp.ID, "BRINE CAVE")
	if err != nil || e.Kind != "npc" {
		t.Errorf("upsert result = %+v, %v", e, err)
	}

	// Search hits body text, secrets only when asked.
	hits, _ := s.LoreSearch(camp.ID, "heir", false)
	if len(hits) != 0 {
		t.Errorf("secret leaked into public search: %+v", hits)
	}
	hits, _ = s.LoreSearch(camp.ID, "heir", true)
	if len(hits) != 1 {
		t.Errorf("secret search = %+v", hits)
	}

	// Room binding: one campaign per room, rebind moves it.
	if err := s.BindRoom(camp.ID, "room-1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.CampaignByRoom("room-1")
	if err != nil || got.ID != camp.ID {
		t.Errorf("CampaignByRoom = %+v, %v", got, err)
	}
	camp2, _ := s.CreateCampaign("Other", "", "")
	if err := s.BindRoom(camp2.ID, "room-1"); err != nil {
		t.Fatal(err)
	}
	got, err = s.CampaignByRoom("room-1")
	if err != nil || got.ID != camp2.ID {
		t.Errorf("rebind: CampaignByRoom = %+v, %v", got, err)
	}
	if _, err := s.CampaignByRoom("room-2"); err == nil {
		t.Error("unbound room should error")
	}

	// Delete.
	if err := s.DeleteLore(camp.ID, "Brine Cave"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteLore(camp.ID, "Brine Cave"); err == nil {
		t.Error("double delete should error")
	}
}

func TestFindCharacterByPlayer(t *testing.T) {
	s := testStore(t)
	camp, _ := s.CreateCampaign("Test", "", "")
	if _, err := s.CreateCharacter(Character{CampaignID: camp.ID, Name: "Vexa", Player: "Michael Stufflebeam"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCharacter(Character{CampaignID: camp.ID, Name: "Thorin", Player: "Party"}); err != nil {
		t.Fatal(err)
	}

	c, err := s.FindCharacterByPlayer("michael stufflebeam", camp.ID)
	if err != nil || c.Name != "Vexa" {
		t.Errorf("by player = %+v, %v", c, err)
	}
	// Fallback: a character NAMED like the player.
	c, err = s.FindCharacterByPlayer("Thorin", camp.ID)
	if err != nil || c.Name != "Thorin" {
		t.Errorf("by name fallback = %+v, %v", c, err)
	}
	if _, err := s.FindCharacterByPlayer("Nobody", camp.ID); err == nil {
		t.Error("unbound player should error")
	}

	// Attacks/spells/conditions roundtrip through the new columns.
	c.Attacks = []Attack{{Name: "Axe", Proficient: true, Damage: "1d12", DamageType: "slashing"}}
	c.Spells = []Spell{{Name: "Light", Level: 0, Prepared: true}}
	c.Conditions = []string{"poisoned"}
	saved, err := s.SaveCharacter(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Attacks) != 1 || saved.Attacks[0].Name != "Axe" ||
		len(saved.Spells) != 1 || len(saved.Conditions) != 1 {
		t.Errorf("v0.2 columns roundtrip = %+v", saved)
	}
}

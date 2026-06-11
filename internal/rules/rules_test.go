package rules

import "testing"

func TestAbilityMod(t *testing.T) {
	cases := map[int]int{1: -5, 3: -4, 5: -3, 7: -2, 8: -1, 9: -1, 10: 0, 11: 0,
		12: 1, 13: 1, 14: 2, 15: 2, 16: 3, 18: 4, 20: 5, 30: 10}
	for score, want := range cases {
		if got := AbilityMod(score); got != want {
			t.Errorf("AbilityMod(%d) = %d, want %d", score, got, want)
		}
	}
}

func TestProficiencyBonus(t *testing.T) {
	cases := map[int]int{1: 2, 4: 2, 5: 3, 8: 3, 9: 4, 12: 4, 13: 5, 16: 5, 17: 6, 20: 6}
	for level, want := range cases {
		if got := ProficiencyBonus(level); got != want {
			t.Errorf("ProficiencyBonus(%d) = %d, want %d", level, got, want)
		}
	}
}

func TestMaxHP(t *testing.T) {
	// Fighter (d10), CON 14 (+2): level 1 = 12; each level +8 (avg 6 + 2).
	if got := MaxHP(10, 1, 2); got != 12 {
		t.Errorf("level-1 fighter HP = %d, want 12", got)
	}
	if got := MaxHP(10, 3, 2); got != 28 {
		t.Errorf("level-3 fighter HP = %d, want 28", got)
	}
	// CON penalty can't drop a level's gain below 1.
	if got := MaxHP(6, 2, -4); got < 2 {
		t.Errorf("HP with heavy CON penalty = %d, want >= 2", got)
	}
}

func TestLevelForXP(t *testing.T) {
	cases := map[int]int{0: 1, 299: 1, 300: 2, 899: 2, 900: 3, 355000: 20, 999999: 20}
	for xp, want := range cases {
		if got := LevelForXP(xp); got != want {
			t.Errorf("LevelForXP(%d) = %d, want %d", xp, got, want)
		}
	}
}

func TestResolveCheck(t *testing.T) {
	abilities := map[string]int{"str": 16, "dex": 14, "con": 14, "int": 10, "wis": 12, "cha": 8}
	profSkills := []string{"perception", "athletics"}
	profSaves := []string{"str", "con"}

	// Proficient skill: WIS +1 + prof +2 = +3.
	c, err := ResolveCheck("Perception", abilities, profSkills, profSaves, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mod != 3 || !c.Proficient {
		t.Errorf("perception = %+v, want mod 3 proficient", c)
	}

	// Non-proficient skill: DEX +2 only.
	c, err = ResolveCheck("stealth", abilities, profSkills, profSaves, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mod != 2 || c.Proficient {
		t.Errorf("stealth = %+v, want mod 2 not proficient", c)
	}

	// Multi-word skill.
	if _, err := ResolveCheck("sleight of hand", abilities, nil, nil, 1); err != nil {
		t.Errorf("sleight of hand: %v", err)
	}

	// Save with proficiency: STR +3 + prof +2.
	c, err = ResolveCheck("str save", abilities, profSkills, profSaves, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mod != 5 {
		t.Errorf("str save = %+v, want mod 5", c)
	}

	// Long save form, not proficient: WIS +1.
	c, err = ResolveCheck("wisdom saving throw", abilities, profSkills, profSaves, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mod != 1 || c.Proficient {
		t.Errorf("wis save = %+v, want mod 1 not proficient", c)
	}

	// Initiative = DEX.
	c, err = ResolveCheck("initiative", abilities, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mod != 2 {
		t.Errorf("initiative = %+v, want mod 2", c)
	}

	// Raw ability.
	c, err = ResolveCheck("strength", abilities, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mod != 3 {
		t.Errorf("strength = %+v, want mod 3", c)
	}

	if _, err := ResolveCheck("basket weaving", abilities, nil, nil, 1); err == nil {
		t.Error("unknown check should error")
	}
}

func TestRollSuggestion(t *testing.T) {
	c := Check{Label: "Perception (WIS)", Mod: 5}
	want := "/roll 1d20+5 [Thorin — Perception (WIS)]"
	if got := c.RollSuggestion("Thorin"); got != want {
		t.Errorf("RollSuggestion = %q, want %q", got, want)
	}
	neg := Check{Label: "Strength check", Mod: -1}
	if got := neg.RollSuggestion("Pip"); got != "/roll 1d20-1 [Pip — Strength check]" {
		t.Errorf("negative RollSuggestion = %q", got)
	}
}

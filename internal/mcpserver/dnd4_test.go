package mcpserver

import "testing"

func TestParseAttack(t *testing.T) {
	a, err := parseAttack("Longsword: 1d8 slashing")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Longsword" || a.Damage != "1d8" || a.DamageType != "slashing" ||
		a.Ability != "" || !a.Proficient || a.MagicBonus != 0 {
		t.Errorf("longsword = %+v", a)
	}

	a, err = parseAttack("Dagger: 1d4 piercing dex range 20/60")
	if err != nil {
		t.Fatal(err)
	}
	if a.Ability != "dex" || a.Range != "20/60" || a.DamageType != "piercing" {
		t.Errorf("dagger = %+v", a)
	}

	a, err = parseAttack("Flame Tongue: 2d6 fire +1")
	if err != nil {
		t.Fatal(err)
	}
	if a.MagicBonus != 1 || a.Damage != "2d6" || a.DamageType != "fire" {
		t.Errorf("flame tongue = %+v", a)
	}

	a, err = parseAttack("Improvised: 1d4 bludgeoning noprof")
	if err != nil {
		t.Fatal(err)
	}
	if a.Proficient {
		t.Errorf("noprof should clear proficiency: %+v", a)
	}

	a, err = parseAttack(`{"name":"Bite","damage":"1d6","proficient":true,"damage_type":"piercing"}`)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Bite" || a.Damage != "1d6" {
		t.Errorf("json attack = %+v", a)
	}

	if _, err := parseAttack("no colon here"); err == nil {
		t.Error("missing colon should error")
	}
	if _, err := parseAttack("Fists: zero dice"); err == nil {
		t.Error("missing dice should error")
	}
}

func TestParseSpells(t *testing.T) {
	sp, err := parseSpells("Fire Bolt@0, Fireball@3, Light")
	if err != nil {
		t.Fatal(err)
	}
	if len(sp) != 3 {
		t.Fatalf("got %d spells", len(sp))
	}
	if sp[0].Name != "Fire Bolt" || sp[0].Level != 0 {
		t.Errorf("fire bolt = %+v", sp[0])
	}
	if sp[1].Name != "Fireball" || sp[1].Level != 3 {
		t.Errorf("fireball = %+v", sp[1])
	}
	if sp[2].Name != "Light" || sp[2].Level != 0 {
		t.Errorf("light = %+v", sp[2])
	}
	if _, err := parseSpells("Wish@10"); err == nil {
		t.Error("level 10 should error")
	}
}

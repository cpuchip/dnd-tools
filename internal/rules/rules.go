// Package rules carries the SRD 5.2 core math a character sheet needs:
// ability modifiers, proficiency, skills, classes, XP thresholds.
//
// This is deliberately a SUBSET — enough to hold an honest sheet and answer
// "what do I add to this d20?". Reference data (spells, monsters, items)
// comes from Open5e at runtime; rules text stays with the SRD.
//
// SRD 5.2.1 is © Wizards of the Coast, licensed CC-BY-4.0 (see README).
package rules

import (
	"fmt"
	"sort"
	"strings"
)

// Abilities in canonical order.
var Abilities = []string{"str", "dex", "con", "int", "wis", "cha"}

var abilityNames = map[string]string{
	"str": "Strength", "dex": "Dexterity", "con": "Constitution",
	"int": "Intelligence", "wis": "Wisdom", "cha": "Charisma",
}

// abilityAliases maps long forms to canonical keys.
var abilityAliases = map[string]string{
	"strength": "str", "dexterity": "dex", "constitution": "con",
	"intelligence": "int", "wisdom": "wis", "charisma": "cha",
	"str": "str", "dex": "dex", "con": "con", "int": "int", "wis": "wis", "cha": "cha",
}

// CanonicalAbility resolves "Strength"/"STR"/"str" to "str"; ok=false if unknown.
func CanonicalAbility(s string) (string, bool) {
	k, ok := abilityAliases[strings.ToLower(strings.TrimSpace(s))]
	return k, ok
}

// AbilityName returns the display name for a canonical key ("wis" -> "Wisdom").
func AbilityName(key string) string {
	if n, ok := abilityNames[key]; ok {
		return n
	}
	return strings.ToUpper(key)
}

// AbilityMod is the SRD modifier: floor((score-10)/2).
// score/2-5 computes the floor correctly for non-negative scores.
func AbilityMod(score int) int { return score/2 - 5 }

// ProficiencyBonus by character level (SRD: +2 at 1st, +1 every 4 levels).
func ProficiencyBonus(level int) int {
	if level < 1 {
		level = 1
	}
	if level > 20 {
		level = 20
	}
	return 2 + (level-1)/4
}

// FmtMod renders a modifier with its sign: +3, -1, +0.
func FmtMod(m int) string { return fmt.Sprintf("%+d", m) }

// Skills maps each SRD skill (canonical snake_case key) to its ability.
var Skills = map[string]string{
	"acrobatics":      "dex",
	"animal_handling": "wis",
	"arcana":          "int",
	"athletics":       "str",
	"deception":       "cha",
	"history":         "int",
	"insight":         "wis",
	"intimidation":    "cha",
	"investigation":   "int",
	"medicine":        "wis",
	"nature":          "int",
	"perception":      "wis",
	"performance":     "cha",
	"persuasion":      "cha",
	"religion":        "int",
	"sleight_of_hand": "dex",
	"stealth":         "dex",
	"survival":        "wis",
}

// SkillName renders a canonical skill key for display ("sleight_of_hand" ->
// "Sleight of Hand").
func SkillName(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if p == "of" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// CanonicalSkill resolves "Sleight of Hand"/"sleight-of-hand" to the canonical
// key; ok=false if it isn't an SRD skill.
func CanonicalSkill(s string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(s))
	k = strings.NewReplacer(" ", "_", "-", "_").Replace(k)
	_, ok := Skills[k]
	return k, ok
}

// SkillKeys returns all skill keys sorted.
func SkillKeys() []string {
	keys := make([]string, 0, len(Skills))
	for k := range Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Class holds the class facts character creation needs.
type Class struct {
	Name   string
	HitDie int      // d6/d8/d10/d12
	Saves  []string // proficient saving throws (canonical ability keys)
}

// Classes are the twelve SRD classes. Save proficiencies are identical in
// SRD 5.1 and 5.2.
var Classes = map[string]Class{
	"barbarian": {"Barbarian", 12, []string{"str", "con"}},
	"bard":      {"Bard", 8, []string{"dex", "cha"}},
	"cleric":    {"Cleric", 8, []string{"wis", "cha"}},
	"druid":     {"Druid", 8, []string{"int", "wis"}},
	"fighter":   {"Fighter", 10, []string{"str", "con"}},
	"monk":      {"Monk", 8, []string{"str", "dex"}},
	"paladin":   {"Paladin", 10, []string{"wis", "cha"}},
	"ranger":    {"Ranger", 10, []string{"str", "dex"}},
	"rogue":     {"Rogue", 8, []string{"dex", "int"}},
	"sorcerer":  {"Sorcerer", 6, []string{"con", "cha"}},
	"warlock":   {"Warlock", 8, []string{"wis", "cha"}},
	"wizard":    {"Wizard", 6, []string{"int", "wis"}},
}

// LookupClass resolves a class name case-insensitively. Unknown classes are
// allowed on a sheet (homebrew), they just get no derived saves and a d8.
func LookupClass(name string) (Class, bool) {
	c, ok := Classes[strings.ToLower(strings.TrimSpace(name))]
	return c, ok
}

// StandardArray is the SRD ability-score array.
var StandardArray = []int{15, 14, 13, 12, 10, 8}

// XPThresholds[level] = total XP needed to BE that level (index 1..20).
var XPThresholds = []int{0,
	0, 300, 900, 2700, 6500,
	14000, 23000, 34000, 48000, 64000,
	85000, 100000, 120000, 140000, 165000,
	195000, 225000, 265000, 305000, 355000,
}

// LevelForXP returns the level a total XP earns (1..20).
func LevelForXP(xp int) int {
	level := 1
	for l := 2; l <= 20; l++ {
		if xp >= XPThresholds[l] {
			level = l
		}
	}
	return level
}

// HPAtLevel1 = hit die max + CON mod (minimum 1).
func HPAtLevel1(hitDie, conMod int) int {
	hp := hitDie + conMod
	if hp < 1 {
		hp = 1
	}
	return hp
}

// HPPerLevel is the fixed (average) gain per level after 1st: die/2+1 + CON
// mod, minimum 1 per level. Deterministic on purpose — rolled HP can come
// later through the room's open /roll.
func HPPerLevel(hitDie, conMod int) int {
	hp := hitDie/2 + 1 + conMod
	if hp < 1 {
		hp = 1
	}
	return hp
}

// MaxHP for a class hit die, level, and CON mod, using fixed averages.
func MaxHP(hitDie, level, conMod int) int {
	if level < 1 {
		level = 1
	}
	return HPAtLevel1(hitDie, conMod) + (level-1)*HPPerLevel(hitDie, conMod)
}

// Check is a resolved d20 check: what to add and why.
type Check struct {
	Label      string // "Perception (WIS)" / "Strength save" / "Initiative (DEX)"
	Mod        int    // total modifier
	Breakdown  string // "WIS +3, proficiency +2"
	Proficient bool
}

// ResolveCheck computes the modifier for a named check against ability scores,
// proficient skills, and proficient saves. Accepted forms:
//
//	"perception", "sleight of hand"   — skill check
//	"str", "strength"                 — raw ability check
//	"dex save", "wisdom saving throw" — saving throw
//	"initiative"                      — DEX-based initiative
func ResolveCheck(check string, abilities map[string]int, profSkills, profSaves []string, level int) (Check, error) {
	q := strings.ToLower(strings.TrimSpace(check))
	prof := ProficiencyBonus(level)

	mod := func(ab string) int { return AbilityMod(abilities[ab]) }
	hasProf := func(list []string, key string) bool {
		for _, p := range list {
			if strings.EqualFold(p, key) {
				return true
			}
		}
		return false
	}

	// Saving throws: "<ability> save" / "<ability> saving throw".
	if ab, found := strings.CutSuffix(q, " saving throw"); found {
		q = ab + " save"
	}
	if ab, found := strings.CutSuffix(q, " save"); found {
		key, ok := CanonicalAbility(ab)
		if !ok {
			return Check{}, fmt.Errorf("unknown ability %q for a saving throw", ab)
		}
		c := Check{Label: AbilityName(key) + " save", Mod: mod(key),
			Breakdown: fmt.Sprintf("%s %s", strings.ToUpper(key), FmtMod(mod(key)))}
		if hasProf(profSaves, key) {
			c.Mod += prof
			c.Proficient = true
			c.Breakdown += fmt.Sprintf(", proficiency %s", FmtMod(prof))
		}
		return c, nil
	}

	if q == "initiative" || q == "init" {
		return Check{Label: "Initiative (DEX)", Mod: mod("dex"),
			Breakdown: "DEX " + FmtMod(mod("dex"))}, nil
	}

	if key, ok := CanonicalSkill(q); ok {
		ab := Skills[key]
		c := Check{Label: fmt.Sprintf("%s (%s)", SkillName(key), strings.ToUpper(ab)),
			Mod:       mod(ab),
			Breakdown: fmt.Sprintf("%s %s", strings.ToUpper(ab), FmtMod(mod(ab)))}
		if hasProf(profSkills, key) {
			c.Mod += prof
			c.Proficient = true
			c.Breakdown += fmt.Sprintf(", proficiency %s", FmtMod(prof))
		}
		return c, nil
	}

	if key, ok := CanonicalAbility(q); ok {
		return Check{Label: AbilityName(key) + " check", Mod: mod(key),
			Breakdown: fmt.Sprintf("%s %s", strings.ToUpper(key), FmtMod(mod(key)))}, nil
	}

	return Check{}, fmt.Errorf("unknown check %q — use a skill (e.g. perception), an ability (str), a save (dex save), or initiative", check)
}

// RollSuggestion renders the chat command that performs this check in the
// open. dnd-tools NEVER rolls dice — the room's server does, so every roll
// is public and un-fudgeable. The [comment] is chattermax's flavor syntax.
func (c Check) RollSuggestion(characterName string) string {
	return fmt.Sprintf("/roll 1d20%s [%s — %s]", FmtMod(c.Mod), characterName, c.Label)
}

// Package sheet renders a character as chat-friendly markdown.
package sheet

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cpuchip/dnd-tools/internal/rules"
	"github.com/cpuchip/dnd-tools/internal/store"
)

// AttackResult is a resolved weapon attack: what to roll to hit and, on a
// hit, what to roll for damage. Both rolls are SUGGESTIONS for the room's
// server — dnd-tools never rolls.
type AttackResult struct {
	Weapon     string `json:"weapon"`
	ToHit      int    `json:"to_hit"`
	DamageExpr string `json:"damage_expr"`
	DamageType string `json:"damage_type,omitempty"`
	Breakdown  string `json:"breakdown"`
	ToHitRoll  string `json:"to_hit_roll"`
	DamageRoll string `json:"damage_roll"`
}

// ResolveAttack computes an attack's numbers for a character. target is
// flavor for the roll comments ("" = generic).
func ResolveAttack(c store.Character, a store.Attack, target string) AttackResult {
	ability := a.Ability
	if ability == "" {
		ability = "str"
	}
	toHit, dmgMod := rules.AttackMods(c.Abilities[ability], c.Level, a.MagicBonus, a.Proficient)
	breakdown := fmt.Sprintf("%s %s", strings.ToUpper(ability), rules.FmtMod(rules.AbilityMod(c.Abilities[ability])))
	if a.Proficient {
		breakdown += fmt.Sprintf(", proficiency %s", rules.FmtMod(rules.ProficiencyBonus(c.Level)))
	}
	if a.MagicBonus != 0 {
		breakdown += fmt.Sprintf(", magic %s", rules.FmtMod(a.MagicBonus))
	}
	vs := ""
	if target != "" {
		vs = " vs " + target
	}
	dmgExpr := rules.DamageExpr(a.Damage, dmgMod)
	dmgLabel := a.Name + " damage"
	if a.DamageType != "" {
		dmgLabel += " (" + a.DamageType + ")"
	}
	return AttackResult{
		Weapon:     a.Name,
		ToHit:      toHit,
		DamageExpr: dmgExpr,
		DamageType: a.DamageType,
		Breakdown:  breakdown,
		ToHitRoll:  fmt.Sprintf("/roll 1d20%s [%s — %s%s]", rules.FmtMod(toHit), c.Name, a.Name, vs),
		DamageRoll: fmt.Sprintf("/roll %s [%s — %s]", dmgExpr, c.Name, dmgLabel),
	}
}

// FindAttack picks a character's attack by name (case-insensitive,
// prefix-tolerant). Empty name returns the first attack.
func FindAttack(c store.Character, name string) (store.Attack, error) {
	if len(c.Attacks) == 0 {
		return store.Attack{}, fmt.Errorf("%s has no attacks on the sheet — add one with dnd_char_update attacks_add", c.Name)
	}
	if strings.TrimSpace(name) == "" {
		return c.Attacks[0], nil
	}
	n := strings.ToLower(strings.TrimSpace(name))
	for _, a := range c.Attacks {
		if strings.ToLower(a.Name) == n {
			return a, nil
		}
	}
	for _, a := range c.Attacks {
		if strings.HasPrefix(strings.ToLower(a.Name), n) {
			return a, nil
		}
	}
	var names []string
	for _, a := range c.Attacks {
		names = append(names, a.Name)
	}
	return store.Attack{}, fmt.Errorf("%s has no attack named %q (has: %s)", c.Name, name, strings.Join(names, ", "))
}

// FindSpell picks a character's known spell by name (case-insensitive,
// prefix-tolerant).
func FindSpell(c store.Character, name string) (store.Spell, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, sp := range c.Spells {
		if strings.ToLower(sp.Name) == n {
			return sp, nil
		}
	}
	for _, sp := range c.Spells {
		if strings.HasPrefix(strings.ToLower(sp.Name), n) {
			return sp, nil
		}
	}
	var names []string
	for _, sp := range c.Spells {
		names = append(names, sp.Name)
	}
	if len(names) == 0 {
		return store.Spell{}, fmt.Errorf("%s knows no spells — add them with dnd_char_update spells_add", c.Name)
	}
	return store.Spell{}, fmt.Errorf("%s doesn't know %q (knows: %s)", c.Name, name, strings.Join(names, ", "))
}

// titleCase upper-cases the first letter only — class names are plain ASCII
// ("fighter" → "Fighter"); known classes arrive pre-cased from rules.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Render produces the full markdown sheet.
func Render(c store.Character) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## %s\n", c.Name)
	line := []string{}
	if c.Species != "" {
		line = append(line, c.Species)
	}
	if c.Class != "" {
		line = append(line, fmt.Sprintf("%s %d", titleCase(c.Class), c.Level))
	} else {
		line = append(line, fmt.Sprintf("Level %d", c.Level))
	}
	if c.Background != "" {
		line = append(line, c.Background)
	}
	if c.Alignment != "" {
		line = append(line, c.Alignment)
	}
	fmt.Fprintf(&b, "*%s*", strings.Join(line, " · "))
	if c.Player != "" {
		fmt.Fprintf(&b, " — played by %s", c.Player)
	}
	if c.Kind == "npc" {
		b.WriteString(" *(NPC)*")
	}
	fmt.Fprintf(&b, " — campaign: %s\n\n", c.Campaign)

	fmt.Fprintf(&b, "**HP** %d/%d · **AC** %d · **Speed** %d ft · **Prof** %s · **XP** %d\n\n",
		c.HPCurrent, c.HPMax, c.AC, c.Speed, rules.FmtMod(rules.ProficiencyBonus(c.Level)), c.XP)

	// Ability block in canonical order.
	var abl []string
	for _, k := range rules.Abilities {
		score := c.Abilities[k]
		abl = append(abl, fmt.Sprintf("%s %d (%s)", strings.ToUpper(k), score, rules.FmtMod(rules.AbilityMod(score))))
	}
	b.WriteString(strings.Join(abl, " · ") + "\n\n")

	if len(c.Saves) > 0 {
		var ss []string
		for _, k := range c.Saves {
			ss = append(ss, rules.AbilityName(k))
		}
		fmt.Fprintf(&b, "**Saves:** %s\n", strings.Join(ss, ", "))
	}
	if len(c.Skills) > 0 {
		prof := rules.ProficiencyBonus(c.Level)
		sk := append([]string(nil), c.Skills...)
		sort.Strings(sk)
		var ss []string
		for _, k := range sk {
			ab, ok := rules.Skills[k]
			if !ok {
				ss = append(ss, rules.SkillName(k))
				continue
			}
			ss = append(ss, fmt.Sprintf("%s %s", rules.SkillName(k), rules.FmtMod(rules.AbilityMod(c.Abilities[ab])+prof)))
		}
		fmt.Fprintf(&b, "**Skills:** %s\n", strings.Join(ss, ", "))
	}

	if len(c.Conditions) > 0 {
		fmt.Fprintf(&b, "**Conditions:** %s\n", strings.Join(c.Conditions, ", "))
	}

	if len(c.Attacks) > 0 {
		b.WriteString("\n**Attacks:**\n")
		for _, a := range c.Attacks {
			r := ResolveAttack(c, a, "")
			line := fmt.Sprintf("- %s: %s to hit, %s", a.Name, rules.FmtMod(r.ToHit), r.DamageExpr)
			if a.DamageType != "" {
				line += " " + a.DamageType
			}
			if a.Range != "" {
				line += fmt.Sprintf(" (range %s)", a.Range)
			}
			b.WriteString(line + "\n")
		}
	}

	if len(c.Spells) > 0 {
		var byLevel []string
		for _, sp := range c.Spells {
			tag := sp.Name
			if sp.Level > 0 {
				tag += fmt.Sprintf(" (L%d)", sp.Level)
			} else {
				tag += " (cantrip)"
			}
			if sp.Prepared {
				tag += "*"
			}
			byLevel = append(byLevel, tag)
		}
		fmt.Fprintf(&b, "**Spells:** %s\n", strings.Join(byLevel, ", "))
	}

	if len(c.SpellSlots) > 0 {
		keys := make([]string, 0, len(c.SpellSlots))
		for k := range c.SpellSlots {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var ss []string
		for _, k := range keys {
			ss = append(ss, fmt.Sprintf("L%s×%d", k, c.SpellSlots[k]))
		}
		fmt.Fprintf(&b, "**Spell slots:** %s\n", strings.Join(ss, " "))
	}

	if len(c.Features) > 0 {
		fmt.Fprintf(&b, "**Features:** %s\n", strings.Join(c.Features, "; "))
	}

	if len(c.Inventory) > 0 {
		b.WriteString("\n**Inventory:**\n")
		for _, it := range c.Inventory {
			b.WriteString("- " + it.Name)
			if it.Qty > 1 {
				fmt.Fprintf(&b, " ×%d", it.Qty)
			}
			if it.Notes != "" {
				fmt.Fprintf(&b, " (%s)", it.Notes)
			}
			b.WriteString("\n")
		}
	}

	if c.Notes != "" {
		fmt.Fprintf(&b, "\n**Notes:** %s\n", c.Notes)
	}
	return b.String()
}

// Line renders a one-line roster summary.
func Line(c store.Character) string {
	cls := c.Class
	if cls != "" {
		cls = fmt.Sprintf("%s %d", titleCase(cls), c.Level)
	} else {
		cls = fmt.Sprintf("L%d", c.Level)
	}
	tag := ""
	if c.Kind == "npc" {
		tag = " [NPC]"
	}
	player := ""
	if c.Player != "" {
		player = " — " + c.Player
	}
	return fmt.Sprintf("%s%s — %s, HP %d/%d, AC %d%s", c.Name, tag, cls, c.HPCurrent, c.HPMax, c.AC, player)
}

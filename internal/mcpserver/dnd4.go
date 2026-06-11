// DH-4 handlers: weapon attacks, spellcasting, room binding, and lore.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/cpuchip/dnd-tools/internal/rules"
	"github.com/cpuchip/dnd-tools/internal/sheet"
	"github.com/cpuchip/dnd-tools/internal/store"
)

func boolArg(req mcp.CallToolRequest, name string) bool {
	v, _ := req.GetArguments()[name].(bool)
	return v
}

// --- attacks -----------------------------------------------------------------

var diceRe = regexp.MustCompile(`^\d*d\d+([+-]\d+)?$`)

// parseAttack reads the compact attack syntax:
//
//	"Longsword: 1d8 slashing"
//	"Dagger: 1d4 piercing dex range 20/60"
//	"Flame Tongue: 2d6 fire +1"
//	"Improvised: 1d4 bludgeoning noprof"
//
// or a JSON Attack object. Proficient defaults to true; ability to str.
func parseAttack(spec string) (store.Attack, error) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "{") {
		var a store.Attack
		if err := json.Unmarshal([]byte(spec), &a); err != nil {
			return a, fmt.Errorf("attacks_add JSON: %w", err)
		}
		if a.Name == "" || a.Damage == "" {
			return a, fmt.Errorf("attacks_add JSON needs at least name and damage")
		}
		return a, nil
	}
	name, rest, found := strings.Cut(spec, ":")
	if !found {
		return store.Attack{}, fmt.Errorf(`attacks_add: use "Name: dice [type] [ability] [+N] [noprof] [range X]", e.g. "Longsword: 1d8 slashing"`)
	}
	a := store.Attack{Name: strings.TrimSpace(name), Proficient: true}
	tokens := strings.Fields(rest)
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		lt := strings.ToLower(t)
		switch {
		case diceRe.MatchString(lt) && a.Damage == "":
			a.Damage = lt
		case lt == "finesse":
			a.Ability = "dex"
		case lt == "noprof":
			a.Proficient = false
		case lt == "range" && i+1 < len(tokens):
			i++
			a.Range = tokens[i]
		case (strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-")) && len(t) > 1:
			if n, err := strconv.Atoi(t); err == nil {
				a.MagicBonus = n
				continue
			}
			a.Notes = strings.TrimSpace(a.Notes + " " + t)
		default:
			if key, ok := rules.CanonicalAbility(lt); ok {
				a.Ability = key
			} else if a.DamageType == "" && a.Damage != "" {
				a.DamageType = lt
			} else {
				a.Notes = strings.TrimSpace(a.Notes + " " + t)
			}
		}
	}
	if a.Damage == "" {
		return a, fmt.Errorf("attacks_add: no damage dice found (e.g. 1d8) in %q", spec)
	}
	return a, nil
}

func (s *Server) handleCharAttack(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, err := s.findChar(req)
	if err != nil {
		return errText(err), nil
	}
	a, err := sheet.FindAttack(c, strArg(req, "weapon"))
	if err != nil {
		return errText(err), nil
	}
	r := sheet.ResolveAttack(c, a, strArg(req, "target"))
	var b strings.Builder
	fmt.Fprintf(&b, "%s attacks with %s: %s to hit (%s).\n", c.Name, a.Name, rules.FmtMod(r.ToHit), r.Breakdown)
	fmt.Fprintf(&b, "Roll to hit: `%s`\n", r.ToHitRoll)
	fmt.Fprintf(&b, "If the DM rules it a hit, roll damage: `%s`", r.DamageRoll)
	return mcp.NewToolResultText(b.String()), nil
}

// --- spells --------------------------------------------------------------------

// parseSpells reads "Name@level" CSV entries; missing level = cantrip.
func parseSpells(spec string) ([]store.Spell, error) {
	var out []store.Spell
	for _, raw := range splitCSV(spec) {
		name, lvlStr, found := strings.Cut(raw, "@")
		sp := store.Spell{Name: strings.TrimSpace(name), Prepared: true}
		if found {
			lvlStr = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(lvlStr)), "l")
			n, err := strconv.Atoi(lvlStr)
			if err != nil || n < 0 || n > 9 {
				return nil, fmt.Errorf(`spells_add: %q — use "Name@level" with level 0..9`, raw)
			}
			sp.Level = n
		}
		if sp.Name == "" {
			return nil, fmt.Errorf("spells_add: empty spell name in %q", raw)
		}
		out = append(out, sp)
	}
	return out, nil
}

func (s *Server) handleCharCast(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, err := s.findChar(req)
	if err != nil {
		return errText(err), nil
	}
	sp, err := sheet.FindSpell(c, strArg(req, "spell"))
	if err != nil {
		return errText(err), nil
	}

	var b strings.Builder
	if sp.Level == 0 {
		fmt.Fprintf(&b, "%s casts **%s** (cantrip — no slot spent).", c.Name, sp.Name)
	} else {
		slot := sp.Level
		if n, ok := numArg(req, "slot_level"); ok {
			if n < sp.Level {
				return mcp.NewToolResultError(fmt.Sprintf("%s is a level-%d spell — it needs a slot of level %d or higher", sp.Name, sp.Level, sp.Level)), nil
			}
			if n > 9 {
				return mcp.NewToolResultError("slot_level must be 1..9"), nil
			}
			slot = n
		}
		key := strconv.Itoa(slot)
		if c.SpellSlots[key] <= 0 {
			return mcp.NewToolResultError(fmt.Sprintf("%s has no level-%d spell slots left", c.Name, slot)), nil
		}
		c.SpellSlots[key]--
		if c, err = s.st.SaveCharacter(c); err != nil {
			return errText(err), nil
		}
		fmt.Fprintf(&b, "%s casts **%s** using a level-%d slot. Remaining L%d slots: %d.",
			c.Name, sp.Name, slot, slot, c.SpellSlots[key])
	}

	// Best-effort: if the spell is keyed to Open5e and the entry carries a
	// damage roll, suggest the open /roll. Offline/uncached just skips.
	if sp.Key != "" {
		if entry, err := s.ref.Get(ctx, "spell", sp.Key); err == nil {
			if dmg, ok := entry["damage_roll"].(string); ok && dmg != "" {
				fmt.Fprintf(&b, "\nDamage: `/roll %s [%s — %s]`", dmg, c.Name, sp.Name)
			}
		}
	}
	b.WriteString("\nNever invent dice results — post the rolls in the open.")
	return mcp.NewToolResultText(b.String()), nil
}

// --- binding + lore -------------------------------------------------------------

func (s *Server) handleCampaignBind(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	roomID := strArg(req, "room_id")
	if roomID == "" {
		return mcp.NewToolResultError("room_id is required"), nil
	}
	c, err := s.st.ResolveCampaign(strArg(req, "campaign"))
	if err != nil {
		return errText(err), nil
	}
	if err := s.st.BindRoom(c.ID, roomID); err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Room %s now plays **%s** — /attack, /check and friends resolve against it.", roomID, c.Name)), nil
}

func (s *Server) loreCampaign(req mcp.CallToolRequest) (store.Campaign, error) {
	return s.st.ResolveCampaign(strArg(req, "campaign"))
}

func (s *Server) handleLoreSet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strArg(req, "name")
	body := strArg(req, "body")
	if name == "" || body == "" {
		return mcp.NewToolResultError("name and body are required"), nil
	}
	kind, ok := store.CanonicalLoreKind(strArg(req, "kind"))
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("unknown kind %q — use %s", strArg(req, "kind"), strings.Join(store.LoreKinds, " | "))), nil
	}
	c, err := s.loreCampaign(req)
	if err != nil {
		return errText(err), nil
	}
	e, err := s.st.SetLore(c.ID, kind, name, body, boolArg(req, "dm_secret"))
	if err != nil {
		return errText(err), nil
	}
	secret := ""
	if e.DMSecret {
		secret = " (DM secret)"
	}
	return mcp.NewToolResultText(fmt.Sprintf("Lore saved: **%s** [%s]%s in %s.", e.Name, e.Kind, secret, c.Name)), nil
}

func (s *Server) handleLoreGet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strArg(req, "name")
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	c, err := s.loreCampaign(req)
	if err != nil {
		return errText(err), nil
	}
	e, err := s.st.LoreByName(c.ID, name)
	if err != nil {
		return errText(fmt.Errorf("no lore named %q in %s", name, c.Name)), nil
	}
	secret := ""
	if e.DMSecret {
		secret = " · 🔒 DM secret"
	}
	return mcp.NewToolResultText(fmt.Sprintf("## %s [%s]%s\n%s", e.Name, e.Kind, secret, e.Body)), nil
}

func renderLoreList(c store.Campaign, entries []store.LoreEntry, empty string) *mcp.CallToolResult {
	if len(entries) == 0 {
		return mcp.NewToolResultText(empty)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Lore in **%s**:\n", c.Name)
	for _, e := range entries {
		line := fmt.Sprintf("- %s [%s]", e.Name, e.Kind)
		if e.DMSecret {
			line += " 🔒"
		}
		if len(e.Body) > 0 {
			summary := e.Body
			if len(summary) > 100 {
				summary = summary[:100] + "…"
			}
			line += " — " + strings.ReplaceAll(summary, "\n", " ")
		}
		b.WriteString(line + "\n")
	}
	return mcp.NewToolResultText(b.String())
}

func (s *Server) handleLoreList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, err := s.loreCampaign(req)
	if err != nil {
		return errText(err), nil
	}
	kind := ""
	if k := strArg(req, "kind"); k != "" {
		if kind, _ = store.CanonicalLoreKind(k); kind == "" {
			return mcp.NewToolResultError(fmt.Sprintf("unknown kind %q", k)), nil
		}
	}
	entries, err := s.st.LoreList(c.ID, kind, boolArg(req, "include_secrets"))
	if err != nil {
		return errText(err), nil
	}
	return renderLoreList(c, entries, fmt.Sprintf("No lore in **%s** yet — dnd_lore_set writes the first entry.", c.Name)), nil
}

func (s *Server) handleLoreSearch(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := strArg(req, "query")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	c, err := s.loreCampaign(req)
	if err != nil {
		return errText(err), nil
	}
	entries, err := s.st.LoreSearch(c.ID, query, boolArg(req, "include_secrets"))
	if err != nil {
		return errText(err), nil
	}
	return renderLoreList(c, entries, fmt.Sprintf("No lore matches %q in **%s**.", query, c.Name)), nil
}

func (s *Server) handleLoreDelete(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strArg(req, "name")
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	c, err := s.loreCampaign(req)
	if err != nil {
		return errText(err), nil
	}
	if err := s.st.DeleteLore(c.ID, name); err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Lore **%s** deleted from %s.", name, c.Name)), nil
}

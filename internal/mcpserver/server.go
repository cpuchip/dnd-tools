// Package mcpserver exposes dnd-tools over MCP (stdio). All tools are
// dnd_-prefixed so a single allow pattern (dnd_*) grants the family.
//
// Dice honesty: nothing here rolls dice. dnd_char_check returns the modifier
// and the /roll command for the ROOM to roll in the open — the same fairness
// story as chattermax's server-side dice.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/cpuchip/dnd-tools/internal/open5e"
	"github.com/cpuchip/dnd-tools/internal/rules"
	"github.com/cpuchip/dnd-tools/internal/sheet"
	"github.com/cpuchip/dnd-tools/internal/store"
)

// Server wires the store and the Open5e client to MCP tools.
type Server struct {
	mcp *server.MCPServer
	st  *store.Store
	ref *open5e.Client
}

// New builds the MCP server with every tool registered.
func New(st *store.Store, ref *open5e.Client, version string) *Server {
	m := server.NewMCPServer("dnd-tools", version, server.WithToolCapabilities(true))
	s := &Server{mcp: m, st: st, ref: ref}
	s.register()
	return s
}

// Serve runs stdio MCP (blocks).
func (s *Server) Serve() error { return server.ServeStdio(s.mcp) }

// HTTPHandler returns the MCP server as a streamable-HTTP handler (mounted
// at /mcp) so a remote bridge can dial it like exa-search.
func (s *Server) HTTPHandler() http.Handler {
	return server.NewStreamableHTTPServer(s.mcp, server.WithEndpointPath("/mcp"))
}

func (s *Server) register() {
	// --- campaigns -----------------------------------------------------
	s.mcp.AddTool(
		mcp.NewTool("dnd_campaign_create",
			mcp.WithDescription("Create a campaign. Campaigns hold characters and a session log. Status starts as 'prep' (the prep-room phase)."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Campaign name, unique")),
			mcp.WithString("description", mcp.Description("One-paragraph premise")),
			mcp.WithString("setting", mcp.Description("World/setting notes")),
		), s.handleCampaignCreate)

	s.mcp.AddTool(
		mcp.NewTool("dnd_campaign_get",
			mcp.WithDescription("Get a campaign overview: premise, roster, and the most recent session-log entries. Omit name when only one campaign exists."),
			mcp.WithString("name", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleCampaignGet)

	s.mcp.AddTool(
		mcp.NewTool("dnd_campaign_log",
			mcp.WithDescription("Append a session entry to the campaign log (the /archive record). Write the summary like a recap a player would enjoy reading before the next session."),
			mcp.WithString("summary", mcp.Required(), mcp.Description("What happened this session")),
			mcp.WithString("title", mcp.Description("Short session title")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
			mcp.WithString("status", mcp.Description("Optionally move the campaign to this status: prep | active | archived")),
		), s.handleCampaignLog)

	// --- characters ----------------------------------------------------
	s.mcp.AddTool(
		mcp.NewTool("dnd_char_create",
			mcp.WithDescription("Create a character sheet (SRD 5.2 ruleset). HP and saving-throw proficiencies derive from the class; skills you pass become proficiencies. Returns the rendered sheet."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Character name, unique within the campaign")),
			mcp.WithString("class", mcp.Required(), mcp.Description("One of the 12 SRD classes (barbarian, bard, cleric, druid, fighter, monk, paladin, ranger, rogue, sorcerer, warlock, wizard); other strings allowed for homebrew")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
			mcp.WithString("player", mcp.Description("Who runs this character (a human's name or a persona/cast name)")),
			mcp.WithString("kind", mcp.Description("pc (default) or npc")),
			mcp.WithString("species", mcp.Description("Species/race, e.g. Dwarf")),
			mcp.WithString("background", mcp.Description("Background, e.g. Soldier")),
			mcp.WithString("alignment", mcp.Description("e.g. Chaotic Good")),
			mcp.WithNumber("level", mcp.Description("Starting level (default 1)")),
			mcp.WithString("abilities", mcp.Description(`Ability scores: "standard" (15,14,13,12,10,8 in str,dex,con,int,wis,cha order), a CSV like "14,15,13,10,12,8" (same order), or JSON like {"str":14,"dex":15,...}. Default: standard array`)),
			mcp.WithString("skills", mcp.Description("Comma-separated proficient skills, e.g. \"perception, stealth\"")),
			mcp.WithNumber("ac", mcp.Description("Armor class (default 10 + DEX mod)")),
			mcp.WithNumber("speed", mcp.Description("Speed in feet (default 30)")),
			mcp.WithString("notes", mcp.Description("Free-form notes (personality, bonds, gear story)")),
		), s.handleCharCreate)

	s.mcp.AddTool(
		mcp.NewTool("dnd_char_get",
			mcp.WithDescription("Get a character's full sheet as markdown."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Character name")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if the character name is unique)")),
		), s.handleCharGet)

	s.mcp.AddTool(
		mcp.NewTool("dnd_char_list",
			mcp.WithDescription("List a campaign's characters (one line each: class, level, HP, AC, player)."),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleCharList)

	s.mcp.AddTool(
		mcp.NewTool("dnd_char_update",
			mcp.WithDescription("Update a character. Pass only what changes. hp_delta applies damage (negative) or healing (positive), clamped to [0, hp_max]."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Character name")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if the character name is unique)")),
			mcp.WithNumber("hp_delta", mcp.Description("Damage (negative) or healing (positive)")),
			mcp.WithNumber("hp_current", mcp.Description("Set current HP directly")),
			mcp.WithNumber("hp_max", mcp.Description("Set max HP")),
			mcp.WithNumber("ac", mcp.Description("Set armor class")),
			mcp.WithNumber("speed", mcp.Description("Set speed (feet)")),
			mcp.WithNumber("xp_add", mcp.Description("Add XP (levels do NOT auto-apply; use dnd_char_levelup)")),
			mcp.WithString("player", mcp.Description("Reassign who runs the character")),
			mcp.WithString("notes_append", mcp.Description("Append a line to notes")),
			mcp.WithString("inventory_add", mcp.Description(`Add an item: "Rope (50 ft)", "Torch x3", or "Potion of Healing x2 | restores 2d4+2"`)),
			mcp.WithString("inventory_remove", mcp.Description("Remove an item by name (case-insensitive; decrements qty, removes at 0)")),
			mcp.WithString("spell_slots", mcp.Description(`Set spell slots: JSON {"1":4,"2":2} or CSV "1:4,2:2"`)),
			mcp.WithString("skills_add", mcp.Description("Comma-separated skills to add as proficiencies")),
			mcp.WithString("features_add", mcp.Description("Comma-separated features/traits to add")),
			mcp.WithString("attacks_add", mcp.Description(`Add a weapon/attack: "Name: dice [type] [ability] [+N magic] [noprof] [range X]" — e.g. "Longsword: 1d8 slashing", "Dagger: 1d4 piercing dex range 20/60", "Flame Tongue: 2d6 fire +1". Or a JSON Attack object.`)),
			mcp.WithString("attacks_remove", mcp.Description("Remove an attack by name")),
			mcp.WithString("spells_add", mcp.Description(`Comma-separated spells as "Name@level" (level 0/omitted = cantrip): "Fire Bolt@0, Fireball@3"`)),
			mcp.WithString("spells_remove", mcp.Description("Remove a spell by name")),
			mcp.WithString("conditions_add", mcp.Description("Comma-separated conditions to apply, e.g. \"poisoned, prone\"")),
			mcp.WithString("conditions_remove", mcp.Description("Comma-separated conditions to clear")),
		), s.handleCharUpdate)

	s.mcp.AddTool(
		mcp.NewTool("dnd_char_levelup",
			mcp.WithDescription("Level a character up by one. HP rises by the class average (die/2+1 + CON mod); proficiency recomputes. Returns what changed."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Character name")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if the character name is unique)")),
		), s.handleCharLevelup)

	s.mcp.AddTool(
		mcp.NewTool("dnd_char_attack",
			mcp.WithDescription("Resolve a weapon attack: the to-hit roll to post, and the damage roll to post IF the DM rules it a hit. NEVER invent the results — post the suggested /roll commands and let the room roll in the open."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Attacker's character name")),
			mcp.WithString("weapon", mcp.Description("Which attack on the sheet (default: the first one)")),
			mcp.WithString("target", mcp.Description("Flavor target for the roll comment, e.g. \"the goblin\"")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if the character name is unique)")),
		), s.handleCharAttack)

	s.mcp.AddTool(
		mcp.NewTool("dnd_char_cast",
			mcp.WithDescription("Cast a spell from the character's sheet: verifies it's known, spends the spell slot (cantrips are free), and reports remaining slots. If the spell has a damage roll, the suggested /roll is included — post it, never invent results."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Caster's character name")),
			mcp.WithString("spell", mcp.Required(), mcp.Description("Spell name from the sheet")),
			mcp.WithNumber("slot_level", mcp.Description("Upcast: spend a higher-level slot (default: the spell's level)")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if the character name is unique)")),
		), s.handleCharCast)

	s.mcp.AddTool(
		mcp.NewTool("dnd_char_check",
			mcp.WithDescription("Resolve a d20 check for a character: the total modifier, its breakdown, and the exact /roll command to post so the ROOM rolls it in the open. NEVER invent a die result — post the suggested /roll instead."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Character name")),
			mcp.WithString("check", mcp.Required(), mcp.Description(`A skill ("perception"), ability ("str"), save ("dex save"), or "initiative"`)),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if the character name is unique)")),
		), s.handleCharCheck)

	// --- world building ---------------------------------------------------
	s.mcp.AddTool(
		mcp.NewTool("dnd_campaign_bind",
			mcp.WithDescription("Bind a campaign to a chat room (one campaign per room). Room-side commands like /attack and /check use this to know which campaign the room plays."),
			mcp.WithString("room_id", mcp.Required(), mcp.Description("The chat room/channel id")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleCampaignBind)

	s.mcp.AddTool(
		mcp.NewTool("dnd_lore_set",
			mcp.WithDescription("Create or update a piece of campaign lore: a location, NPC, faction, plot thread, item, or event. The world's durable memory — write what the table establishes, update as it evolves."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Entry name, unique within the campaign")),
			mcp.WithString("body", mcp.Required(), mcp.Description("The lore text")),
			mcp.WithString("kind", mcp.Description("location | npc | faction | plot | item | event | other (default other)")),
			mcp.WithBoolean("dm_secret", mcp.Description("DM-only entry — hidden from players' surfaces (default false)")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleLoreSet)

	s.mcp.AddTool(
		mcp.NewTool("dnd_lore_get",
			mcp.WithDescription("Fetch one lore entry by name."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Entry name")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleLoreGet)

	s.mcp.AddTool(
		mcp.NewTool("dnd_lore_list",
			mcp.WithDescription("List the campaign's lore entries (names + kinds), optionally by kind."),
			mcp.WithString("kind", mcp.Description("Filter: location | npc | faction | plot | item | event | other")),
			mcp.WithBoolean("include_secrets", mcp.Description("Include DM-only entries (DM use; default false)")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleLoreList)

	s.mcp.AddTool(
		mcp.NewTool("dnd_lore_search",
			mcp.WithDescription("Search the campaign's lore by name/body text."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search text")),
			mcp.WithBoolean("include_secrets", mcp.Description("Include DM-only entries (DM use; default false)")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleLoreSearch)

	s.mcp.AddTool(
		mcp.NewTool("dnd_lore_delete",
			mcp.WithDescription("Delete a lore entry by name."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Entry name")),
			mcp.WithString("campaign", mcp.Description("Campaign name (optional if unambiguous)")),
		), s.handleLoreDelete)

	// --- reference (Open5e) ---------------------------------------------
	s.mcp.AddTool(
		mcp.NewTool("dnd_ref_search",
			mcp.WithDescription("Search SRD reference data by name via Open5e (cached locally): creatures, spells, items, conditions. Returns keys for dnd_ref_get."),
			mcp.WithString("type", mcp.Required(), mcp.Description("One of: "+open5e.TypeNames())),
			mcp.WithString("query", mcp.Required(), mcp.Description("Name fragment, e.g. \"goblin\" or \"fireball\"")),
			mcp.WithString("ruleset", mcp.Description("srd-2024 (SRD 5.2, default) or srd-2014 (SRD 5.1)")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 10, cap 20)")),
		), s.handleRefSearch)

	s.mcp.AddTool(
		mcp.NewTool("dnd_ref_get",
			mcp.WithDescription("Fetch one SRD reference entry by key (from dnd_ref_search), formatted for the table."),
			mcp.WithString("type", mcp.Required(), mcp.Description("One of: "+open5e.TypeNames())),
			mcp.WithString("key", mcp.Required(), mcp.Description("Entry key, e.g. srd-2024_goblin-warrior")),
		), s.handleRefGet)
}

// --- argument helpers ----------------------------------------------------

func strArg(req mcp.CallToolRequest, name string) string {
	if v, ok := req.GetArguments()[name].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func numArg(req mcp.CallToolRequest, name string) (int, bool) {
	if v, ok := req.GetArguments()[name].(float64); ok {
		return int(v), true
	}
	return 0, false
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func errText(err error) *mcp.CallToolResult { return mcp.NewToolResultError(err.Error()) }

// findChar resolves name+campaign args to a character.
func (s *Server) findChar(req mcp.CallToolRequest) (store.Character, error) {
	name := strArg(req, "name")
	if name == "" {
		return store.Character{}, fmt.Errorf("name is required")
	}
	var campaignID int64
	if cn := strArg(req, "campaign"); cn != "" {
		c, err := s.st.ResolveCampaign(cn)
		if err != nil {
			return store.Character{}, err
		}
		campaignID = c.ID
	}
	return s.st.FindCharacter(name, campaignID)
}

// --- campaign handlers -----------------------------------------------------

func (s *Server) handleCampaignCreate(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strArg(req, "name")
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	c, err := s.st.CreateCampaign(name, strArg(req, "description"), strArg(req, "setting"))
	if err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Campaign **%s** created (status: %s). Add characters with dnd_char_create.", c.Name, c.Status)), nil
}

func (s *Server) handleCampaignGet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, err := s.st.ResolveCampaign(strArg(req, "name"))
	if err != nil {
		return errText(err), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Campaign: %s (%s)\n", c.Name, c.Status)
	if c.Description != "" {
		fmt.Fprintf(&b, "%s\n", c.Description)
	}
	if c.Setting != "" {
		fmt.Fprintf(&b, "\n**Setting:** %s\n", c.Setting)
	}
	chars, err := s.st.CharactersForCampaign(c.ID)
	if err != nil {
		return errText(err), nil
	}
	if len(chars) > 0 {
		b.WriteString("\n**Roster:**\n")
		for _, ch := range chars {
			b.WriteString("- " + sheet.Line(ch) + "\n")
		}
	} else {
		b.WriteString("\n*No characters yet.*\n")
	}
	log, err := s.st.RecentLog(c.ID, 3)
	if err != nil {
		return errText(err), nil
	}
	if len(log) > 0 {
		b.WriteString("\n**Recent sessions:**\n")
		for _, e := range log {
			title := e.Title
			if title == "" {
				title = "(untitled)"
			}
			fmt.Fprintf(&b, "- Session %d — %s: %s\n", e.SessionNo, title, e.Summary)
		}
	}
	return mcp.NewToolResultText(b.String()), nil
}

func (s *Server) handleCampaignLog(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	summary := strArg(req, "summary")
	if summary == "" {
		return mcp.NewToolResultError("summary is required"), nil
	}
	c, err := s.st.ResolveCampaign(strArg(req, "campaign"))
	if err != nil {
		return errText(err), nil
	}
	e, err := s.st.AppendLog(c.ID, strArg(req, "title"), summary)
	if err != nil {
		return errText(err), nil
	}
	msg := fmt.Sprintf("Logged session %d of **%s**.", e.SessionNo, c.Name)
	if status := strArg(req, "status"); status != "" {
		switch status {
		case "prep", "active", "archived":
			if err := s.st.SetCampaignStatus(c.ID, status); err != nil {
				return errText(err), nil
			}
			msg += fmt.Sprintf(" Campaign status → %s.", status)
		default:
			msg += fmt.Sprintf(" (status %q ignored — use prep | active | archived)", status)
		}
	}
	return mcp.NewToolResultText(msg), nil
}

// --- character handlers ------------------------------------------------------

// parseAbilities accepts "standard", a 6-number CSV (str,dex,con,int,wis,cha),
// or a JSON object with ability keys.
func parseAbilities(spec string) (map[string]int, error) {
	spec = strings.TrimSpace(spec)
	out := map[string]int{}
	switch {
	case spec == "" || strings.EqualFold(spec, "standard"):
		for i, k := range rules.Abilities {
			out[k] = rules.StandardArray[i]
		}
		return out, nil
	case strings.HasPrefix(spec, "{"):
		var raw map[string]any
		if err := json.Unmarshal([]byte(spec), &raw); err != nil {
			return nil, fmt.Errorf("abilities JSON: %w", err)
		}
		for k, v := range raw {
			key, ok := rules.CanonicalAbility(k)
			if !ok {
				return nil, fmt.Errorf("unknown ability %q in abilities JSON", k)
			}
			f, ok := v.(float64)
			if !ok {
				return nil, fmt.Errorf("ability %q must be a number", k)
			}
			out[key] = int(f)
		}
	default:
		parts := splitCSV(spec)
		if len(parts) != 6 {
			return nil, fmt.Errorf("abilities CSV needs 6 numbers (str,dex,con,int,wis,cha order), got %d", len(parts))
		}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, fmt.Errorf("abilities CSV: %q is not a number", p)
			}
			out[rules.Abilities[i]] = n
		}
	}
	// Fill gaps so the sheet never renders a zero STR by accident.
	for _, k := range rules.Abilities {
		if _, ok := out[k]; !ok {
			out[k] = 10
		}
	}
	for k, v := range out {
		if v < 1 || v > 30 {
			return nil, fmt.Errorf("ability %s=%d out of range 1..30", k, v)
		}
	}
	return out, nil
}

func parseSkills(spec string) ([]string, error) {
	var out []string
	for _, raw := range splitCSV(spec) {
		key, ok := rules.CanonicalSkill(raw)
		if !ok {
			return nil, fmt.Errorf("unknown skill %q — SRD skills: %s", raw, strings.Join(rules.SkillKeys(), ", "))
		}
		out = append(out, key)
	}
	return out, nil
}

func (s *Server) handleCharCreate(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strArg(req, "name")
	className := strArg(req, "class")
	if name == "" || className == "" {
		return mcp.NewToolResultError("name and class are required"), nil
	}
	campaign, err := s.st.ResolveCampaign(strArg(req, "campaign"))
	if err != nil {
		return errText(err), nil
	}
	abilities, err := parseAbilities(strArg(req, "abilities"))
	if err != nil {
		return errText(err), nil
	}
	skills, err := parseSkills(strArg(req, "skills"))
	if err != nil {
		return errText(err), nil
	}

	level := 1
	if n, ok := numArg(req, "level"); ok && n >= 1 && n <= 20 {
		level = n
	}
	kind := "pc"
	if k := strings.ToLower(strArg(req, "kind")); k == "npc" {
		kind = "npc"
	}

	conMod := rules.AbilityMod(abilities["con"])
	hitDie := 8
	var saves []string
	if cls, ok := rules.LookupClass(className); ok {
		hitDie = cls.HitDie
		saves = cls.Saves
		className = cls.Name
	}
	hpMax := rules.MaxHP(hitDie, level, conMod)

	ac := 10 + rules.AbilityMod(abilities["dex"])
	if n, ok := numArg(req, "ac"); ok && n > 0 {
		ac = n
	}
	speed := 30
	if n, ok := numArg(req, "speed"); ok && n > 0 {
		speed = n
	}

	c, err := s.st.CreateCharacter(store.Character{
		CampaignID: campaign.ID,
		Name:       name,
		Player:     strArg(req, "player"),
		Kind:       kind,
		Species:    strArg(req, "species"),
		Class:      className,
		Background: strArg(req, "background"),
		Alignment:  strArg(req, "alignment"),
		Level:      level,
		XP:         rules.XPThresholds[level],
		Abilities:  abilities,
		Skills:     skills,
		Saves:      saves,
		HPMax:      hpMax,
		HPCurrent:  hpMax,
		AC:         ac,
		Speed:      speed,
		Notes:      strArg(req, "notes"),
	})
	if err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(sheet.Render(c)), nil
}

func (s *Server) handleCharGet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, err := s.findChar(req)
	if err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(sheet.Render(c)), nil
}

func (s *Server) handleCharList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	campaign, err := s.st.ResolveCampaign(strArg(req, "campaign"))
	if err != nil {
		return errText(err), nil
	}
	chars, err := s.st.CharactersForCampaign(campaign.ID)
	if err != nil {
		return errText(err), nil
	}
	if len(chars) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No characters in **%s** yet — dnd_char_create adds one.", campaign.Name)), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Characters in **%s**:\n", campaign.Name)
	for _, c := range chars {
		b.WriteString("- " + sheet.Line(c) + "\n")
	}
	return mcp.NewToolResultText(b.String()), nil
}

// parseItem reads "Name", "Name x3", or "Name x2 | notes".
func parseItem(spec string) store.Item {
	it := store.Item{Qty: 1}
	if name, notes, found := strings.Cut(spec, "|"); found {
		it.Notes = strings.TrimSpace(notes)
		spec = strings.TrimSpace(name)
	}
	if i := strings.LastIndex(strings.ToLower(spec), " x"); i > 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(spec[i+2:])); err == nil && n > 0 {
			it.Qty = n
			spec = strings.TrimSpace(spec[:i])
		}
	}
	it.Name = strings.TrimSpace(spec)
	return it
}

func parseSlots(spec string) (map[string]int, error) {
	spec = strings.TrimSpace(spec)
	out := map[string]int{}
	if strings.HasPrefix(spec, "{") {
		var raw map[string]int
		if err := json.Unmarshal([]byte(spec), &raw); err != nil {
			return nil, fmt.Errorf("spell_slots JSON: %w", err)
		}
		return raw, nil
	}
	for _, p := range splitCSV(spec) {
		lvl, count, found := strings.Cut(p, ":")
		if !found {
			return nil, fmt.Errorf(`spell_slots: %q — use "1:4,2:2" or JSON`, p)
		}
		n, err := strconv.Atoi(strings.TrimSpace(count))
		if err != nil {
			return nil, fmt.Errorf("spell_slots: %q is not a number", count)
		}
		out[strings.TrimSpace(lvl)] = n
	}
	return out, nil
}

func (s *Server) handleCharUpdate(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, err := s.findChar(req)
	if err != nil {
		return errText(err), nil
	}
	var changes []string

	if n, ok := numArg(req, "hp_max"); ok {
		c.HPMax = n
		changes = append(changes, fmt.Sprintf("max HP → %d", n))
	}
	if n, ok := numArg(req, "hp_current"); ok {
		c.HPCurrent = n
		changes = append(changes, fmt.Sprintf("HP → %d", n))
	}
	if n, ok := numArg(req, "hp_delta"); ok {
		c.HPCurrent += n
		verb := "healed"
		amt := n
		if n < 0 {
			verb = "took"
			amt = -n
		}
		changes = append(changes, fmt.Sprintf("%s %d (%s)", verb, amt, hpWord(n)))
	}
	if c.HPCurrent < 0 {
		c.HPCurrent = 0
	}
	if c.HPCurrent > c.HPMax {
		c.HPCurrent = c.HPMax
	}
	if n, ok := numArg(req, "ac"); ok {
		c.AC = n
		changes = append(changes, fmt.Sprintf("AC → %d", n))
	}
	if n, ok := numArg(req, "speed"); ok {
		c.Speed = n
		changes = append(changes, fmt.Sprintf("speed → %d", n))
	}
	if n, ok := numArg(req, "xp_add"); ok {
		c.XP += n
		changes = append(changes, fmt.Sprintf("XP +%d → %d", n, c.XP))
		if lvl := rules.LevelForXP(c.XP); lvl > c.Level {
			changes = append(changes, fmt.Sprintf("⬆ enough XP for level %d — dnd_char_levelup applies it", lvl))
		}
	}
	if p := strArg(req, "player"); p != "" {
		c.Player = p
		changes = append(changes, "player → "+p)
	}
	if n := strArg(req, "notes_append"); n != "" {
		if c.Notes != "" {
			c.Notes += "\n"
		}
		c.Notes += n
		changes = append(changes, "notes updated")
	}
	if spec := strArg(req, "inventory_add"); spec != "" {
		it := parseItem(spec)
		merged := false
		for i := range c.Inventory {
			if strings.EqualFold(c.Inventory[i].Name, it.Name) {
				c.Inventory[i].Qty += it.Qty
				merged = true
				break
			}
		}
		if !merged {
			c.Inventory = append(c.Inventory, it)
		}
		changes = append(changes, fmt.Sprintf("+%d %s", it.Qty, it.Name))
	}
	if name := strArg(req, "inventory_remove"); name != "" {
		removed := false
		for i := range c.Inventory {
			if strings.EqualFold(c.Inventory[i].Name, name) {
				c.Inventory[i].Qty--
				if c.Inventory[i].Qty <= 0 {
					c.Inventory = append(c.Inventory[:i], c.Inventory[i+1:]...)
				}
				removed = true
				break
			}
		}
		if !removed {
			return mcp.NewToolResultError(fmt.Sprintf("%s has no item named %q", c.Name, name)), nil
		}
		changes = append(changes, "-1 "+name)
	}
	if spec := strArg(req, "spell_slots"); spec != "" {
		slots, err := parseSlots(spec)
		if err != nil {
			return errText(err), nil
		}
		c.SpellSlots = slots
		changes = append(changes, "spell slots set")
	}
	if spec := strArg(req, "skills_add"); spec != "" {
		add, err := parseSkills(spec)
		if err != nil {
			return errText(err), nil
		}
		for _, k := range add {
			if !containsFold(c.Skills, k) {
				c.Skills = append(c.Skills, k)
				changes = append(changes, "skill +"+rules.SkillName(k))
			}
		}
	}
	if spec := strArg(req, "features_add"); spec != "" {
		for _, f := range splitCSV(spec) {
			c.Features = append(c.Features, f)
			changes = append(changes, "feature +"+f)
		}
	}
	if spec := strArg(req, "attacks_add"); spec != "" {
		a, err := parseAttack(spec)
		if err != nil {
			return errText(err), nil
		}
		replaced := false
		for i := range c.Attacks {
			if strings.EqualFold(c.Attacks[i].Name, a.Name) {
				c.Attacks[i] = a
				replaced = true
				break
			}
		}
		if !replaced {
			c.Attacks = append(c.Attacks, a)
		}
		changes = append(changes, "attack +"+a.Name)
	}
	if name := strArg(req, "attacks_remove"); name != "" {
		removed := false
		for i := range c.Attacks {
			if strings.EqualFold(c.Attacks[i].Name, name) {
				c.Attacks = append(c.Attacks[:i], c.Attacks[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			return mcp.NewToolResultError(fmt.Sprintf("%s has no attack named %q", c.Name, name)), nil
		}
		changes = append(changes, "attack -"+name)
	}
	if spec := strArg(req, "spells_add"); spec != "" {
		add, err := parseSpells(spec)
		if err != nil {
			return errText(err), nil
		}
		for _, sp := range add {
			dup := false
			for i := range c.Spells {
				if strings.EqualFold(c.Spells[i].Name, sp.Name) {
					c.Spells[i] = sp
					dup = true
					break
				}
			}
			if !dup {
				c.Spells = append(c.Spells, sp)
			}
			changes = append(changes, "spell +"+sp.Name)
		}
	}
	if name := strArg(req, "spells_remove"); name != "" {
		removed := false
		for i := range c.Spells {
			if strings.EqualFold(c.Spells[i].Name, name) {
				c.Spells = append(c.Spells[:i], c.Spells[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			return mcp.NewToolResultError(fmt.Sprintf("%s knows no spell named %q", c.Name, name)), nil
		}
		changes = append(changes, "spell -"+name)
	}
	if spec := strArg(req, "conditions_add"); spec != "" {
		for _, cond := range splitCSV(spec) {
			if !containsFold(c.Conditions, cond) {
				c.Conditions = append(c.Conditions, strings.ToLower(cond))
				changes = append(changes, "condition +"+cond)
			}
		}
	}
	if spec := strArg(req, "conditions_remove"); spec != "" {
		for _, cond := range splitCSV(spec) {
			for i := range c.Conditions {
				if strings.EqualFold(c.Conditions[i], cond) {
					c.Conditions = append(c.Conditions[:i], c.Conditions[i+1:]...)
					changes = append(changes, "condition -"+cond)
					break
				}
			}
		}
	}

	if len(changes) == 0 {
		return mcp.NewToolResultText("Nothing to change — pass at least one field."), nil
	}
	c, err = s.st.SaveCharacter(c)
	if err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("**%s** updated: %s. HP %d/%d, AC %d.",
		c.Name, strings.Join(changes, "; "), c.HPCurrent, c.HPMax, c.AC)), nil
}

func hpWord(delta int) string {
	if delta < 0 {
		return "damage"
	}
	return "healing"
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func (s *Server) handleCharLevelup(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c, err := s.findChar(req)
	if err != nil {
		return errText(err), nil
	}
	if c.Level >= 20 {
		return mcp.NewToolResultText(fmt.Sprintf("%s is already level 20 — the SRD ladder tops out here.", c.Name)), nil
	}
	hitDie := 8
	if cls, ok := rules.LookupClass(c.Class); ok {
		hitDie = cls.HitDie
	}
	conMod := rules.AbilityMod(c.Abilities["con"])
	gain := rules.HPPerLevel(hitDie, conMod)
	oldProf := rules.ProficiencyBonus(c.Level)
	c.Level++
	c.HPMax += gain
	c.HPCurrent += gain
	if c.XP < rules.XPThresholds[c.Level] {
		c.XP = rules.XPThresholds[c.Level]
	}
	newProf := rules.ProficiencyBonus(c.Level)
	c, err = s.st.SaveCharacter(c)
	if err != nil {
		return errText(err), nil
	}
	msg := fmt.Sprintf("**%s** is now level %d! HP +%d → %d/%d.", c.Name, c.Level, gain, c.HPCurrent, c.HPMax)
	if newProf > oldProf {
		msg += fmt.Sprintf(" Proficiency bonus rises to %s.", rules.FmtMod(newProf))
	}
	return mcp.NewToolResultText(msg), nil
}

func (s *Server) handleCharCheck(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	check := strArg(req, "check")
	if check == "" {
		return mcp.NewToolResultError("check is required (e.g. perception, str, dex save, initiative)"), nil
	}
	c, err := s.findChar(req)
	if err != nil {
		return errText(err), nil
	}
	resolved, err := rules.ResolveCheck(check, c.Abilities, c.Skills, c.Saves, c.Level)
	if err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("%s — %s: %s (%s).\nRoll it in the open: `%s`",
		c.Name, resolved.Label, rules.FmtMod(resolved.Mod), resolved.Breakdown,
		resolved.RollSuggestion(c.Name))), nil
}

// --- reference handlers ------------------------------------------------------

func (s *Server) handleRefSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	typ := strArg(req, "type")
	query := strArg(req, "query")
	if typ == "" || query == "" {
		return mcp.NewToolResultError("type and query are required"), nil
	}
	limit, _ := numArg(req, "limit")
	results, err := s.ref.Search(ctx, typ, query, strArg(req, "ruleset"), limit)
	if err != nil {
		return errText(err), nil
	}
	if len(results) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No %s entries match %q in that ruleset.", typ, query)), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s result(s) for %q:\n", len(results), typ, query)
	for _, r := range results {
		fmt.Fprintf(&b, "- %s — `%s`%s\n", r.Name, r.Key, refHint(typ, r.Raw))
	}
	b.WriteString("\nFetch detail with dnd_ref_get.")
	return mcp.NewToolResultText(b.String()), nil
}

// refHint adds a short identifying tail to a search line.
func refHint(typ string, raw map[string]any) string {
	switch typ {
	case "creature":
		var parts []string
		if v, ok := raw["challenge_rating_text"].(string); ok && v != "" {
			parts = append(parts, "CR "+v)
		}
		if m, ok := raw["type"].(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				parts = append(parts, n)
			}
		}
		if len(parts) > 0 {
			return " (" + strings.Join(parts, ", ") + ")"
		}
	case "spell":
		if lvl, ok := raw["level"].(float64); ok {
			if school, ok := raw["school"].(map[string]any); ok {
				if n, ok := school["name"].(string); ok {
					return fmt.Sprintf(" (level %d %s)", int(lvl), n)
				}
			}
			return fmt.Sprintf(" (level %d)", int(lvl))
		}
	}
	return ""
}

func (s *Server) handleRefGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	typ := strArg(req, "type")
	key := strArg(req, "key")
	if typ == "" || key == "" {
		return mcp.NewToolResultError("type and key are required"), nil
	}
	entry, err := s.ref.Get(ctx, typ, key)
	if err != nil {
		return errText(err), nil
	}
	return mcp.NewToolResultText(formatRefEntry(typ, entry)), nil
}

// formatRefEntry renders an Open5e v2 entry compactly for chat. Shape-tolerant:
// unknown fields are simply skipped, and a final desc block carries the text.
func formatRefEntry(typ string, e map[string]any) string {
	var b strings.Builder
	name, _ := e["name"].(string)
	fmt.Fprintf(&b, "## %s\n", name)

	str := func(key string) string { v, _ := e[key].(string); return v }
	num := func(key string) (float64, bool) { v, ok := e[key].(float64); return v, ok }
	nested := func(key, sub string) string {
		if m, ok := e[key].(map[string]any); ok {
			v, _ := m[sub].(string)
			return v
		}
		return ""
	}

	switch typ {
	case "creature":
		var line []string
		if v := nested("size", "name"); v != "" {
			line = append(line, v)
		}
		if v := nested("type", "name"); v != "" {
			line = append(line, v)
		}
		if v := str("alignment"); v != "" {
			line = append(line, v)
		}
		if len(line) > 0 {
			fmt.Fprintf(&b, "*%s*\n", strings.Join(line, " "))
		}
		if v, ok := num("armor_class"); ok {
			fmt.Fprintf(&b, "**AC** %d", int(v))
		}
		if v, ok := num("hit_points"); ok {
			fmt.Fprintf(&b, " · **HP** %d", int(v))
			if hd := str("hit_dice"); hd != "" {
				fmt.Fprintf(&b, " (%s)", hd)
			}
		}
		if cr := str("challenge_rating_text"); cr != "" {
			fmt.Fprintf(&b, " · **CR** %s", cr)
		}
		b.WriteString("\n")
		if scores, ok := e["ability_scores"].(map[string]any); ok {
			var abl []string
			for _, k := range []string{"strength", "dexterity", "constitution", "intelligence", "wisdom", "charisma"} {
				if v, ok := scores[k].(float64); ok {
					abl = append(abl, fmt.Sprintf("%s %d", strings.ToUpper(k[:3]), int(v)))
				}
			}
			b.WriteString(strings.Join(abl, " · ") + "\n")
		}
		if actions, ok := e["actions"].([]any); ok && len(actions) > 0 {
			b.WriteString("\n**Actions:**\n")
			for _, a := range actions {
				if m, ok := a.(map[string]any); ok {
					an, _ := m["name"].(string)
					ad, _ := m["desc"].(string)
					fmt.Fprintf(&b, "- **%s.** %s\n", an, ad)
				}
			}
		}
		if traits, ok := e["traits"].([]any); ok && len(traits) > 0 {
			b.WriteString("\n**Traits:**\n")
			for _, t := range traits {
				if m, ok := t.(map[string]any); ok {
					tn, _ := m["name"].(string)
					td, _ := m["desc"].(string)
					fmt.Fprintf(&b, "- **%s.** %s\n", tn, td)
				}
			}
		}
	case "spell":
		var line []string
		if v, ok := num("level"); ok {
			line = append(line, fmt.Sprintf("Level %d", int(v)))
		}
		if v := nested("school", "name"); v != "" {
			line = append(line, v)
		}
		if len(line) > 0 {
			fmt.Fprintf(&b, "*%s*\n", strings.Join(line, " "))
		}
		if v := str("casting_time"); v != "" {
			fmt.Fprintf(&b, "**Casting time** %s", v)
		}
		if v := str("range_text"); v != "" {
			fmt.Fprintf(&b, " · **Range** %s", v)
		}
		if v := str("duration"); v != "" {
			fmt.Fprintf(&b, " · **Duration** %s", v)
		}
		b.WriteString("\n")
	}

	if desc := str("desc"); desc != "" {
		fmt.Fprintf(&b, "\n%s\n", desc)
	}
	// Stable key order for any leftover simple fields worth showing.
	extras := []string{"higher_level", "material", "languages"}
	sort.Strings(extras)
	for _, k := range extras {
		if v, ok := e[k].(string); ok && v != "" {
			fmt.Fprintf(&b, "\n**%s:** %s\n", strings.ReplaceAll(k, "_", " "), v)
		}
	}
	b.WriteString("\n*Source: SRD via Open5e (CC-BY-4.0).*")
	return b.String()
}

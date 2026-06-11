# dnd-tools

An [MCP](https://modelcontextprotocol.io) server for running D&D 5e campaigns with AI agents at the table: **character sheets, campaign state, and a session log you own**, on the SRD 5.2 ruleset, with reference data (creatures, spells, items, conditions) served from [Open5e](https://open5e.com) and cached locally.

Built as the game-state backbone for AI-driven campaigns on [ai-chattermax](https://github.com/cpuchip/ai-chattermax) — a DM persona looks up monsters, a Party persona builds and levels PCs — but it's a standalone server any MCP client can use.

## The dice-honesty design

**dnd-tools never rolls dice.** `dnd_char_check` resolves a check to its modifier and hands back the exact `/roll` command to post in the room:

> Thorin — Perception (WIS): +5 (WIS +3, proficiency +2).
> Roll it in the open: `/roll 1d20+5 [Thorin — Perception (WIS)]`

The chat server rolls server-side (crypto/rand), in history, for every sender. One implementation, one fairness story: neither a human nor a model can fudge a roll, and a model never *invents* one — the same discipline as read-before-quoting.

## Tools

| Tool | What it does |
|------|--------------|
| `dnd_campaign_create` | Create a campaign (status: prep → active → archived) |
| `dnd_campaign_get` | Overview: premise, roster, recent session log |
| `dnd_campaign_log` | Append a session recap (the archive/resume record) |
| `dnd_char_create` | Create a sheet — HP and save proficiencies derive from class; standard array, CSV, or JSON ability scores |
| `dnd_char_get` | Full markdown sheet |
| `dnd_char_list` | One-line roster |
| `dnd_char_update` | Damage/heal (`hp_delta`), AC, XP, inventory add/remove, spell slots, skills, features, notes |
| `dnd_char_levelup` | +1 level: average HP gain, proficiency recompute |
| `dnd_char_check` | Modifier + breakdown + the `/roll` command (skills, abilities, saves, initiative) |
| `dnd_ref_search` | Search SRD creatures/spells/items/conditions by name (Open5e, cached) |
| `dnd_ref_get` | One reference entry, formatted for the table |

Rulesets: `srd-2024` (SRD 5.2, the 2024 rules — default) or `srd-2014` (SRD 5.1).

## Run

```sh
go build ./cmd/dnd-mcp

# stdio MCP server; state in ./dnd.db (or -db / DND_DB)
./dnd-mcp

# with the optional read-only HTTP sheet API
./dnd-mcp -http :8089
# GET /healthz · /api/campaigns · /api/campaigns/{name}
# GET /api/characters/{name}?campaign=X · /api/characters/{name}/sheet
```

Storage is a single SQLite file (pure-Go driver — cross-compiles with `CGO_ENABLED=0`). Open5e responses are cached in the same file, so repeat lookups are instant and work offline.

## Architecture

```
cmd/dnd-mcp/        entry — stdio MCP server + optional HTTP API
internal/rules/     SRD 5.2 core math: modifiers, proficiency, skills, classes, XP
internal/store/     SQLite: campaigns, characters, campaign log, ref cache
internal/sheet/     markdown sheet renderer
internal/open5e/    Open5e client (read-through cache)
internal/mcpserver/ tool registration + handlers
internal/httpapi/   read-only JSON/markdown sheet API
```

The character model is deliberately flat JSON-friendly columns — close enough to common character-JSON exports that an importer (e.g. from a user-supplied D&D Beyond export) could map onto it later. There is no D&D Beyond API integration: none exists publicly.

## Licenses & attribution

- **Code:** MIT (see [LICENSE](LICENSE)).
- **Rules data:** This work includes material from the **System Reference Document 5.2.1** ("SRD 5.2.1") by Wizards of the Coast LLC, available at <https://www.dndbeyond.com/srd>. The SRD 5.2.1 is licensed under the [Creative Commons Attribution 4.0 International License](https://creativecommons.org/licenses/by/4.0/legalcode). Portions derive likewise from the **SRD 5.1** under the same license.
- **Reference API:** [Open5e](https://open5e.com) — an open-source API over the SRDs and other openly licensed 5e content. Cached responses carry the same CC-BY-4.0 attribution.

D&D and Dungeons & Dragons are trademarks of Wizards of the Coast LLC. This project is unaffiliated.

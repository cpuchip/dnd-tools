// Package httpapi is the JSON surface a chat server (or a sheet panel) calls:
// room→campaign resolution, player→character binding, check/attack/cast
// resolution (returning the /roll commands — dice stay with the caller's
// room), and sheet reads/edits.
//
// Auth: when an API key is configured, every /api/* request needs
// `Authorization: Bearer <key>` (or `?key=`). DM-secret lore is NEVER served
// here — this is the player-facing surface.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cpuchip/dnd-tools/internal/open5e"
	"github.com/cpuchip/dnd-tools/internal/rules"
	"github.com/cpuchip/dnd-tools/internal/sheet"
	"github.com/cpuchip/dnd-tools/internal/store"
)

// Handler builds the API mux. ref may be nil (cast skips the damage lookup).
func Handler(st *store.Store, ref *open5e.Client, version, apiKey string) http.Handler {
	mux := http.NewServeMux()

	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	writeErr := func(w http.ResponseWriter, err error) {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
	}
	badReq := func(w http.ResponseWriter, msg string) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
	})

	mux.HandleFunc("GET /api/campaigns", func(w http.ResponseWriter, r *http.Request) {
		cs, err := st.Campaigns()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cs)
	})

	mux.HandleFunc("POST /api/campaigns", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Setting     string `json:"setting"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			badReq(w, `body must be {"name":"...", "description":..., "setting":...}`)
			return
		}
		c, err := st.CreateCampaign(strings.TrimSpace(body.Name), body.Description, body.Setting)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})

	// PUT /api/rooms/{roomID}/campaign {"campaign":"name"} binds (creating the
	// campaign if needed); {"campaign":""} unbinds. The room's binding IS the
	// D&D feature switch (DH-4 room gating).
	mux.HandleFunc("PUT /api/rooms/{roomID}/campaign", func(w http.ResponseWriter, r *http.Request) {
		roomID := r.PathValue("roomID")
		var body struct {
			Campaign string `json:"campaign"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			badReq(w, `body must be {"campaign":"<name>"} (empty name unbinds)`)
			return
		}
		name := strings.TrimSpace(body.Campaign)
		if name == "" {
			c, err := st.CampaignByRoom(roomID)
			if err != nil {
				writeErr(w, err)
				return
			}
			if err := st.BindRoom(c.ID, ""); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"unbound": c.Name})
			return
		}
		c, err := st.CampaignByName(name)
		if errors.Is(err, store.ErrNotFound) {
			if c, err = st.CreateCampaign(name, "", ""); err != nil {
				writeErr(w, err)
				return
			}
		} else if err != nil {
			writeErr(w, err)
			return
		}
		if err := st.BindRoom(c.ID, roomID); err != nil {
			writeErr(w, err)
			return
		}
		c, _ = st.CampaignByID(c.ID)
		writeJSON(w, http.StatusOK, c)
	})

	mux.HandleFunc("GET /api/campaigns/{name}", func(w http.ResponseWriter, r *http.Request) {
		c, err := st.CampaignByName(r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		chars, err := st.CharactersForCampaign(c.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		log, err := st.RecentLog(c.ID, 10)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"campaign": c, "characters": chars, "log": log,
		})
	})

	// --- room-scoped (what a chat server calls) -------------------------

	mux.HandleFunc("GET /api/rooms/{roomID}/campaign", func(w http.ResponseWriter, r *http.Request) {
		c, err := st.CampaignByRoom(r.PathValue("roomID"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})

	// Roster with HP — the room's HP chips.
	mux.HandleFunc("GET /api/rooms/{roomID}/characters", func(w http.ResponseWriter, r *http.Request) {
		c, err := st.CampaignByRoom(r.PathValue("roomID"))
		if err != nil {
			writeErr(w, err)
			return
		}
		chars, err := st.CharactersForCampaign(c.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"campaign": c.Name, "characters": chars})
	})

	// The character a player runs in this room's campaign.
	mux.HandleFunc("GET /api/rooms/{roomID}/player/{player}", func(w http.ResponseWriter, r *http.Request) {
		c, err := st.CampaignByRoom(r.PathValue("roomID"))
		if err != nil {
			writeErr(w, err)
			return
		}
		ch, err := st.FindCharacterByPlayer(r.PathValue("player"), c.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ch)
	})

	// --- character reads ---------------------------------------------------

	charLookup := func(r *http.Request, name string) (store.Character, error) {
		var campaignID int64
		if cn := r.URL.Query().Get("campaign"); cn != "" {
			c, err := st.CampaignByName(cn)
			if err != nil {
				return store.Character{}, err
			}
			campaignID = c.ID
		}
		return st.FindCharacter(name, campaignID)
	}

	mux.HandleFunc("GET /api/characters/{name}", func(w http.ResponseWriter, r *http.Request) {
		c, err := charLookup(r, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})

	mux.HandleFunc("GET /api/characters/{name}/sheet", func(w http.ResponseWriter, r *http.Request) {
		c, err := charLookup(r, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(sheet.Render(c)))
	})

	// --- resolution (the slash commands) ---------------------------------

	// POST /api/characters/{name}/check {"check":"stealth"} — also accepts
	// ?campaign=. Returns the modifier + the /roll command.
	mux.HandleFunc("POST /api/characters/{name}/check", func(w http.ResponseWriter, r *http.Request) {
		c, err := charLookup(r, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct {
			Check string `json:"check"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Check) == "" {
			badReq(w, `body must be {"check":"<skill | ability | X save | initiative>"}`)
			return
		}
		resolved, err := rules.ResolveCheck(body.Check, c.Abilities, c.Skills, c.Saves, c.Level)
		if err != nil {
			badReq(w, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"character": c.Name,
			"label":     resolved.Label,
			"mod":       resolved.Mod,
			"breakdown": resolved.Breakdown,
			"roll":      resolved.RollSuggestion(c.Name),
		})
	})

	// POST /api/characters/{name}/attack {"weapon":"longsword","target":"the goblin"}
	mux.HandleFunc("POST /api/characters/{name}/attack", func(w http.ResponseWriter, r *http.Request) {
		c, err := charLookup(r, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct {
			Weapon string `json:"weapon"`
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			badReq(w, `body must be {"weapon":"...", "target":"..."} (both optional)`)
			return
		}
		a, err := sheet.FindAttack(c, body.Weapon)
		if err != nil {
			badReq(w, err.Error())
			return
		}
		res := sheet.ResolveAttack(c, a, body.Target)
		writeJSON(w, http.StatusOK, map[string]any{
			"character": c.Name, "result": res,
		})
	})

	// POST /api/characters/{name}/cast {"spell":"fireball","slot_level":3} —
	// verifies the spell is known, spends the slot (cantrips free), returns
	// remaining slots and (when Open5e knows it) the damage dice.
	mux.HandleFunc("POST /api/characters/{name}/cast", func(w http.ResponseWriter, r *http.Request) {
		c, err := charLookup(r, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct {
			Spell     string `json:"spell"`
			SlotLevel int    `json:"slot_level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Spell) == "" {
			badReq(w, `body must be {"spell":"...", "slot_level": <optional>}`)
			return
		}
		sp, err := sheet.FindSpell(c, body.Spell)
		if err != nil {
			badReq(w, err.Error())
			return
		}
		out := map[string]any{"character": c.Name, "spell": sp.Name, "level": sp.Level}
		if sp.Level > 0 {
			slot := sp.Level
			if body.SlotLevel != 0 {
				if body.SlotLevel < sp.Level || body.SlotLevel > 9 {
					badReq(w, fmt.Sprintf("%s is a level-%d spell — slot_level must be %d..9", sp.Name, sp.Level, sp.Level))
					return
				}
				slot = body.SlotLevel
			}
			key := strconv.Itoa(slot)
			if c.SpellSlots[key] <= 0 {
				badReq(w, fmt.Sprintf("%s has no level-%d spell slots left", c.Name, slot))
				return
			}
			c.SpellSlots[key]--
			if c, err = st.SaveCharacter(c); err != nil {
				writeErr(w, err)
				return
			}
			out["slot_used"] = slot
			out["slots_remaining"] = c.SpellSlots[key]
		}
		if ref != nil && sp.Key != "" {
			if entry, err := ref.Get(r.Context(), "spell", sp.Key); err == nil {
				if dmg, ok := entry["damage_roll"].(string); ok && dmg != "" {
					out["damage_roll"] = dmg
				}
			}
		}
		writeJSON(w, http.StatusOK, out)
	})

	// POST /api/characters/{name}/hp {"delta":-5} — clamped to [0, hp_max].
	mux.HandleFunc("POST /api/characters/{name}/hp", func(w http.ResponseWriter, r *http.Request) {
		c, err := charLookup(r, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var body struct {
			Delta int `json:"delta"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Delta == 0 {
			badReq(w, `body must be {"delta": <non-zero int>} (negative = damage)`)
			return
		}
		c.HPCurrent += body.Delta
		if c.HPCurrent < 0 {
			c.HPCurrent = 0
		}
		if c.HPCurrent > c.HPMax {
			c.HPCurrent = c.HPMax
		}
		c, err = st.SaveCharacter(c)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"character": c.Name, "hp_current": c.HPCurrent, "hp_max": c.HPMax, "delta": body.Delta,
		})
	})

	// PATCH /api/characters/{name} — partial sheet edit (the /char panel).
	// Present fields replace; absent fields keep. Arrays/maps replace whole.
	mux.HandleFunc("PATCH /api/characters/{name}", func(w http.ResponseWriter, r *http.Request) {
		c, err := charLookup(r, r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var patch map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			badReq(w, "body must be a partial character JSON object")
			return
		}
		if err := applyPatch(&c, patch); err != nil {
			badReq(w, err.Error())
			return
		}
		c, err = st.SaveCharacter(c)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})

	// --- lore (player-facing: secrets excluded ALWAYS) ----------------------

	mux.HandleFunc("GET /api/rooms/{roomID}/lore", func(w http.ResponseWriter, r *http.Request) {
		c, err := st.CampaignByRoom(r.PathValue("roomID"))
		if err != nil {
			writeErr(w, err)
			return
		}
		var entries []store.LoreEntry
		if q := r.URL.Query().Get("q"); q != "" {
			entries, err = st.LoreSearch(c.ID, q, false)
		} else {
			entries, err = st.LoreList(c.ID, r.URL.Query().Get("kind"), false)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"campaign": c.Name, "lore": entries})
	})

	return withAuth(apiKey, mux)
}

// applyPatch copies present fields from a partial JSON object onto c. The
// panel sends only what changed; identity fields (id, campaign) are immutable.
func applyPatch(c *store.Character, patch map[string]json.RawMessage) error {
	targets := map[string]any{
		"player": &c.Player, "species": &c.Species, "class": &c.Class,
		"background": &c.Background, "alignment": &c.Alignment, "notes": &c.Notes,
		"level": &c.Level, "xp": &c.XP, "hp_max": &c.HPMax, "hp_current": &c.HPCurrent,
		"ac": &c.AC, "speed": &c.Speed,
		"abilities": &c.Abilities, "skills": &c.Skills, "saves": &c.Saves,
		"inventory": &c.Inventory, "spell_slots": &c.SpellSlots,
		"attacks": &c.Attacks, "spells": &c.Spells, "conditions": &c.Conditions,
		"features": &c.Features,
	}
	for key, raw := range patch {
		dst, ok := targets[key]
		if !ok {
			continue // unknown/immutable fields are ignored, not errors
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return errors.New("field " + key + ": " + err.Error())
		}
	}
	if c.Level < 1 {
		c.Level = 1
	}
	if c.Level > 20 {
		c.Level = 20
	}
	if c.HPCurrent < 0 {
		c.HPCurrent = 0
	}
	return nil
}

// withAuth gates /api/* behind the bearer key (when configured). /healthz
// stays open for liveness probes.
func withAuth(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			next.ServeHTTP(w, r)
			return
		case !strings.HasPrefix(r.URL.Path, "/api/"):
			http.NotFound(w, r)
			return
		}
		if apiKey != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" {
				got = r.URL.Query().Get("key")
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"missing or invalid API key"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

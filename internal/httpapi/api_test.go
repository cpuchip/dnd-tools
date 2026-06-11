package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpuchip/dnd-tools/internal/store"
)

func testServer(t *testing.T, apiKey string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(Handler(st, nil, "test", apiKey))
	t.Cleanup(srv.Close)
	return srv, st
}

func seed(t *testing.T, st *store.Store) store.Character {
	t.Helper()
	camp, err := st.CreateCampaign("API Test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoom(camp.ID, "room-9"); err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateCharacter(store.Character{
		CampaignID: camp.ID, Name: "Vexa", Player: "Michael",
		Level: 1, HPMax: 9, HPCurrent: 9, AC: 14,
		Abilities: map[string]int{"str": 9, "dex": 16, "con": 13, "int": 12, "wis": 14, "cha": 11},
		Skills:    []string{"stealth"},
		Attacks:   []store.Attack{{Name: "Dagger", Ability: "dex", Proficient: true, Damage: "1d4", DamageType: "piercing"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func req(t *testing.T, method, url, key, body string) *http.Response {
	t.Helper()
	var r *http.Request
	if body != "" {
		r, _ = http.NewRequest(method, url, strings.NewReader(body))
	} else {
		r, _ = http.NewRequest(method, url, nil)
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAuthGate(t *testing.T) {
	srv, st := testServer(t, "sekrit")
	seed(t, st)

	// No key → 401; wrong key → 401; right key → 200; healthz open.
	if resp := req(t, "GET", srv.URL+"/api/campaigns", "", ""); resp.StatusCode != 401 {
		t.Errorf("no key = %d, want 401", resp.StatusCode)
	}
	if resp := req(t, "GET", srv.URL+"/api/campaigns", "wrong", ""); resp.StatusCode != 401 {
		t.Errorf("wrong key = %d, want 401", resp.StatusCode)
	}
	if resp := req(t, "GET", srv.URL+"/api/campaigns", "sekrit", ""); resp.StatusCode != 200 {
		t.Errorf("right key = %d, want 200", resp.StatusCode)
	}
	if resp := req(t, "GET", srv.URL+"/healthz", "", ""); resp.StatusCode != 200 {
		t.Errorf("healthz = %d, want 200", resp.StatusCode)
	}
}

func TestRoomResolutionAndChecks(t *testing.T) {
	srv, st := testServer(t, "")
	seed(t, st)

	// Player binding through the room.
	got := decode[store.Character](t, req(t, "GET", srv.URL+"/api/rooms/room-9/player/Michael", "", ""))
	if got.Name != "Vexa" {
		t.Errorf("player resolution = %+v", got)
	}

	// Check: stealth = DEX +3 + prof +2.
	check := decode[map[string]any](t, req(t, "POST", srv.URL+"/api/characters/Vexa/check", "", `{"check":"stealth"}`))
	if check["mod"].(float64) != 5 {
		t.Errorf("stealth mod = %v", check["mod"])
	}
	if !strings.Contains(check["roll"].(string), "1d20+5") {
		t.Errorf("roll = %v", check["roll"])
	}

	// Attack: dagger DEX +3 + prof +2 to hit; damage 1d4+3.
	atk := decode[struct {
		Result struct {
			ToHit      int    `json:"to_hit"`
			DamageExpr string `json:"damage_expr"`
			ToHitRoll  string `json:"to_hit_roll"`
		} `json:"result"`
	}](t, req(t, "POST", srv.URL+"/api/characters/Vexa/attack", "", `{"target":"the goblin"}`))
	if atk.Result.ToHit != 5 || atk.Result.DamageExpr != "1d4+3" {
		t.Errorf("attack = %+v", atk.Result)
	}
	if !strings.Contains(atk.Result.ToHitRoll, "vs the goblin") {
		t.Errorf("to-hit roll = %q", atk.Result.ToHitRoll)
	}

	// HP delta clamps at 0.
	hp := decode[map[string]any](t, req(t, "POST", srv.URL+"/api/characters/Vexa/hp", "", `{"delta":-50}`))
	if hp["hp_current"].(float64) != 0 {
		t.Errorf("hp = %v", hp)
	}

	// PATCH edits a field and replaces an array.
	patched := decode[store.Character](t, req(t, "PATCH", srv.URL+"/api/characters/Vexa", "",
		`{"ac": 15, "conditions": ["prone"]}`))
	if patched.AC != 15 || len(patched.Conditions) != 1 {
		t.Errorf("patch = %+v", patched)
	}

	// Unknown room errors.
	if resp := req(t, "GET", srv.URL+"/api/rooms/nope/campaign", "", ""); resp.StatusCode == 200 {
		t.Error("unbound room should not be 200")
	}
}

func TestLoreSecretsNeverServed(t *testing.T) {
	srv, st := testServer(t, "")
	c := seed(t, st)
	if _, err := st.SetLore(c.CampaignID, "plot", "Secret Twist", "The mayor did it.", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetLore(c.CampaignID, "location", "Town Square", "Cobblestones.", false); err != nil {
		t.Fatal(err)
	}
	lore := decode[struct {
		Lore []store.LoreEntry `json:"lore"`
	}](t, req(t, "GET", srv.URL+"/api/rooms/room-9/lore", "", ""))
	if len(lore.Lore) != 1 || lore.Lore[0].Name != "Town Square" {
		t.Errorf("public lore = %+v", lore.Lore)
	}
	// Search can't reach secrets either.
	hits := decode[struct {
		Lore []store.LoreEntry `json:"lore"`
	}](t, req(t, "GET", srv.URL+"/api/rooms/room-9/lore?q=mayor", "", ""))
	if len(hits.Lore) != 0 {
		t.Errorf("secret leaked: %+v", hits.Lore)
	}
}

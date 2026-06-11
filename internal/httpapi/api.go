// Package httpapi is the small read-only JSON surface for sheet viewing —
// a chat frontend (or a curious player) can render a character without MCP.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cpuchip/dnd-tools/internal/sheet"
	"github.com/cpuchip/dnd-tools/internal/store"
)

// Handler builds the API mux.
func Handler(st *store.Store, version string) http.Handler {
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

	// /api/characters/{name}?campaign=X — JSON sheet;
	// /api/characters/{name}/sheet?campaign=X — rendered markdown.
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

	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

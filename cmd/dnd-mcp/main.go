// dnd-mcp is the dnd-tools server: character + campaign state on the SRD 5.2
// ruleset, with Open5e-backed (cached) reference lookups.
//
// Modes:
//
//	dnd-mcp                      stdio MCP server (Claude Code, local bridges)
//	dnd-mcp -http :8089          stdio MCP + HTTP (JSON API + MCP at /mcp)
//	dnd-mcp -serve -http :8089   HTTP only — the container/deployment mode
//
// Flags fall back to env (DND_DB, DND_HTTP, DND_API_KEY). When an API key is
// set, /api/* needs `Authorization: Bearer <key>` and /mcp needs `?key=`.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/cpuchip/dnd-tools/internal/httpapi"
	"github.com/cpuchip/dnd-tools/internal/mcpserver"
	"github.com/cpuchip/dnd-tools/internal/open5e"
	"github.com/cpuchip/dnd-tools/internal/store"
)

const version = "0.2.0"

func main() {
	dbPath := flag.String("db", envOr("DND_DB", "dnd.db"), "SQLite database path (env DND_DB)")
	httpAddr := flag.String("http", os.Getenv("DND_HTTP"), "HTTP listen address, e.g. :8089 (env DND_HTTP)")
	apiKey := flag.String("api-key", os.Getenv("DND_API_KEY"), "bearer key guarding /api/* and /mcp (env DND_API_KEY; empty = no auth)")
	serveOnly := flag.Bool("serve", false, "HTTP-only mode (no stdio MCP) — for containers")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("dnd-tools", version)
		return
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store %s: %v", *dbPath, err)
	}
	defer st.Close()

	ref := open5e.New(st)
	srv := mcpserver.New(st, ref, version)

	if *httpAddr != "" {
		handler := buildHTTP(st, ref, srv, *apiKey)
		if *serveOnly {
			log.Printf("dnd-tools %s serving HTTP on %s (api + /mcp)", version, *httpAddr)
			log.Fatal(http.ListenAndServe(*httpAddr, handler))
		}
		go func() {
			log.Printf("dnd-tools %s HTTP on %s (api + /mcp)", version, *httpAddr)
			if err := http.ListenAndServe(*httpAddr, handler); err != nil {
				log.Printf("http: %v", err)
			}
		}()
	} else if *serveOnly {
		log.Fatal("-serve requires -http (or DND_HTTP)")
	}

	if err := srv.Serve(); err != nil {
		log.Fatalf("mcp serve: %v", err)
	}
}

// buildHTTP mounts the JSON API and the streamable-HTTP MCP endpoint on one
// listener. The MCP endpoint shares the same key (?key= — the bridge's http
// transport carries auth in the URL, like exa-search).
func buildHTTP(st *store.Store, ref *open5e.Client, srv *mcpserver.Server, apiKey string) http.Handler {
	api := httpapi.Handler(st, ref, version, apiKey)
	mcpHandler := srv.HTTPHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/mcp") {
			if apiKey != "" && r.URL.Query().Get("key") != apiKey {
				http.Error(w, `{"error":"missing or invalid key"}`, http.StatusUnauthorized)
				return
			}
			mcpHandler.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

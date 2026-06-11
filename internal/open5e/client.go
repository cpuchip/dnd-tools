// Package open5e is a small client for the Open5e API (https://open5e.com) —
// the reference-data side of dnd-tools. Character STATE is ours; spells,
// creatures, items, and conditions are the SRD's, served by Open5e and
// cached locally in SQLite so repeat lookups are instant and offline.
//
// Rulesets: document__key srd-2024 (SRD 5.2, the 2024 rules — default) or
// srd-2014 (SRD 5.1, the 2014 rules), both CC-BY-4.0.
package open5e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Cache is the persistence the client needs (the store satisfies it).
type Cache interface {
	CacheGet(key string) (string, bool)
	CachePut(key, body string) error
}

// Client queries Open5e v2 with a read-through cache.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Cache   Cache
}

// New builds a client with sane defaults.
func New(cache Cache) *Client {
	return &Client{
		BaseURL: "https://api.open5e.com",
		HTTP:    &http.Client{Timeout: 20 * time.Second},
		Cache:   cache,
	}
}

// Types maps our public type names to Open5e v2 endpoints.
var Types = map[string]string{
	"creature":  "creatures",
	"spell":     "spells",
	"item":      "items",
	"condition": "conditions",
}

// TypeNames lists the accepted type values for tool descriptions.
func TypeNames() string { return "creature | spell | item | condition" }

// Rulesets maps accepted ruleset names to Open5e document keys. Empty means
// the default ruleset, SRD 5.2 (the 2024 rules).
var Rulesets = map[string]string{
	"":         "srd-2024",
	"srd-2024": "srd-2024", "2024": "srd-2024", "5.2": "srd-2024",
	"srd-2014": "srd-2014", "2014": "srd-2014", "5.1": "srd-2014",
}

// Result is one search hit, shape-tolerant across endpoints.
type Result struct {
	Key  string         `json:"key"`
	Name string         `json:"name"`
	Raw  map[string]any `json:"-"`
}

type listResponse struct {
	Count   int              `json:"count"`
	Results []map[string]any `json:"results"`
}

func (c *Client) get(ctx context.Context, path string, query url.Values) (string, error) {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	if body, ok := c.Cache.CacheGet(u); ok {
		return body, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("open5e unreachable (%w) — cached entries still work", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("open5e returned %s for %s", resp.Status, path)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if err := c.Cache.CachePut(u, string(b)); err != nil {
		return "", fmt.Errorf("cache write: %w", err)
	}
	return string(b), nil
}

// Search finds reference entries by name within a ruleset.
func (c *Client) Search(ctx context.Context, typ, query, ruleset string, limit int) ([]Result, error) {
	endpoint, ok := Types[strings.ToLower(strings.TrimSpace(typ))]
	if !ok {
		return nil, fmt.Errorf("unknown type %q — use %s", typ, TypeNames())
	}
	doc, ok := Rulesets[strings.ToLower(strings.TrimSpace(ruleset))]
	if !ok {
		return nil, fmt.Errorf("unknown ruleset %q — use srd-2024 (default) or srd-2014", ruleset)
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	q := url.Values{}
	q.Set("name__icontains", query)
	q.Set("document__key", doc)
	q.Set("limit", fmt.Sprint(limit))
	body, err := c.get(ctx, "/v2/"+endpoint+"/", q)
	if err != nil {
		return nil, err
	}
	var list listResponse
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return nil, fmt.Errorf("open5e response decode: %w", err)
	}
	out := make([]Result, 0, len(list.Results))
	for _, r := range list.Results {
		res := Result{Raw: r}
		res.Key, _ = r["key"].(string)
		res.Name, _ = r["name"].(string)
		out = append(out, res)
	}
	return out, nil
}

// Get fetches one entry by its Open5e key (e.g. "srd-2024_goblin-warrior").
func (c *Client) Get(ctx context.Context, typ, key string) (map[string]any, error) {
	endpoint, ok := Types[strings.ToLower(strings.TrimSpace(typ))]
	if !ok {
		return nil, fmt.Errorf("unknown type %q — use %s", typ, TypeNames())
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("key is required (from dnd_ref_search results)")
	}
	body, err := c.get(ctx, "/v2/"+endpoint+"/"+url.PathEscape(key)+"/", nil)
	if err != nil {
		return nil, err
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(body), &entry); err != nil {
		return nil, fmt.Errorf("open5e response decode: %w", err)
	}
	return entry, nil
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// callRequest builds a minimal MCP CallToolRequest carrying the given
// arguments. Handlers only read GetArguments under the hood, so this is enough.
func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text, res.IsError
}

// resetCache wipes the package-level LRU before each subtest so cached state
// from a previous case does not affect the current one.
func resetCache(t *testing.T) {
	t.Helper()
	prev := fetchCache
	fetchCache = newLRUCache(cacheDefaultMax, cacheDefaultTTL)
	t.Cleanup(func() { fetchCache = prev })
}

// localTestServer ALLOWs the SSRF guard to reach the loopback httptest server.
// The flag flips back via t.Setenv cleanup, and we also reset the cache so
// previous tests do not leak hits.
func localTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "1")
	resetCache(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleExtractMetadata(t *testing.T) {
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html>
<html><head>
<title>Hello world</title>
<meta name="description" content="demo page">
<meta property="og:title" content="OG Title">
<meta property="og:image" content="/img.png">
<meta name="twitter:card" content="summary">
<link rel="canonical" href="/canonical">
</head><body>hi</body></html>`))
	}))

	res, err := handleExtractMetadata(context.Background(), callRequest(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error result: %s", text)
	}
	var got metadataResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if got.Title != "Hello world" {
		t.Errorf("title: got %q", got.Title)
	}
	if got.Description != "demo page" {
		t.Errorf("description: got %q", got.Description)
	}
	if got.OpenGraph["title"] != "OG Title" || !strings.HasSuffix(got.OpenGraph["image"], "/img.png") {
		t.Errorf("open_graph: got %+v", got.OpenGraph)
	}
	if got.Twitter["card"] != "summary" {
		t.Errorf("twitter: got %+v", got.Twitter)
	}
	if !strings.HasSuffix(got.Canonical, "/canonical") {
		t.Errorf("canonical: got %q", got.Canonical)
	}
}

func TestHandleExtractTextSelector(t *testing.T) {
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>
<header>NAV NAV NAV</header>
<article id="content"><p>just the article</p></article>
<footer>FOOT</footer>
</body></html>`))
	}))

	res, _ := handleExtractText(context.Background(), callRequest(map[string]any{
		"url":      srv.URL,
		"selector": "#content",
	}))
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got textResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got.Text != "just the article" {
		t.Fatalf("expected only article text, got %q", got.Text)
	}
	if got.Selector != "#content" {
		t.Fatalf("selector echo: got %q", got.Selector)
	}
}

func TestHandleExtractTextSelectorNoMatch(t *testing.T) {
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>nothing here</body></html>`))
	}))
	res, _ := handleExtractText(context.Background(), callRequest(map[string]any{
		"url":      srv.URL,
		"selector": "#missing",
	}))
	text, isErr := resultText(t, res)
	if !isErr || !strings.Contains(text, "matched nothing") {
		t.Fatalf("expected selector-no-match error, got isErr=%v %q", isErr, text)
	}
}

func TestFetchCacheHit(t *testing.T) {
	var hits atomic.Int32
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`<html><body>once</body></html>`))
	}))

	first, err := handleFetchPage(context.Background(), callRequest(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	text1, _ := resultText(t, first)
	var r1 fetchResultJSON
	if err := json.Unmarshal([]byte(text1), &r1); err != nil {
		t.Fatalf("first not JSON: %v", err)
	}
	if r1.FromCache {
		t.Fatal("first call should not be from_cache")
	}

	second, _ := handleFetchPage(context.Background(), callRequest(map[string]any{"url": srv.URL}))
	text2, _ := resultText(t, second)
	var r2 fetchResultJSON
	if err := json.Unmarshal([]byte(text2), &r2); err != nil {
		t.Fatalf("second not JSON: %v", err)
	}
	if !r2.FromCache {
		t.Fatal("second call should be from_cache")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("expected exactly 1 server hit, got %d", got)
	}
}

func TestFetchNoCache(t *testing.T) {
	var hits atomic.Int32
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`<html><body>x</body></html>`))
	}))
	for i := 0; i < 2; i++ {
		_, err := handleFetchPage(context.Background(), callRequest(map[string]any{
			"url":      srv.URL,
			"no_cache": true,
		}))
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("no_cache should bypass cache; expected 2 hits, got %d", got)
	}
}

func TestFetchHeadersAndUserAgent(t *testing.T) {
	var capturedUA, capturedX atomic.Value
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA.Store(r.Header.Get("User-Agent"))
		capturedX.Store(r.Header.Get("X-Custom"))
		w.Write([]byte(`<html><body>ok</body></html>`))
	}))

	_, err := handleFetchPage(context.Background(), callRequest(map[string]any{
		"url":        srv.URL,
		"user_agent": "TestAgent/9.9",
		"headers": map[string]any{
			"X-Custom": "abc",
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if ua, _ := capturedUA.Load().(string); ua != "TestAgent/9.9" {
		t.Fatalf("user_agent: got %q", ua)
	}
	if x, _ := capturedX.Load().(string); x != "abc" {
		t.Fatalf("X-Custom header: got %q", x)
	}
}

func TestWaitSelectorRetries(t *testing.T) {
	var hits atomic.Int32
	srv := localTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 2 {
			w.Write([]byte(`<html><body><div id="loading">spinner</div></body></html>`))
			return
		}
		w.Write([]byte(`<html><body><div id="content">data</div></body></html>`))
	}))

	res, err := handleExtractText(context.Background(), callRequest(map[string]any{
		"url":              srv.URL,
		"selector":         "#content",
		"wait_selector":    "#content",
		"wait_attempts":    float64(3),
		"wait_interval_ms": float64(10),
		"no_cache":         true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := resultText(t, res)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got textResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got.Text != "data" {
		t.Fatalf("expected retry to land on populated page, got %q", got.Text)
	}
	if h := hits.Load(); h < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", h)
	}
}

func TestSSRFBlocksHandler(t *testing.T) {
	// Without ALLOW_PRIVATE, a loopback URL must be rejected before the dial.
	t.Setenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE", "")
	resetCache(t)
	res, _ := handleFetchPage(context.Background(), callRequest(map[string]any{
		"url": "http://127.0.0.1:1/",
	}))
	text, isErr := resultText(t, res)
	if !isErr {
		t.Fatalf("expected SSRF block, got success: %s", text)
	}
}

func TestStringMapArg(t *testing.T) {
	got := stringMapArg(callRequest(map[string]any{
		"headers": map[string]any{"A": "1", "B": 2, "C": "3"},
	}), "headers")
	if got["A"] != "1" || got["C"] != "3" {
		t.Fatalf("unexpected map: %+v", got)
	}
	if _, ok := got["B"]; ok {
		t.Fatal("non-string value B should be dropped")
	}
	if r := stringMapArg(callRequest(map[string]any{}), "headers"); r != nil {
		t.Fatalf("expected nil for missing arg, got %v", r)
	}
}

func TestParseFetchOptionsTimeoutCap(t *testing.T) {
	opts, err := parseFetchOptions(callRequest(map[string]any{
		"url":             "https://example.com/",
		"timeout_seconds": float64(999),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Timeout != 60*time.Second {
		t.Fatalf("expected 60s cap, got %v", opts.Timeout)
	}
}

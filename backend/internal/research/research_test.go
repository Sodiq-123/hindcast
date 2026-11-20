package research

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestCollectSources(t *testing.T) {
	raw := `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},
	"content":[
	 {"type":"server_tool_use","id":"s1","name":"web_search","input":{"query":"zcash"}},
	 {"type":"web_search_tool_result","tool_use_id":"s1","content":[{"type":"web_search_result","url":"https://z.cash/","title":"Zcash","encrypted_content":"x","page_age":""}]},
	 {"type":"server_tool_use","id":"f1","name":"web_fetch","input":{"url":"https://github.com/zcash/zcash"}},
	 {"type":"web_fetch_tool_result","tool_use_id":"f1","content":{"type":"web_fetch_result","url":"https://github.com/zcash/zcash","retrieved_at":"2026-10-06","content":{"type":"document","title":"zcash repo","source":{"type":"text","media_type":"text/plain","data":"hello"}}}},
	 {"type":"text","text":"Zcash is a privacy coin [https://z.cash/]. Supply is 21M [https://made.up/page].","citations":null}
	]}`
	var m anthropic.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	src := map[string]Source{}
	var notes strings.Builder
	collect(&m, src, &notes)
	if !src["https://z.cash/"].Retrieved || !src["https://github.com/zcash/zcash"].Retrieved {
		t.Errorf("search and fetch results must be marked retrieved: %+v", src)
	}
	if !strings.Contains(notes.String(), "privacy coin") {
		t.Error("notes missing text")
	}
	// A URL only cited in the text (never retrieved) is kept but not marked retrieved.
	for _, u := range urlRe.FindAllString(notes.String(), -1) {
		addSource(src, u, "", false)
	}
	if s, ok := src["https://made.up/page"]; !ok || s.Retrieved {
		t.Errorf("cited-only URL should be listed as not retrieved: %+v", s)
	}
}

func TestLastPage(t *testing.T) {
	link := `<https://api.github.com/repositories/1/commits?per_page=1&since=x&page=2>; rel="next", <https://api.github.com/repositories/1/commits?per_page=1&since=x&page=347>; rel="last"`
	if n := lastPage(link); n != 347 {
		t.Errorf("got %d", n)
	}
	if lastPage("") != -1 {
		t.Error("no link header")
	}
}

func TestSchemaAndHeadings(t *testing.T) {
	for _, k := range []string{"stock", "crypto", "token"} {
		b, err := json.Marshal(reportSchema(k))
		if err != nil || !strings.Contains(string(b), `"red_flags"`) {
			t.Errorf("%s schema: %v", k, err)
		}
	}
	if headings("stock")[0][0] != "business" || headings("token")[0][0] != "what_it_is" {
		t.Error("headings by kind")
	}
	if normalise(" zec-usd ") != "ZEC-USD" || normalise("pumpCmXqMfrsAkQ5r49WcJnRayYRqmXz6ae8H7H9Dfn") != "pumpCmXqMfrsAkQ5r49WcJnRayYRqmXz6ae8H7H9Dfn" {
		t.Error("normalise")
	}
}

func TestIdentityWarning(t *testing.T) {
	cases := []struct{ kind, facts, want string }{
		{"token", "Contract X on Solana.\nIdentity WARNING: not the CoinGecko-listed ZEC; the listed one is OmniBridge Bridged Zcash (Solana) at A7b.\n", "Not the listed ZEC"},
		{"token", "Contract Y on Ethereum.\nSafety check: Very high risk.\n", "Not listed on CoinGecko"},
		{"token", "Identity: CoinGecko lists this exact address as Bonk (rank #60).\n", ""},
		{"crypto", "CoinGecko profile (Zcash, rank #10)", ""},
		{"stock", "", ""},
	}
	for _, c := range cases {
		if got := (&Report{Kind: c.kind, Facts: c.facts}).IdentityWarning(); got != c.want {
			t.Errorf("%s %q: got %q, want %q", c.kind, c.facts, got, c.want)
		}
	}
}

func TestSchemaAsksForHeadline(t *testing.T) {
	sch := reportSchema("crypto")
	req := sch["required"].([]any)
	if req[0] != "headline" {
		t.Errorf("headline should be required first: %v", req)
	}
	if _, ok := sch["properties"].(map[string]any)["headline"]; !ok {
		t.Error("headline property missing")
	}
}

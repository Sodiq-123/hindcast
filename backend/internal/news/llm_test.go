package news

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Verifies the exact request we send (structured output, effort, fallbacks
// beta) and that a structured response is parsed and applied.
func TestAnalystRequestAndParse(t *testing.T) {
	var body map[string]any
	var beta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beta = r.Header.Get("anthropic-beta")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		out := `{"articles":[{"id":"abc","relevance":1.4,"direction":-0.7,"impact":"high","horizon":"days","reasoning":"Guidance cut lowers earnings."}],` +
			`"narrative":"Bearish near term [abc].","bull_case":["b"],"bear_case":["c"],"upcoming_catalysts":[],"key_risks":["r"]}`
		resp := map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
			"content":     []any{map[string]any{"type": "text", "text": out}},
			"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 1000000, "output_tokens": 100000},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := &Analyst{
		client: anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0)),
		model:  "claude-opus-5-5",
		effort: "high",
	}
	arts := []Article{{ID: "abc", Title: "X cuts guidance", Published: time.Now(), Scope: "asset"}}
	known := []Article{{ID: "old1", Title: "Earlier headline", Published: time.Now(), Relevance: 0.8, Direction: 0.3, Impact: "medium"}}
	an, usage, err := a.Analyze(context.Background(), AnalysisRequest{Symbol: "X", Name: "X Corp", QuantSummary: "summary", New: arts, Known: known})
	if err != nil {
		t.Fatal(err)
	}
	if body["fallbacks"] != "default" || !strings.Contains(beta, "server-side-fallback-2026-07-01") {
		t.Fatalf("fallback not configured: fallbacks=%v beta=%q", body["fallbacks"], beta)
	}
	oc, _ := body["output_config"].(map[string]any)
	if oc["effort"] != "high" || oc["format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("bad output_config: %v", oc)
	}
	// $4/MTok in + $20/MTok out for Opus 5.5: 1M in + 100k out = $6.
	if usage.CostUSD < 5.99 || usage.CostUSD > 6.01 {
		t.Fatalf("cost wrong: %+v", usage)
	}
	msgs, _ := body["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	judge, ctxPart, _ := strings.Cut(content, "ALREADY JUDGED")
	if !strings.Contains(judge, "id=abc") || strings.Contains(judge, "id=old1") || !strings.Contains(ctxPart, "id=old1") {
		t.Fatalf("known articles must be context only:\n%s", content)
	}
	an.Apply(arts)
	if arts[0].ScoredBy != "llm" || arts[0].Relevance != 1 || arts[0].Direction != -0.7 {
		t.Fatalf("judgment not applied/clamped: %+v", arts[0])
	}
}

// The batch path: submit, poll until ended, stream JSONL results, map them
// back to symbols, and price at the 50% batch rate.
func TestRunBatch(t *testing.T) {
	polls := 0
	var submitted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/messages/batches":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &submitted)
			_, _ = w.Write([]byte(`{"id":"msgbatch_1","type":"message_batch","processing_status":"in_progress"}`))
		case r.Method == "GET" && r.URL.Path == "/v1/messages/batches/msgbatch_1":
			polls++
			_, _ = w.Write([]byte(`{"id":"msgbatch_1","type":"message_batch","processing_status":"ended","results_url":"` + "http://" + r.Host + `/v1/messages/batches/msgbatch_1/results"}`))
		case r.URL.Path == "/v1/messages/batches/msgbatch_1/results":
			out := `{\"articles\":[],\"narrative\":\"ok\",\"bull_case\":[],\"bear_case\":[],\"upcoming_catalysts\":[],\"key_risks\":[]}`
			ok := `{"custom_id":"r0","result":{"type":"succeeded","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"` + out + `"}],"stop_reason":"end_turn","usage":{"input_tokens":1000000,"output_tokens":0}}}}`
			bad := `{"custom_id":"r1","result":{"type":"errored","error":{"type":"error","error":{"type":"api_error","message":"x"}}}}`
			w.Header().Set("Content-Type", "application/x-jsonl")
			_, _ = w.Write([]byte(ok + "\n" + bad + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Analyst{client: anthropic.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0)), model: "claude-opus-5-5", effort: "high"}
	out, err := a.RunBatch(context.Background(), map[string]AnalysisRequest{
		"^GSPC": {Symbol: "^GSPC"}, "AAPL": {Symbol: "AAPL"},
	}, 10*time.Millisecond, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reqs := submitted["requests"].([]any)
	if len(reqs) != 2 || reqs[0].(map[string]any)["custom_id"] != "r0" {
		t.Fatalf("bad submission: %v", submitted)
	}
	// Keys are sorted: r0 = "AAPL", r1 = "^GSPC".
	if o := out["AAPL"]; o.Err != nil || o.Analysis == nil || o.Analysis.Narrative != "ok" || o.Usage.CostUSD < 1.99 || o.Usage.CostUSD > 2.01 || !o.Usage.Batch {
		t.Fatalf("AAPL outcome wrong (want $2 = half of $4): %+v", o)
	}
	if out["^GSPC"].Err == nil {
		t.Fatal("errored request must surface an error")
	}
}

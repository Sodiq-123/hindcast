package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/plain"
)

func TestReportViewKeepsBothParts(t *testing.T) {
	b, err := json.Marshal(reportView{Report: &engine.Report{Symbol: "NVDA", Name: "NVIDIA Corporation"}, Plain: []plain.Answer{{Key: "1m", Say: "Too close to call."}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"symbol":"NVDA"`) || !strings.Contains(s, `"plain":[{"key":"1m"`) {
		t.Errorf("json = %.300s", s)
	}
}

package store

import (
	"context"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/research"
)

func TestResearchSubjectsSummariseLatest(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	save := func(r research.Report) {
		t.Helper()
		if err := st.SaveResearch(ctx, &r); err != nil {
			t.Fatal(err)
		}
	}
	save(research.Report{Subject: "ZEC-USD", Kind: "crypto", Name: "Zcash", Symbol: "ZEC-USD", CreatedAt: t0, Price: 1000, Fundamentals: "weak"})
	save(research.Report{Subject: "ZEC-USD", Kind: "crypto", Name: "Zcash", Symbol: "ZEC-USD", CreatedAt: t0.Add(24 * time.Hour), Price: 1370, Fundamentals: "mixed", Headline: "A real, working privacy coin."})
	save(research.Report{Subject: "9SWX", Kind: "token", Name: "ZEC", Symbol: "ZEC", CreatedAt: t0.Add(time.Hour), Price: 658,
		Fundamentals: "weak", Facts: "Identity WARNING: not the CoinGecko-listed ZEC; the listed one is Bridged Zcash at A7b.\n"})

	subs, err := st.ResearchSubjects(ctx)
	if err != nil || len(subs) != 2 {
		t.Fatalf("subjects = %+v, %v", subs, err)
	}
	z := subs[0]
	if z.Subject != "ZEC-USD" || z.Reports != 2 || z.Fundamentals != "mixed" || z.Price != 1370 || z.Headline == "" || z.Identity != "" {
		t.Errorf("latest Zcash summary wrong: %+v", z)
	}
	if tok := subs[1]; tok.Identity != "Not the listed ZEC" || tok.Kind != "token" {
		t.Errorf("token summary wrong: %+v", tok)
	}
}

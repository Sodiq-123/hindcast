package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sodiq/financial-market/backend/internal/research"
)

func (s *Store) SaveResearch(ctx context.Context, r *research.Report) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `INSERT INTO research_reports(subject, kind, name, created_at, price, cost_usd, body)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, r.Subject, r.Kind, r.Name, r.CreatedAt, r.Price, r.Cost.USD, body).Scan(&r.ID); err != nil {
		return err
	}
	return nil
}

// ResearchHistory returns a subject's reports, newest first.
func (s *Store) ResearchHistory(ctx context.Context, subject string, limit int) ([]*research.Report, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, body FROM research_reports WHERE subject = $1 ORDER BY id DESC LIMIT $2`, subject, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*research.Report
	for rows.Next() {
		var id int64
		var body []byte
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		var r research.Report
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		r.ID = id
		out = append(out, &r)
	}
	return out, rows.Err()
}

// ResearchReport loads one report by id.
func (s *Store) ResearchReport(ctx context.Context, id int64) (*research.Report, error) {
	var body []byte
	if err := s.db.QueryRowContext(ctx, `SELECT body FROM research_reports WHERE id = $1`, id).Scan(&body); err != nil {
		return nil, err
	}
	var r research.Report
	err := json.Unmarshal(body, &r)
	r.ID = id
	return &r, err
}

// ResearchSubject is one researched coin, token or stock, with a summary of
// its latest report.
type ResearchSubject struct {
	Subject      string    `json:"subject"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	Symbol       string    `json:"symbol"`
	Reports      int       `json:"reports"`
	Last         time.Time `json:"last"`
	LatestID     int64     `json:"latestId"`
	Fundamentals string    `json:"fundamentals"`
	Headline     string    `json:"headline,omitempty"`
	Price        float64   `json:"price"`              // when the latest report was made
	Identity     string    `json:"identity,omitempty"` // e.g. "Not the listed ZEC"
}

func (s *Store) ResearchSubjects(ctx context.Context) ([]ResearchSubject, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.subject, g.n, r.id, r.created_at, r.body
		FROM (SELECT subject, count(*) n, max(id) id FROM research_reports GROUP BY subject) g
		JOIN research_reports r ON r.id = g.id
		ORDER BY r.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResearchSubject{}
	for rows.Next() {
		var x ResearchSubject
		var body []byte
		if err := rows.Scan(&x.Subject, &x.Reports, &x.LatestID, &x.Last, &body); err != nil {
			return nil, err
		}
		var r research.Report
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		x.Kind, x.Name, x.Symbol, x.Fundamentals, x.Headline, x.Price, x.Identity = r.Kind, r.Name, r.Symbol, r.Fundamentals, r.Headline, r.Price, r.IdentityWarning()
		out = append(out, x)
	}
	return out, rows.Err()
}

// ResearchSpend is the Claude spend on research since a time (failed runs
// included, from the usage log).
func (s *Store) ResearchSpend(ctx context.Context, since time.Time) (float64, error) {
	var v float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0) FROM llm_usage WHERE symbol LIKE 'RESEARCH:%' AND at >= $1`, since).Scan(&v)
	return v, err
}

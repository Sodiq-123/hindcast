package api

import (
	"net/http"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/plain"
)

// reportView is a report plus its plain-words reading for this person.
type reportView struct {
	*engine.Report
	Plain []plain.Answer `json:"plain"`
}

func (s *Server) withPlain(r *http.Request, rep *engine.Report) reportView {
	level := "intermediate"
	if p, err := s.Store.Profile(r.Context(), uid(r)); err == nil {
		level = p.Settings().Level
	}
	return reportView{Report: rep, Plain: plain.Explain(rep, level)}
}

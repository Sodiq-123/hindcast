package api

import (
	"net/http"
	"slices"
	"time"

	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/today"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// today is the home screen: briefing, one suggested step, and ranges.
func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	ctx, id, now := r.Context(), uid(r), time.Now()
	syms, err := s.Store.Watchlist(ctx, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	prof, err := s.Store.Profile(ctx, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	set := prof.Settings()
	in := today.Input{Now: now, Level: set.Level, Horizon: set.Horizon, Profile: prof.Status}
	for _, sym := range syms {
		a := today.Asset{Symbol: sym}
		if rep, err := s.Store.LatestReport(ctx, sym); err == nil {
			a.Report = rep
		}
		in.Assets = append(in.Assets, a)
	}
	if ts, err := s.Store.WatchTokens(ctx, id); err == nil {
		for _, t := range ts {
			b := token.Brief{Address: t.Address, Chain: t.Chain, Symbol: t.Address[:6] + "…"}
			if rep, err := s.Store.LatestTokenReport(ctx, t.Address, 365*24*time.Hour); err == nil {
				b = rep.Brief()
			}
			in.Tokens = append(in.Tokens, b)
		}
	}
	if pos, err := s.Store.Positions(ctx, id); err == nil {
		for _, p := range pos {
			if p.ClosedAt == nil {
				in.Open = append(in.Open, today.Position{ID: p.ID, Symbol: p.Symbol, HasStop: p.Stop != nil})
			}
		}
	}
	states, err := s.Store.StepStates(ctx, id, today.DayStart(now))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	in.Steps = map[string]today.StepState{}
	for k, v := range states {
		in.Steps[k] = today.StepState{Done: v.Done, SkippedToday: v.SkippedToday}
	}
	if in.LastSeen, err = s.Store.MarkSeen(ctx, id, now, 30*time.Minute); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, today.Build(in))
}

// todayStep records what the person did with the suggestion: done, skip
// (not today) or undo (take back today's skip).
func (s *Server) todayStep(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID, Action string }
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" || len(req.ID) > 200 || !slices.Contains([]string{"done", "skip", "undo"}, req.Action) {
		writeErr(w, 400, "Send an id and an action: done, skip or undo.")
		return
	}
	if err := s.Store.StepAction(r.Context(), uid(r), req.ID, req.Action, today.DayStart(time.Now())); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) savePrefs(w http.ResponseWriter, r *http.Request) {
	var p store.Prefs
	if !decode(w, r, &p) {
		return
	}
	if !slices.Contains(store.Themes, p.Theme) || !slices.Contains(store.Accents, p.Accent) {
		writeErr(w, 400, "Pick a theme (auto, light or dark) and one of the listed accent colours.")
		return
	}
	if err := s.Store.SavePrefs(r.Context(), uid(r), p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

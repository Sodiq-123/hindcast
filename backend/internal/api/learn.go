package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sodiq/financial-market/backend/internal/learn"
	"github.com/sodiq/financial-market/backend/internal/store"
)

// learnEvent records something the person did (an asset visit, a watchlist
// view, the technical detail opened). Nothing is kept while learning is paused.
func (s *Server) learnEvent(w http.ResponseWriter, r *http.Request) {
	var req struct{ Kind, Subject string }
	if !decode(w, r, &req) {
		return
	}
	if err := s.Learn.Record(r.Context(), uid(r), req.Kind, req.Subject); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// learnSuggestion returns today's suggestion (null if there isn't one).
func (s *Server) learnSuggestion(w http.ResponseWriter, r *http.Request) {
	sh, err := s.Learn.Current(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"suggestion": sh})
}

func (s *Server) learnAnswer(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct{ Answer string }
	if !decode(w, r, &req) {
		return
	}
	sh, err := s.Learn.Answer(r.Context(), uid(r), id, req.Answer)
	switch {
	case errors.Is(err, learn.ErrAnswer):
		writeErr(w, 409, err.Error())
	case errors.Is(err, store.ErrWatchFull):
		writeErr(w, 409, err.Error())
	case err != nil:
		writeStoreErr(w, err)
	default:
		writeJSON(w, 200, sh)
	}
}

func (s *Server) learnOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.Learn.Overview(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, o)
}

func (s *Server) learnPrefs(w http.ResponseWriter, r *http.Request) {
	var p store.LearnPrefs
	if !decode(w, r, &p) {
		return
	}
	if err := s.Learn.SetPrefs(r.Context(), uid(r), p); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) learnForget(w http.ResponseWriter, r *http.Request) {
	var req struct{ What string }
	if !decode(w, r, &req) {
		return
	}
	if err := s.Learn.Forget(r.Context(), uid(r), req.What); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// learnExport downloads everything the app has learned about the person.
func (s *Server) learnExport(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), uid(r)
	o, err := s.Learn.Overview(ctx, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	evs, _ := s.Store.Events(ctx, id, time.Time{})
	rounds, _ := s.Learn.PracticeExport(ctx, id)
	w.Header().Set("Content-Disposition", `attachment; filename="hindcast-what-it-learned.json"`)
	writeJSON(w, 200, map[string]any{"exportedAt": time.Now().UTC(), "learned": o, "events": evs, "practice": rounds})
}

// learnReminders returns "Worth a look" lines for a trade being planned.
func (s *Server) learnReminders(w http.ResponseWriter, r *http.Request) {
	set, err := s.Learn.Reminders(r.Context(), uid(r), r.URL.Query().Get("symbol"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, set)
}

func (s *Server) learnReminderAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct{ Class, Answer, Title string }
	if !decode(w, r, &req) {
		return
	}
	if err := s.Learn.AnswerReminder(r.Context(), uid(r), req.Class, req.Answer, req.Title); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// practice returns the practice page: the current set of rounds, the round
// waiting for a decision, or the summary once the set is done.
func (s *Server) practice(w http.ResponseWriter, r *http.Request) {
	st, err := s.Learn.Practice(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

// practiceStart prepares a new set of rounds (or returns the unfinished one).
// Working out what the app said on each day is a past-day analysis, so a set
// counts as heavy work.
func (s *Server) practiceStart(w http.ResponseWriter, r *http.Request) {
	if !s.heavy(w, r) {
		return
	}
	st, err := s.Learn.StartPractice(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) practiceDecide(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Call         string
		Stop, Target float64
	}
	if !decode(w, r, &req) {
		return
	}
	rd, err := s.Learn.DecideRound(r.Context(), uid(r), id, req.Call, req.Stop, req.Target)
	switch {
	case errors.Is(err, learn.ErrPractice):
		writeErr(w, 409, err.Error())
	case err != nil:
		writeErr(w, 400, err.Error())
	default:
		writeJSON(w, 200, rd)
	}
}

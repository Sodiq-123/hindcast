package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/auth"
	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/profile"
	"github.com/sodiq/financial-market/backend/internal/store"
)

// ------------------------------------------------------------------- account

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req struct{ Name, Email, Password string }
	if !decode(w, r, &req) {
		return
	}
	if req.Email == "" {
		req.Email = u.Email
	}
	for _, err := range []error{checkEmail(req.Email), checkName(req.Name)} {
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	// Changing the sign-in email needs the password, so a borrowed session
	// can't take over the account.
	if store.NormEmail(req.Email) != u.Email && !s.passwordOK(r, req.Password) {
		writeErr(w, 403, "Enter your current password to change your email.")
		return
	}
	if err := s.Store.UpdateAccount(r.Context(), u.ID, req.Name, req.Email); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			writeErr(w, 409, err.Error())
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	nu, err := s.Store.User(r.Context(), u.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.writeMe(w, r, nu)
}

func (s *Server) passwordOK(r *http.Request, pw string) bool {
	if len(pw) > 4*auth.MaxPassword {
		return false
	}
	h, err := s.Store.PasswordHash(r.Context(), uid(r))
	return err == nil && auth.VerifyPassword(pw, h)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Current, New string }
	if !decode(w, r, &req) {
		return
	}
	key := "pw:" + strconv.FormatInt(uid(r), 10)
	if b, wait := s.Accounts.loginEmail.Blocked(key); b {
		tooMany(w, "Too many wrong passwords.", wait)
		return
	}
	if !s.passwordOK(r, req.Current) {
		s.Accounts.loginEmail.Add(key)
		writeErr(w, 403, "Your current password isn't right.")
		return
	}
	if err := auth.CheckPassword(req.New, user(r).Email); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Every other device is signed out; this one stays signed in.
	if err := s.Store.SetPassword(r.Context(), uid(r), hash, auth.HashToken(auth.SessionToken(r))); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	ss, err := s.Store.Sessions(r.Context(), uid(r), auth.HashToken(auth.SessionToken(r)))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ss)
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Store.RevokeSession(r.Context(), uid(r), id, auth.HashToken(auth.SessionToken(r))); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// exportData is everything stored about the person, as one JSON download.
func (s *Server) exportData(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), uid(r)
	p, err := s.Store.Profile(ctx, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	wl, _ := s.Store.Watchlist(ctx, id)
	toks, _ := s.Store.WatchTokens(ctx, id)
	pos, _ := s.Store.Positions(ctx, id)
	sess, _ := s.Store.Sessions(ctx, id, nil)
	prefs, _ := s.Store.Prefs(ctx, id)
	steps, _ := s.Store.Steps(ctx, id)
	learned, _ := s.Learn.Overview(ctx, id)
	events, _ := s.Store.Events(ctx, id, time.Time{})
	rounds, _ := s.Learn.PracticeExport(ctx, id)
	w.Header().Set("Content-Disposition", `attachment; filename="hindcast-my-data.json"`)
	writeJSON(w, 200, map[string]any{
		"exportedAt":  time.Now().UTC(),
		"account":     user(r),
		"profile":     p,
		"whatItDoes":  p.Effects(),
		"watchlist":   map[string]any{"assets": wl, "tokens": toks},
		"journal":     map[string]any{"settings": s.Journal.Settings(ctx, id), "positions": pos},
		"sessions":    sess,
		"look":        prefs,
		"suggestions": steps,
		"learned":     map[string]any{"overview": learned, "events": events, "practice": rounds},
	})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	if !s.passwordOK(r, req.Password) {
		writeErr(w, 403, "Your password isn't right.")
		return
	}
	if err := s.Store.DeleteUser(r.Context(), uid(r)); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			writeErr(w, 409, err.Error())
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	auth.ClearSessionCookie(w, s.Accounts.SecureCookie)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ------------------------------------------------------------------- profile

func (s *Server) quiz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, profile.Questions)
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.Profile(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"profile": p, "settings": p.Settings(), "effects": p.Effects()})
}

// saveProfile stores the answers. Quiz answers, when sent, are graded here
// and the right answers returned with explanations. Finishing (or skipping)
// setup with an empty watchlist fills it with a starter list.
func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), uid(r)
	var req struct {
		profile.Profile
		QuizAnswers map[string]int `json:"quizAnswers"`
	}
	if !decode(w, r, &req) {
		return
	}
	old, err := s.Store.Profile(ctx, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	p := req.Profile
	p.Quiz = old.Quiz
	if req.QuizAnswers != nil {
		if p.Quiz, err = profile.Grade(req.QuizAnswers); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if p.Status == "" {
		p.Status = old.Status
	}
	if err := p.Validate(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.Store.SaveProfile(ctx, id, &p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := map[string]any{"profile": p, "settings": p.Settings(), "effects": p.Effects()}
	if req.QuizAnswers != nil {
		out["correct"] = profile.Correct()
	}
	if p.Status != "" && old.Status == "" {
		wl, _ := s.Store.Watchlist(ctx, id)
		toks, _ := s.Store.WatchTokens(ctx, id)
		if len(wl) == 0 && len(toks) == 0 {
			var added []string
			for _, sym := range p.StarterWatchlist(s.Accounts.DefaultWatch) {
				if s.Store.AddWatch(ctx, id, sym) == nil {
					added = append(added, sym)
				}
			}
			out["starter"] = added
			s.analyseMissing(added)
		}
	}
	writeJSON(w, 200, out)
}

// analyseMissing runs a first analysis (without the AI analyst, so it costs
// nothing) for symbols nobody has analysed yet; the scheduled run adds the
// AI news reading later.
func (s *Server) analyseMissing(syms []string) {
	if s.Engine == nil {
		return
	}
	go func() {
		for _, sym := range syms {
			if _, err := s.Store.LatestReport(s.BaseCtx, sym); !errors.Is(err, store.ErrNotFound) {
				continue
			}
			rep, err := s.Engine.Analyze(s.BaseCtx, sym, engine.Options{})
			if err != nil {
				s.Log.Warn("starter analysis", "symbol", sym, "err", err)
				continue
			}
			if err := s.Store.SaveReport(s.BaseCtx, rep); err != nil {
				s.Log.Warn("save starter report", "symbol", sym, "err", err)
			}
		}
	}()
}

// --------------------------------------------------------------------- admin

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.Store.Users(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, us)
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	var err error
	switch {
	case req.Role != nil:
		if *req.Role != auth.RoleAdmin && *req.Role != auth.RoleMember {
			writeErr(w, 400, "role must be admin or member")
			return
		}
		err = s.Store.SetRole(r.Context(), id, *req.Role)
	case req.Disabled != nil:
		err = s.Store.SetDisabled(r.Context(), id, *req.Disabled)
	default:
		writeErr(w, 400, "nothing to change")
		return
	}
	s.adminResult(w, err)
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id == uid(r) {
		writeErr(w, 400, "delete your own account from the About you page")
		return
	}
	s.adminResult(w, s.Store.DeleteUser(r.Context(), id))
}

func (s *Server) adminResult(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		writeErr(w, 409, err.Error())
	case err != nil:
		writeStoreErr(w, err)
	default:
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

// adminResetLink makes a one-day reset link to pass on by hand (for servers
// without email).
func (s *Server) adminResetLink(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := s.Store.User(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	link, err := s.resetLink(r.Context(), id, 24*time.Hour)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"link": link})
}

func (s *Server) adminInvites(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Store.Invites(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, inv)
}

// adminCreateInvite returns the code once; only its hash is stored.
func (s *Server) adminCreateInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note    string `json:"note"`
		MaxUses int    `json:"maxUses"`
		Days    int    `json:"days"`
		Role    string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = auth.RoleMember
	}
	switch {
	case req.MaxUses < 1 || req.MaxUses > 1000:
		writeErr(w, 400, "uses must be between 1 and 1000")
		return
	case req.Days < 1 || req.Days > 90:
		writeErr(w, 400, "the invite must last between 1 and 90 days")
		return
	case req.Role != auth.RoleMember && req.Role != auth.RoleAdmin:
		writeErr(w, 400, "role must be admin or member")
		return
	case len(strings.TrimSpace(req.Note)) > 200:
		writeErr(w, 400, "keep the note under 200 characters")
		return
	}
	code, hash, err := auth.NewToken()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	id, err := s.Store.CreateInvite(r.Context(), hash, req.Note, req.Role, req.MaxUses, time.Now().AddDate(0, 0, req.Days), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "code": code, "link": s.Accounts.AppURL + "/signup?invite=" + code})
}

func (s *Server) adminRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.adminResult(w, s.Store.RevokeInvite(r.Context(), id))
}

// ----------------------------------------------------------------- allowances

// startOfMonth is the first instant of the current calendar month (UTC).
func startOfMonth() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// allowance reports how much of a member's allowance is used (admins have
// none: limit -1).
func (s *Server) allowance(r *http.Request, kind string) (used, limit int) {
	if user(r).Admin() {
		return 0, -1
	}
	since, limit := time.Now().Add(-24*time.Hour), s.Accounts.MemberAIRuns
	if kind == "research" {
		since, limit = startOfMonth(), s.Accounts.MemberResearch
	}
	used, _ = s.Store.ActionsSince(r.Context(), uid(r), kind, since)
	return used, limit
}

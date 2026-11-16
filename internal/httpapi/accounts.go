package httpapi

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
)

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.publicRegistration {
		s.problem(w, r, fault.Forbidden)
		return
	}
	in, err := decode[identity.RegistrationInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	_, raw, err := s.accounts.Register(identity.WithClient(r.Context(), r.UserAgent()), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	a, err := s.accounts.Authenticate(r.Context(), raw, identity.KindSession)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	s.setCookie(w, raw)
	respond(w, 201, sessionView(a))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	in, err := decode[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	// Account usernames have at most 48 characters; a profile login may be an
	// email address of up to 254.
	if len(in.Username) > 254 {
		s.problem(w, r, fault.Unauthenticated)
		return
	}
	// One client gets a small budget per account; the account-wide ceiling still bounds
	// guessing from many addresses without letting a single client lock the owner out.
	account := accountBucket("login-account", in.Username)
	if err := s.accounts.Limit(r.Context(), account+":"+remoteBucket(r), 12, time.Minute); err != nil {
		s.problem(w, r, err)
		return
	}
	if err := s.accounts.Limit(r.Context(), account, 120, time.Minute); err != nil {
		s.problem(w, r, err)
		return
	}
	_, raw, err := s.accounts.Login(identity.WithClient(r.Context(), r.UserAgent()), in.Username, in.Password)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	a, err := s.accounts.Authenticate(r.Context(), raw, identity.KindSession)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	s.setCookie(w, raw)
	respond(w, 200, sessionView(a))
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, err := s.accounts.VisibleUser(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, u)
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, sessionView(actor(r)))
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.accounts.Logout(r.Context(), actor(r)); err != nil {
		s.problem(w, r, err)
		return
	}
	s.setCookie(w, "")
	w.WriteHeader(204)
}
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	in, err := decode[identity.UserPatch](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	u, err := s.accounts.UpdateUser(r.Context(), actor(r), actor(r).User.ID, in, true)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, u)
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !s.passwordLimit(w, r) {
		return
	}
	in, err := decode[struct {
		CurrentPassword string `json:"current_password"`
		Password        string `json:"password"`
	}](w, r)
	if err == nil {
		err = s.accounts.ChangePassword(r.Context(), actor(r), in.CurrentPassword, in.Password)
	}
	if err != nil {
		s.problem(w, r, err)
		return
	}
	s.setCookie(w, "")
	w.WriteHeader(204)
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	v, err := s.accounts.Sessions(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": v})
}
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "credentialID")
	if err := s.accounts.RevokeSession(r.Context(), actor(r), id); err != nil {
		s.problem(w, r, err)
		return
	}
	if id == actor(r).CredentialID {
		s.setCookie(w, "")
	}
	w.WriteHeader(204)
}
func (s *Server) tokens(w http.ResponseWriter, r *http.Request) {
	v, err := s.accounts.Tokens(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": v})
}
func (s *Server) mintToken(w http.ResponseWriter, r *http.Request) {
	if !s.accountLimit(w, r, "token-create", 6) || !s.passwordLimit(w, r) {
		return
	}
	in, err := decode[identity.TokenInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	t, raw, err := s.accounts.MintToken(r.Context(), actor(r), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, map[string]any{"token": t, "secret": raw})
}
func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.accounts.RevokeToken(r.Context(), actor(r), chi.URLParam(r, "credentialID")); err != nil {
		s.problem(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func queryLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 100 {
		return 0, fault.Invalid("Page size must be between 1 and 100.")
	}
	return n, nil
}
func userID(r *http.Request) (int64, error) { return identity.ParseID(chi.URLParam(r, "userID")) }
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	q := r.URL.Query()
	out, err := s.accounts.Users(r.Context(), actor(r), identity.UserFilter{Query: q.Get("q"), Role: q.Get("role"), Status: q.Get("status"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	id, err := userID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	u, err := s.accounts.User(r.Context(), actor(r), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, u)
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := userID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[identity.UserPatch](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	u, err := s.accounts.UpdateUser(r.Context(), actor(r), id, in, false)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, u)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if !s.accountLimit(w, r, "user-create", 20) {
		return
	}
	in, err := decode[identity.CreateUserInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	u, i, raw, err := s.accounts.CreateUser(r.Context(), actor(r), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, map[string]any{"user": u, "invitation": i, "claim_url": s.origin + "/join#" + raw})
}
func (s *Server) resetLink(w http.ResponseWriter, r *http.Request) {
	if !s.accountLimit(w, r, "reset-link", 10) {
		return
	}
	id, err := userID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	i, raw, err := s.accounts.ResetLink(r.Context(), actor(r), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, map[string]any{"invitation": i, "claim_url": s.origin + "/join#" + raw})
}
func (s *Server) revokeAccess(w http.ResponseWriter, r *http.Request) {
	id, err := userID(r)
	if err == nil {
		err = s.accounts.RevokeUserAccess(r.Context(), actor(r), id)
	}
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if id == actor(r).User.ID {
		s.setCookie(w, "")
	}
	w.WriteHeader(204)
}
func (s *Server) invitations(w http.ResponseWriter, r *http.Request) {
	out, err := s.accounts.Invitations(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": out})
}
func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	if !s.accountLimit(w, r, "invite-create", 20) {
		return
	}
	in, err := decode[identity.InviteInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	i, raw, err := s.accounts.Invite(r.Context(), actor(r), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, map[string]any{"invitation": i, "claim_url": s.origin + "/join#" + raw})
}
func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	if err := s.accounts.RevokeInvitation(r.Context(), actor(r), chi.URLParam(r, "invitationID")); err != nil {
		s.problem(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) previewInvite(w http.ResponseWriter, r *http.Request) {
	in, err := decode[struct {
		Token string `json:"token"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	v, err := s.accounts.PreviewInvite(r.Context(), in.Token)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, v)
}
func (s *Server) claimInvite(w http.ResponseWriter, r *http.Request) {
	in, err := decode[struct {
		Token       string `json:"token"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	u, err := s.accounts.Claim(r.Context(), in.Token, in.Username, in.DisplayName, in.Password)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, u)
}
func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.accounts.AuditLog(r.Context(), actor(r), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

// accountBucket names a rate budget for a submitted account name. Canonical names
// share it across case and spacing without storing the submitted name.
func accountBucket(label, username string) string {
	return fmt.Sprintf("%s:%x", label, sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username)))))
}

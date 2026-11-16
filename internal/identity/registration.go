package identity

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"strconv"
	"strings"
)

type RegistrationInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}

// Register creates only a member and its session in one transaction. Contact
// email is not verified identity and never links another account automatically.
func (s *Store) Register(ctx context.Context, in RegistrationInput) (User, string, error) {
	username, err := ValidateUsername(in.Username)
	if err != nil {
		return User{}, "", err
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.DisplayName == "" {
		in.DisplayName = username
	}
	if len([]rune(in.DisplayName)) > 80 {
		return User{}, "", fault.Invalid("Display name is too long.")
	}
	in.Email = strings.TrimSpace(in.Email)
	if in.Email == "" {
		return User{}, "", fault.Invalid("Enter an email address.")
	}
	if err = ValidateEmail(in.Email); err != nil {
		return User{}, "", err
	}
	hash, err := s.Passwords.Hash(ctx, in.Password)
	if err != nil {
		return User{}, "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", accountLock); err != nil {
		return User{}, "", err
	}
	// Public registration must not win the one-time bootstrap race on an empty instance.
	var initialized bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE role='owner' AND active)").Scan(&initialized); err != nil {
		return User{}, "", err
	}
	if !initialized {
		return User{}, "", fault.Forbidden
	}
	var id, epoch int64
	if err = tx.QueryRow(ctx, `INSERT INTO users(username,display_name,email,password_hash,role) VALUES($1,$2,$3,$4,'member') RETURNING id,auth_epoch`, username, in.DisplayName, in.Email, hash).Scan(&id, &epoch); err != nil {
		return User{}, "", dbError(err)
	}
	u, raw, err := issueSession(ctx, tx, id, epoch)
	if err != nil {
		return User{}, "", err
	}
	if err = Audit(ctx, tx, Actor{User: u}, "user.registered", "user", strconv.FormatInt(id, 10), map[string]any{"role": "member"}); err != nil {
		return User{}, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, "", err
	}
	return u, raw, nil
}

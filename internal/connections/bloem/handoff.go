package bloem

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

type HandoffSettings struct {
	Enabled bool   `json:"enabled"`
	Label   string `json:"label"`
	BaseURL string `json:"base_url"`
}
type HandoffTarget struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

func validHandoffOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(raw, "#") && len(raw) <= 500 && utf8.ValidString(raw) && !strings.ContainsFunc(raw, unicode.IsControl)
}
func handoffTarget(settings HandoffSettings, contentID string) *HandoffTarget {
	if !settings.Enabled || !validHandoffOrigin(settings.BaseURL) || contentID == "" {
		return nil
	}
	return &HandoffTarget{URL: strings.TrimSuffix(settings.BaseURL, "/") + "/item/" + url.PathEscape(contentID), Label: settings.Label}
}
func (s Store) SetHandoffSettings(ctx context.Context, a identity.Actor, instance string, in HandoffSettings) (HandoffSettings, error) {
	tx, fresh, err := s.adminTx(ctx, a)
	if err != nil {
		return HandoffSettings{}, err
	}
	defer tx.Rollback(ctx)
	if !validHandoffOrigin(in.BaseURL) || strings.TrimSpace(in.Label) == "" || utf8.RuneCountInString(in.Label) > 100 || !utf8.ValidString(in.Label) || strings.ContainsFunc(in.Label, unicode.IsControl) {
		return HandoffSettings{}, fault.Invalid("Enter a playback label and an HTTP or HTTPS origin.")
	}
	result, err := tx.Exec(ctx, `UPDATE bloem_instances SET handoff_enabled=$2,handoff_label=$3,handoff_base_url=$4 WHERE id=$1`, instance, in.Enabled, in.Label, in.BaseURL)
	if err != nil {
		return HandoffSettings{}, err
	}
	if result.RowsAffected() != 1 {
		return HandoffSettings{}, fault.NotFound
	}
	if err = identity.Audit(ctx, tx, fresh, "bloem.instance.handoff", "bloem_instance", instance, map[string]any{"enabled": in.Enabled, "label": in.Label, "base_url": in.BaseURL}); err != nil {
		return HandoffSettings{}, err
	}
	return in, tx.Commit(ctx)
}
func (s Store) HandoffSettings(ctx context.Context, a identity.Actor, instance string) (HandoffSettings, error) {
	tx, _, err := s.adminTx(ctx, a)
	if err != nil {
		return HandoffSettings{}, err
	}
	defer tx.Rollback(ctx)
	var out HandoffSettings
	err = tx.QueryRow(ctx, `SELECT handoff_enabled,handoff_label,COALESCE(handoff_base_url,url) FROM bloem_instances WHERE id=$1`, instance).Scan(&out.Enabled, &out.Label, &out.BaseURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	return out, err
}

// CalendarHandoffs consumes already authorized source rows, with no request-selected instance.
func (s Store) CalendarHandoffs(ctx context.Context, events []CalendarEvent) (map[string]HandoffSettings, error) {
	selected := []string{}
	seen := map[string]bool{}
	for _, event := range events {
		if !seen[event.InstanceID] {
			seen[event.InstanceID] = true
			selected = append(selected, event.InstanceID)
		}
	}
	out := map[string]HandoffSettings{}
	if len(selected) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,handoff_enabled,handoff_label,COALESCE(handoff_base_url,url) FROM bloem_instances WHERE id=ANY($1::text[]) AND active`, selected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var settings HandoffSettings
		if err = rows.Scan(&id, &settings.Enabled, &settings.Label, &settings.BaseURL); err != nil {
			return nil, err
		}
		out[id] = settings
	}
	return out, rows.Err()
}
func CalendarHandoff(settings HandoffSettings, contentID string) *HandoffTarget {
	return handoffTarget(settings, contentID)
}

type HandoffInstance struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

func (s Store) HandoffInstances(ctx context.Context, a identity.Actor) ([]HandoffInstance, error) {
	tx, _, err := s.adminTx(ctx, a)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,url,active FROM bloem_instances ORDER BY id LIMIT 101`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HandoffInstance{}
	for rows.Next() {
		var instance HandoffInstance
		if err = rows.Scan(&instance.ID, &instance.URL, &instance.Active); err != nil {
			return nil, err
		}
		out = append(out, instance)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 100 {
		return nil, fault.Invalid("Too many Silo instances to display.")
	}
	return out, nil
}

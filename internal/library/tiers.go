package library

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// Tier is one labelled rank of a list's tier board, with its members in board order.
type Tier struct {
	ID       int64    `json:"id,string"`
	Label    string   `json:"label"`
	Color    *string  `json:"color"`
	EntryIDs []string `json:"entry_ids"`
}

// TierBoard is a manual list arranged into tiers; members in no tier are unranked.
type TierBoard struct {
	Version  int      `json:"version"`
	Tiers    []Tier   `json:"tiers"`
	Unranked []string `json:"unranked"`
}

// TierInput replaces a list's whole board.
type TierInput struct {
	Version int `json:"version"`
	Tiers   []struct {
		Label    string   `json:"label"`
		Color    *string  `json:"color"`
		EntryIDs []string `json:"entry_ids"`
	} `json:"tiers"`
}

var tierColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// Tiers reads a manual list's board. Members keep the list's manual order when unranked.
func (s Store) Tiers(ctx context.Context, a identity.Actor, listID int64) (TierBoard, error) {
	out := TierBoard{Tiers: []Tier{}, Unranked: []string{}}
	if !a.Allows("lists:read") {
		return out, fault.Forbidden
	}
	var kind string
	err := s.Pool.QueryRow(ctx, "SELECT kind,version FROM personal_lists WHERE profile_id=$1 AND id=$2", a.TrackingID(), listID).Scan(&kind, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	if err != nil {
		return out, err
	}
	if kind != "manual" {
		return out, fault.Invalid("Only manual lists have a tier board.")
	}
	rows, err := s.Pool.Query(ctx, "SELECT id,label,color FROM list_tiers WHERE profile_id=$1 AND list_id=$2 ORDER BY position", a.TrackingID(), listID)
	if err != nil {
		return out, err
	}
	index := map[int64]int{}
	for rows.Next() {
		var t Tier
		if err = rows.Scan(&t.ID, &t.Label, &t.Color); err != nil {
			rows.Close()
			return out, err
		}
		t.EntryIDs = []string{}
		index[t.ID] = len(out.Tiers)
		out.Tiers = append(out.Tiers, t)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT entry_id,tier_id FROM list_entries WHERE profile_id=$1 AND list_id=$2
		ORDER BY tier_id IS NULL, tier_position, position, entry_id`, a.TrackingID(), listID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry int64
		var tier *int64
		if err = rows.Scan(&entry, &tier); err != nil {
			return out, err
		}
		id := strconv.FormatInt(entry, 10)
		if i, ok := tierIndex(index, tier); ok {
			out.Tiers[i].EntryIDs = append(out.Tiers[i].EntryIDs, id)
		} else {
			out.Unranked = append(out.Unranked, id)
		}
	}
	return out, rows.Err()
}

func tierIndex(index map[int64]int, tier *int64) (int, bool) {
	if tier == nil {
		return 0, false
	}
	i, ok := index[*tier]
	return i, ok
}

// SaveTiers replaces the board: the named tiers in order, each with its members in
// order. Members not named in any tier become unranked. The list's manual order is
// untouched.
func (s Store) SaveTiers(ctx context.Context, a identity.Actor, listID int64, key string, in TierInput) (Mutation, error) {
	if len(in.Tiers) > 20 {
		return Mutation{}, fault.Invalid("A board has at most 20 tiers.")
	}
	type tier struct {
		label   string
		color   *string
		entries []int64
	}
	tiers := make([]tier, 0, len(in.Tiers))
	seen := map[int64]bool{}
	for _, t := range in.Tiers {
		label := strings.TrimSpace(t.Label)
		if n := len([]rune(label)); n < 1 || n > 40 {
			return Mutation{}, fault.Invalid("Give every tier a label of 1 to 40 characters.")
		}
		if t.Color != nil {
			c := strings.ToLower(*t.Color)
			if !tierColor.MatchString(c) {
				return Mutation{}, fault.Invalid("A tier color is a #rrggbb value.")
			}
			t.Color = &c
		}
		var ids []int64
		if len(t.EntryIDs) > 0 {
			var err error
			if ids, err = parseIDs(t.EntryIDs, 5000, "tier entries"); err != nil {
				return Mutation{}, err
			}
		}
		for _, id := range ids {
			if seen[id] {
				return Mutation{}, fault.Invalid("Place each list member in at most one tier.")
			}
			seen[id] = true
		}
		tiers = append(tiers, tier{label, t.Color, ids})
	}
	return s.mutate(ctx, a, "lists:write", key, "list.tiers/"+strconv.FormatInt(listID, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		if err := checkList(ctx, tx, a, listID, in.Version); err != nil {
			return Mutation{}, err
		}
		var kind string
		if err := tx.QueryRow(ctx, "SELECT kind FROM personal_lists WHERE id=$1", listID).Scan(&kind); err != nil {
			return Mutation{}, err
		}
		if kind != "manual" {
			return Mutation{}, fault.Invalid("Only manual lists have a tier board.")
		}
		named := make([]int64, 0, len(seen))
		for id := range seen {
			named = append(named, id)
		}
		var members int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM list_entries WHERE list_id=$1 AND entry_id=ANY($2)", listID, named).Scan(&members); err != nil {
			return Mutation{}, err
		}
		if members != len(named) {
			return Mutation{}, fault.Invalid("Place only members of this list in its tiers.")
		}
		if _, err := tx.Exec(ctx, "DELETE FROM list_tiers WHERE profile_id=$1 AND list_id=$2", a.TrackingID(), listID); err != nil {
			return Mutation{}, err
		}
		if _, err := tx.Exec(ctx, "UPDATE list_entries SET tier_id=NULL,tier_position=NULL WHERE list_id=$1", listID); err != nil {
			return Mutation{}, err
		}
		for i, t := range tiers {
			var id int64
			if err := tx.QueryRow(ctx, "INSERT INTO list_tiers(profile_id,list_id,position,label,color) VALUES($1,$2,$3,$4,$5) RETURNING id", a.TrackingID(), listID, i+1, t.label, t.color).Scan(&id); err != nil {
				return Mutation{}, err
			}
			if len(t.entries) == 0 {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE list_entries l SET tier_id=$2,tier_position=o.n FROM unnest($3::bigint[]) WITH ORDINALITY o(id,n)
				WHERE l.list_id=$1 AND l.entry_id=o.id`, listID, id, t.entries); err != nil {
				return Mutation{}, err
			}
		}
		out := Mutation{ID: listID}
		err := tx.QueryRow(ctx, "UPDATE personal_lists SET version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version", a.TrackingID(), listID).Scan(&out.Version)
		return out, err
	})
}

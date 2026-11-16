package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

func (s Store) snapshotList(ctx context.Context, tx pgx.Tx, userID, sourceID, destID int64) error {
	var kind, sortName string
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT kind,sort,rules FROM personal_lists WHERE profile_id=$1 AND id=$2 FOR SHARE", userID, sourceID).Scan(&kind, &sortName, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	if err != nil {
		return err
	}
	f := Filter{Sort: sortName}
	join := ""
	note := "''"
	manual := kind == "manual"
	if manual {
		f.ListID = sourceID
		join = " JOIN list_entries le ON le.profile_id=e.profile_id AND le.entry_id=e.id"
		note = "le.note"
	} else {
		var rules Rules
		if err = json.Unmarshal(raw, &rules); err != nil {
			return err
		}
		if err = rules.apply(&f); err != nil {
			return err
		}
	}
	if err = f.normalize(); err != nil {
		return err
	}
	order, ok := sorts[f.Sort]
	if !ok {
		return fault.Invalid("Unsupported sort order.")
	}
	dir := " DESC"
	if order.asc {
		dir = " ASC"
	}
	where, args := entryWhere(f, userID, manual)
	args = append(args, destID)
	_, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO list_entries(profile_id,list_id,entry_id,position,note)
 SELECT e.profile_id,$%d,e.id,row_number() OVER(ORDER BY %s%s,e.id%s),%s`, len(args), order.expr, dir, dir, note)+from+join+where, args...)
	return err
}

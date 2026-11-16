package catalog

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
)

// EpisodeOrderSummary names an alternate order of a show's episodes.
type EpisodeOrderSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type EpisodeOrderGroup struct {
	Number   int    `json:"number"`
	Name     string `json:"name"`
	Episodes []Item `json:"episodes"`
}

type EpisodeOrderDetail struct {
	EpisodeOrderSummary
	Groups []EpisodeOrderGroup `json:"groups"`
}

func (s *Service) orderer(show Item) (provider.Orderer, error) {
	if show.Kind != "series" || show.ParentID != nil {
		return nil, fault.Invalid("Episode orders apply to shows.")
	}
	o, ok := s.Providers[show.Provider].(provider.Orderer)
	if !ok {
		return nil, nil
	}
	return o, nil
}

// EpisodeOrders lists a show's alternate orders, cached for a day.
func (s *Service) EpisodeOrders(ctx context.Context, showID int64) ([]EpisodeOrderSummary, error) {
	show, err := s.Get(ctx, showID)
	if err != nil {
		return nil, err
	}
	o, err := s.orderer(show)
	if err != nil || o == nil {
		return []EpisodeOrderSummary{}, err
	}
	found, err := cached(s, ctx, "catalog:orders:v1:"+show.Provider+":"+show.ProviderID, 24*time.Hour, func(ctx context.Context) ([]provider.EpisodeOrder, error) {
		return o.EpisodeOrders(ctx, show.ProviderID)
	})
	if err != nil {
		return nil, err
	}
	out := make([]EpisodeOrderSummary, 0, len(found))
	for _, f := range found {
		out = append(out, EpisodeOrderSummary{ID: show.Provider + ":" + f.ID, Name: f.Name, Kind: f.Kind})
	}
	return out, nil
}

// EpisodeOrder loads an order with its episodes as catalog records. Orders are stored
// so the next episode can follow them, and refreshed weekly.
func (s *Service) EpisodeOrder(ctx context.Context, showID int64, orderID string) (EpisodeOrderDetail, error) {
	var out EpisodeOrderDetail
	show, err := s.Get(ctx, showID)
	if err != nil {
		return out, err
	}
	o, err := s.orderer(show)
	if err != nil {
		return out, err
	}
	providerID, ok := strings.CutPrefix(orderID, show.Provider+":")
	if o == nil || !ok || providerID == "" {
		return out, fault.NotFound
	}
	var stale bool
	err = s.Pool.QueryRow(ctx, "SELECT name,kind,refreshed_at<clock_timestamp()-interval '7 days' FROM episode_orders WHERE id=$1 AND show_id=$2", orderID, showID).Scan(&out.Name, &out.Kind, &stale)
	if errors.Is(err, pgx.ErrNoRows) || stale {
		if err = s.storeOrder(ctx, o, show, orderID, providerID); err != nil {
			return out, err
		}
		err = s.Pool.QueryRow(ctx, "SELECT name,kind FROM episode_orders WHERE id=$1", orderID).Scan(&out.Name, &out.Kind)
	}
	if err != nil {
		return out, err
	}
	out.ID, out.Groups = orderID, []EpisodeOrderGroup{}
	rows, err := s.Pool.Query(ctx, "SELECT o.group_number,o.group_name,"+prefixed("c.", itemColumns)+" FROM episode_order_items o JOIN catalog_items c ON c.id=o.catalog_item_id WHERE o.order_id=$1 ORDER BY o.position", orderID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var number int
		var name string
		it, err := scanItem(prefixRow{rows, &number, &name})
		if err != nil {
			return out, err
		}
		if len(out.Groups) == 0 || out.Groups[len(out.Groups)-1].Number != number {
			out.Groups = append(out.Groups, EpisodeOrderGroup{Number: number, Name: name, Episodes: []Item{}})
		}
		g := &out.Groups[len(out.Groups)-1]
		g.Episodes = append(g.Episodes, it)
	}
	return out, rows.Err()
}

func (s *Service) storeOrder(ctx context.Context, o provider.Orderer, show Item, orderID, providerID string) error {
	orders, err := s.EpisodeOrders(ctx, show.ID)
	if err != nil {
		return err
	}
	belongs := false
	for _, candidate := range orders {
		if candidate.ID == orderID {
			belongs = true
			break
		}
	}
	if !belongs {
		return fault.NotFound
	}
	fetchCtx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
	order, err := o.EpisodeOrder(fetchCtx, providerID)
	cancel()
	if err != nil {
		return providerFault(err)
	}
	// Every season must be loaded so the order's episodes exist in the catalog.
	if _, err = s.EpisodeMap(ctx, show.ID); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	name := order.Name
	if name == "" {
		name = "Alternate order"
	}
	tag, err := tx.Exec(ctx, `INSERT INTO episode_orders(id,show_id,name,kind) VALUES($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name,kind=EXCLUDED.kind,refreshed_at=clock_timestamp() WHERE episode_orders.show_id=EXCLUDED.show_id`, orderID, show.ID, name, order.Kind)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.NotFound
	}
	if _, err = tx.Exec(ctx, "DELETE FROM episode_order_items WHERE order_id=$1", orderID); err != nil {
		return err
	}
	position := 0
	for i, g := range order.Groups {
		groupName := g.Name
		if groupName == "" {
			groupName = "Part " + itoa(i+1)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO episode_order_items(order_id,catalog_item_id,group_number,group_name,position)
			SELECT $1,c.id,$3,$4,$5+o.n FROM unnest($6::text[]) WITH ORDINALITY o(pid,n)
			JOIN catalog_items c ON c.root_id=$2 AND c.kind='episode' AND c.provider_id=o.pid
			ON CONFLICT DO NOTHING`, orderID, show.ID, i+1, groupName, position, g.Episodes); err != nil {
			return err
		}
		position += len(g.Episodes)
	}
	if _, err = tx.Exec(ctx, library.NextEpisodeSQL+"e.catalog_item_id=$1 AND e.episode_order=$2", show.ID, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func itoa(n int) string { return strconv.Itoa(n) }

// prefixed qualifies each column of a column list with a table alias.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + strings.TrimSpace(p)
	}
	return strings.Join(parts, ",")
}

// prefixRow scans leading extra columns before the item's own.
type prefixRow struct {
	row   pgx.Row
	first *int
	name  *string
}

func (p prefixRow) Scan(dest ...any) error {
	return p.row.Scan(append([]any{p.first, p.name}, dest...)...)
}

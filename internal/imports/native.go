package imports

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/transfer"
	"github.com/jackc/pgx/v5"
	"io"
)

func (s Store) nativeReference(ctx context.Context, r transfer.Reference) (int64, error) {
	lookup := func() (int64, error) {
		var id int64
		err := s.Pool.QueryRow(ctx, "SELECT id FROM catalog_items WHERE provider=$1 AND kind=$2 AND provider_id=$3", r.Provider, r.Kind, r.ProviderID).Scan(&id)
		return id, err
	}
	if id, err := lookup(); err == nil {
		return id, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if s.Catalog == nil {
		return 0, fault.Invalid("A catalog reference is unavailable on this server.")
	}
	root, err := s.Catalog.Resolve(ctx, r.RootProvider, r.RootKind, r.RootID)
	if err != nil {
		return 0, err
	}
	if r.Kind == "episode" && r.Season != nil {
		children, e := s.Catalog.Children(ctx, root.ID)
		if e != nil {
			return 0, e
		}
		for _, child := range children {
			if child.Kind == "season" && child.SeasonNumber != nil && *child.SeasonNumber == *r.Season {
				if _, e = s.Catalog.Children(ctx, child.ID); e != nil {
					return 0, e
				}
			}
		}
	}
	id, err := lookup()
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fault.Invalid("A catalog reference is unavailable on this server.")
	}
	return id, err
}

func nativeBytes(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, transfer.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(out) > transfer.MaxBytes {
		return nil, fault.Invalid("Native snapshot exceeds 64 MiB.")
	}
	return out, nil
}

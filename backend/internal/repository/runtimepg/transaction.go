package runtimepg

import (
	"context"

	"agent-platform/backend/internal/model/runtimev1"
	"agent-platform/backend/internal/runtimeports"
	"github.com/jackc/pgx/v5"
)

// OwnerTransaction holds the original authorization and local execution guard
// for a short business commit. The callback must not perform network work.
func (s *Store) OwnerTransaction(ctx context.Context, ref runtimev1.ExecRef, cap, target string, body func(pgx.Tx, runtimeports.Execution) error) error {
	tx, err := s.tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	info, err := s.authorize(ctx, tx, ref, cap, target)
	if err != nil {
		return err
	}
	if err = body(tx, info); err != nil {
		return err
	}
	if _, err = s.authorize(ctx, tx, ref, cap, target); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

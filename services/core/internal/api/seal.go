package api

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

// SealUnsealed seals attempts written before rows carried a seal (migration
// 0008). It runs as the schema owner, across tenants, once per migrate: each
// attempt whose chain still checks against its row is sealed as it stands;
// one that does not is left unsealed, and reads as altered. Returns how many
// were sealed and how many were left.
func SealUnsealed(ctx context.Context, owner *pgx.Conn, keys *secure.Keys) (sealed, left int, err error) {
	s := &Server{Keys: keys}
	err = pgx.BeginFunc(ctx, owner, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+attemptColumns+` FROM attempts WHERE seal IS NULL FOR UPDATE`)
		if err != nil {
			return err
		}
		atts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*attemptRow, error) { return scanAttempt(r) })
		if err != nil {
			return err
		}
		for _, a := range atts {
			a.Seal = s.seal(a)
			_, intact, _, err := s.loadEvents(ctx, tx, a)
			if err != nil {
				return err
			}
			if !intact {
				left++
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE attempts SET seal = $2 WHERE id = $1`, a.ID, a.Seal); err != nil {
				return err
			}
			sealed++
		}
		return nil
	})
	return sealed, left, err
}

package api

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
)

const sealPage = 1000

// SealUnsealed seals attempts written before rows carried a seal (migration
// 0008). It runs as the schema owner, across tenants, once per migrate: each
// attempt whose chain still checks against its row is sealed as it stands;
// one that does not is left unsealed, and reads as altered. Returns how many
// were sealed and how many were left.
func SealUnsealed(ctx context.Context, owner *pgx.Conn, keys *secure.Keys) (sealed, left int, err error) {
	s := &Server{Keys: keys}
	err = db.OnConn(ctx, owner, func(tx *db.Tx) error {
		// In pages by id. A sealed attempt leaves the set and one left
		// unsealed stays at its front, so each page skips the ones left.
		for {
			rows, err := attempt.New().Where(attempt.Seal.IsNull()).
				Order(attempt.ID.Asc()).Offset(int64(left)).Limit(sealPage).ForUpdate().All(ctx, tx, nil)
			if err != nil {
				return err
			}
			for _, r := range rows {
				a := attemptOf(r)
				a.Seal = s.seal(a)
				_, intact, _, err := s.loadEvents(ctx, tx, a)
				if err != nil {
					return err
				}
				if !intact {
					left++
					continue
				}
				m := attempt.MutateKey(r.ID)
				m.SetSeal(a.Seal)
				if err := m.Update(ctx, tx); err != nil {
					return err
				}
				sealed++
			}
			if len(rows) < sealPage {
				return nil
			}
		}
	})
	return sealed, left, err
}

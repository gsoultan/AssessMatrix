package api

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/gsoultan/assessmatrix/services/core/internal/mail"
)

// queueMail writes an outbox row and its delivery job in the caller's
// transaction: if the change rolls back, no mail goes out, and if it commits,
// the mail is guaranteed a delivery attempt.
func (s *Server) queueMail(ctx context.Context, tx pgx.Tx, kind, to, subject, body string) error {
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO outbox (tenant_id, kind, to_addr, subject, body)
		VALUES (am_tenant(), $1, $2, $3, $4) RETURNING id`, kind, to, subject, body).Scan(&id); err != nil {
		return err
	}
	if s.Jobs == nil {
		return nil
	}
	_, err := s.Jobs.InsertTx(ctx, tx, mail.SendArgs{OutboxID: id}, nil)
	return err
}

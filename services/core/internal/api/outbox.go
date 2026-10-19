package api

import (
	"context"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/mail"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/outbox"
)

// queueMail writes an outbox row and its delivery job in the caller's
// transaction: if the change rolls back, no mail goes out, and if it commits,
// the mail is guaranteed a delivery attempt.
func (s *Server) queueMail(ctx context.Context, tx *db.Tx, kind, to, subject, body string) error {
	n := outbox.Create()
	n.SetTenantID(tx.Tenant())
	n.SetKind(kind)
	n.SetToAddr(to)
	n.SetSubject(subject)
	n.SetBody(body)
	row, err := n.Insert(ctx, tx)
	if err != nil {
		return err
	}
	if s.Jobs == nil {
		return nil
	}
	_, err = s.Jobs.InsertTx(ctx, tx.Pgx(), mail.SendArgs{OutboxID: idText(row.ID)}, nil)
	return err
}

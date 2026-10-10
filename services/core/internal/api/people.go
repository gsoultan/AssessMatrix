package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

type personService struct{ *Server }

// MaxRosterRows matches the roster file cap in docs/08.
const MaxRosterRows = 5000

func scanPerson(row pgx.Row) (*v1.Person, error) {
	var p v1.Person
	var at time.Time
	if err := row.Scan(&p.Id, &p.Name, &p.Email, &p.Group, &at); err != nil {
		return nil, err
	}
	p.CreatedAt = at.UnixMilli()
	return &p, nil
}

// cleanDraft trims a draft and checks it is a usable address. The schema
// checks again; this gives a readable error first.
func cleanDraft(d *v1.PersonDraft) (*v1.PersonDraft, error) {
	email := strings.TrimSpace(d.GetEmail())
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 || strings.ContainsAny(email, " \t\r\n<>") || len(email) > 320 {
		return nil, invalidArg(errors.New("not an email address: " + email))
	}
	name := strings.TrimSpace(d.GetName())
	if name == "" {
		name = email[:at]
	}
	group := strings.TrimSpace(d.GetGroup())
	if len(name) > 200 || len(group) > 200 {
		return nil, invalidArg(errors.New("name and group are limited to 200 characters"))
	}
	return &v1.PersonDraft{Name: name, Email: email, Group: group}, nil
}

func (s *personService) ListPeople(ctx context.Context, _ *connect.Request[v1.ListPeopleRequest]) (*connect.Response[v1.ListPeopleResponse], error) {
	var out []*v1.Person
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, email, grp, created_at FROM people ORDER BY created_at DESC LIMIT 50000`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPerson(rows)
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, s.internal(ctx, "ListPeople", err)
	}
	return connect.NewResponse(&v1.ListPeopleResponse{People: out}), nil
}

func (s *personService) AddPerson(ctx context.Context, req *connect.Request[v1.AddPersonRequest]) (*connect.Response[v1.AddPersonResponse], error) {
	d, err := cleanDraft(req.Msg.GetPerson())
	if err != nil {
		return nil, err
	}
	var p *v1.Person
	err = s.tx(ctx, func(tx pgx.Tx) (err error) {
		p, err = scanPerson(tx.QueryRow(ctx, `INSERT INTO people (tenant_id, name, email, grp)
			VALUES (am_tenant(), $1, $2, $3) RETURNING id, name, email, grp, created_at`, d.Name, d.Email, d.Group))
		if isUniqueViolation(err) {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("someone with that email is already on the roster"))
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, "person.added", p.GetId(), nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "AddPerson", err)
	}
	return connect.NewResponse(&v1.AddPersonResponse{Person: p}), nil
}

// ensure puts every draft on the roster once and returns ids in input order.
func ensure(ctx context.Context, tx pgx.Tx, drafts []*v1.PersonDraft) (ids []string, created int32, people []*v1.Person, err error) {
	seen := map[string]bool{}
	for _, raw := range drafts {
		d, err := cleanDraft(raw)
		if err != nil {
			return nil, 0, nil, err
		}
		key := strings.ToLower(d.Email)
		if seen[key] {
			continue
		}
		seen[key] = true
		// Insert if new; either way return the row. xmax = 0 marks a fresh insert.
		var inserted bool
		var p v1.Person
		var at time.Time
		err = tx.QueryRow(ctx, `INSERT INTO people (tenant_id, name, email, grp) VALUES (am_tenant(), $1, $2, $3)
			ON CONFLICT (tenant_id, lower(email)) DO UPDATE SET email = people.email
			RETURNING id, name, email, grp, created_at, (xmax = 0)`, d.Name, d.Email, d.Group).
			Scan(&p.Id, &p.Name, &p.Email, &p.Group, &at, &inserted)
		if err != nil {
			return nil, 0, nil, err
		}
		p.CreatedAt = at.UnixMilli()
		if inserted {
			created++
		}
		ids = append(ids, p.Id)
		people = append(people, &p)
	}
	return ids, created, people, nil
}

func (s *personService) EnsurePeople(ctx context.Context, req *connect.Request[v1.EnsurePeopleRequest]) (*connect.Response[v1.EnsurePeopleResponse], error) {
	if len(req.Msg.GetPeople()) > MaxRosterRows {
		return nil, invalidArg(errors.New("at most 5,000 people at once"))
	}
	var out v1.EnsurePeopleResponse
	err := s.tx(ctx, func(tx pgx.Tx) (err error) {
		out.Ids, out.Created, out.People, err = ensure(ctx, tx, req.Msg.GetPeople())
		if err != nil {
			return err
		}
		return audit(ctx, tx, "people.ensured", "", map[string]any{"rows": len(out.Ids), "created": out.Created})
	})
	if err != nil {
		return nil, s.internal(ctx, "EnsurePeople", err)
	}
	return connect.NewResponse(&out), nil
}

func (s *personService) DeletePerson(ctx context.Context, req *connect.Request[v1.DeletePersonRequest]) (*connect.Response[v1.DeletePersonResponse], error) {
	id := req.Msg.GetId()
	if !validUUID(id) {
		return nil, notFound("person")
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var sat bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM attempts WHERE person_id = $1)`, id).Scan(&sat); err != nil {
			return err
		}
		if sat {
			return failedPrecondition("this person has sat an instrument; their attempts are evidence and cannot be deleted from here")
		}
		// Their assignments go with them: no dangling invitations.
		tag, err := tx.Exec(ctx, `DELETE FROM people WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("person")
		}
		return audit(ctx, tx, "person.deleted", id, nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "DeletePerson", err)
	}
	return connect.NewResponse(&v1.DeletePersonResponse{}), nil
}

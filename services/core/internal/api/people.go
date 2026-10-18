package api

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	"github.com/gsoultan/storm/runtime"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/person"
)

type personService struct{ *Server }

// MaxRosterRows matches the roster file cap in docs/08.
const MaxRosterRows = 5000

// personOf is the API's view of a roster row.
func personOf(r person.Row) *v1.Person {
	return &v1.Person{Id: idText(r.ID), Name: r.Name, Email: r.Email, Group: r.GroupName, CreatedAt: r.CreatedAt.UnixMilli()}
}

// newPerson stages a roster row from a cleaned draft.
func newPerson(tx *db.Tx, d *v1.PersonDraft) person.Ins {
	n := person.Create()
	n.SetTenantID(tx.Tenant())
	n.SetName(d.Name)
	n.SetEmail(d.Email)
	n.SetGroupName(d.Group)
	return n
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		rows, err := person.New().Order(person.CreatedAt.Desc(), person.ID.Asc()).Limit(50000).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, personOf(r))
		}
		return nil
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
	err = s.tx(ctx, func(tx *db.Tx) (err error) {
		n := newPerson(tx, d)
		r, err := n.Insert(ctx, tx)
		if isUniqueViolation(err) {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("someone with that email is already on the roster"))
		}
		if err != nil {
			return err
		}
		p = personOf(r)
		return audit(ctx, tx, "person.added", p.GetId(), nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "AddPerson", err)
	}
	return connect.NewResponse(&v1.AddPersonResponse{Person: p}), nil
}

// ensure puts every draft on the roster once and returns ids in input order.
func ensure(ctx context.Context, tx *db.Tx, drafts []*v1.PersonDraft) (ids []string, created int32, people []*v1.Person, err error) {
	seen := map[string]bool{}
	for _, raw := range drafts {
		d, err := cleanDraft(raw)
		if err != nil {
			return nil, 0, nil, err
		}
		lower := strings.ToLower(d.Email)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		// Insert if new; someone already on the roster keeps their row.
		n := newPerson(tx, d)
		r, err := n.OnConflictTenantIDLowerEmail().DoNothing().Insert(ctx, tx)
		switch {
		case err == nil:
			created++
		case errors.Is(err, runtime.ErrConflict):
			var ok bool
			if r, ok, err = person.New().Where(person.Email.EqLower(d.Email)).One(ctx, tx); err != nil {
				return nil, 0, nil, err
			}
			if !ok { // deleted between the two statements
				return nil, 0, nil, connect.NewError(connect.CodeAborted, errors.New("the roster changed; try again"))
			}
		default:
			return nil, 0, nil, err
		}
		p := personOf(r)
		ids = append(ids, p.Id)
		people = append(people, p)
	}
	return ids, created, people, nil
}

func (s *personService) EnsurePeople(ctx context.Context, req *connect.Request[v1.EnsurePeopleRequest]) (*connect.Response[v1.EnsurePeopleResponse], error) {
	if len(req.Msg.GetPeople()) > MaxRosterRows {
		return nil, invalidArg(errors.New("at most 5,000 people at once"))
	}
	var out v1.EnsurePeopleResponse
	err := s.tx(ctx, func(tx *db.Tx) (err error) {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		sat, err := attempt.New().Where(attempt.PersonID.Eq(key(id))).Exists(ctx, tx)
		if err != nil {
			return err
		}
		if sat {
			return failedPrecondition("this person has sat an instrument; their attempts are evidence and cannot be deleted from here")
		}
		// Their assignments go with them: no dangling invitations.
		if err := person.Delete(ctx, tx, key(id)); noRow(err) {
			return notFound("person")
		} else if err != nil {
			return err
		}
		return audit(ctx, tx, "person.deleted", id, nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "DeletePerson", err)
	}
	return connect.NewResponse(&v1.DeletePersonResponse{}), nil
}

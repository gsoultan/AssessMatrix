package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

type examService struct{ *Server }

const examColumns = `id, kind, status, version, published_version, title, issuer, tier, pass_pct,
	shuffle, timed, anonymous, sections, updated_at`

func scanExam(row pgx.Row) (*v1.Exam, error) {
	var (
		e        v1.Exam
		kind     int16
		status   int16
		tier     int16
		passPct  int16
		sections []byte
		updated  time.Time
	)
	if err := row.Scan(&e.Id, &kind, &status, &e.Version, &e.PublishedVersion, &e.Title, &e.Issuer,
		&tier, &passPct, &e.Shuffle, &e.Timed, &e.Anonymous, &sections, &updated); err != nil {
		return nil, err
	}
	e.Kind, e.Status, e.Tier, e.PassPct = v1.Kind(kind), v1.Status(status), int32(tier), int32(passPct)
	e.UpdatedAt = updated.UnixMilli()
	var holder v1.Exam
	if err := protojson.Unmarshal(sections, &holder); err != nil {
		return nil, fmt.Errorf("decode sections of %s: %w", e.Id, err)
	}
	e.Sections = holder.Sections
	return &e, nil
}

func encodeSections(e *v1.Exam) ([]byte, error) {
	return protojson.Marshal(&v1.Exam{Sections: e.GetSections()})
}

func encodeSnapshot(e *v1.Exam) ([]byte, error) { return protojson.Marshal(e) }

func decodeSnapshot(b []byte) (*v1.Exam, error) {
	var e v1.Exam
	if err := protojson.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func getExam(ctx context.Context, tx pgx.Tx, id string, lock bool) (*v1.Exam, error) {
	if !validUUID(id) {
		return nil, notFound("instrument")
	}
	q := `SELECT ` + examColumns + ` FROM exams WHERE id = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	e, err := scanExam(tx.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("instrument")
	}
	return e, err
}

// examAt returns the version an attempt or invitation is pinned to.
func examAt(ctx context.Context, tx pgx.Tx, examID string, version int32) (*v1.Exam, error) {
	var snap []byte
	err := tx.QueryRow(ctx, `SELECT snapshot FROM exam_versions WHERE exam_id = $1 AND version = $2`, examID, version).Scan(&snap)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("instrument version")
	}
	if err != nil {
		return nil, err
	}
	return decodeSnapshot(snap)
}

func snapshot(ctx context.Context, tx pgx.Tx, e *v1.Exam, note string) error {
	b, err := encodeSnapshot(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO exam_versions (tenant_id, exam_id, version, note, snapshot)
		VALUES (am_tenant(), $1, $2, $3, $4)
		ON CONFLICT (exam_id, version) DO UPDATE SET snapshot = excluded.snapshot, note = excluded.note, published_at = now()`,
		e.GetId(), e.GetVersion(), note, b)
	return err
}

func writeExam(ctx context.Context, tx pgx.Tx, e *v1.Exam) (*v1.Exam, error) {
	sections, err := encodeSections(e)
	if err != nil {
		return nil, err
	}
	return scanExam(tx.QueryRow(ctx, `UPDATE exams SET kind = $2, status = $3, version = $4, published_version = $5,
		title = $6, issuer = $7, tier = $8, pass_pct = $9, shuffle = $10, timed = $11, anonymous = $12,
		sections = $13, updated_at = clock_timestamp()
		WHERE id = $1 RETURNING `+examColumns,
		e.GetId(), int16(e.GetKind()), int16(e.GetStatus()), e.GetVersion(), e.GetPublishedVersion(),
		e.GetTitle(), e.GetIssuer(), int16(e.GetTier()), int16(e.GetPassPct()), e.GetShuffle(), e.GetTimed(),
		e.GetAnonymous(), sections))
}

func insertExam(ctx context.Context, tx pgx.Tx, e *v1.Exam) (*v1.Exam, error) {
	sections, err := encodeSections(e)
	if err != nil {
		return nil, err
	}
	return scanExam(tx.QueryRow(ctx, `INSERT INTO exams (tenant_id, kind, status, version, published_version,
		title, issuer, tier, pass_pct, shuffle, timed, anonymous, sections)
		VALUES (am_tenant(), $1, $2, 1, 0, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+examColumns,
		int16(e.GetKind()), int16(v1.Status_STATUS_DRAFT), e.GetTitle(), e.GetIssuer(), int16(e.GetTier()),
		int16(e.GetPassPct()), e.GetShuffle(), e.GetTimed(), e.GetAnonymous(), sections))
}

func (s *examService) ListExams(ctx context.Context, _ *connect.Request[v1.ListExamsRequest]) (*connect.Response[v1.ListExamsResponse], error) {
	var out []*v1.Exam
	err := s.tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+examColumns+` FROM exams ORDER BY updated_at DESC LIMIT 1000`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanExam(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, s.internal(ctx, "ListExams", err)
	}
	return connect.NewResponse(&v1.ListExamsResponse{Exams: out}), nil
}

func (s *examService) GetExam(ctx context.Context, req *connect.Request[v1.GetExamRequest]) (*connect.Response[v1.GetExamResponse], error) {
	var e *v1.Exam
	err := s.tx(ctx, func(tx pgx.Tx) (err error) {
		e, err = getExam(ctx, tx, req.Msg.GetId(), false)
		return err
	})
	if err != nil {
		return nil, s.internal(ctx, "GetExam", err)
	}
	return connect.NewResponse(&v1.GetExamResponse{Exam: e}), nil
}

// blankExam mirrors blankExam() in the prototype's store.ts.
func blankExam(kind v1.Kind) *v1.Exam {
	survey := kind == v1.Kind_KIND_SURVEY
	title := map[v1.Kind]string{v1.Kind_KIND_EXAM: "Asesmen baru", v1.Kind_KIND_QUIZ: "Kuis baru", v1.Kind_KIND_SURVEY: "Kuesioner baru"}[kind]
	var tier int32
	if kind == v1.Kind_KIND_EXAM {
		tier = 2
	}
	item := &v1.Item{Id: shortID("i"), Type: v1.ItemType_ITEM_TYPE_SINGLE, Points: 1}
	if survey {
		item.Type = v1.ItemType_ITEM_TYPE_LIKERT
		item.Scale = &v1.Scale{Points: 5, MinLabel: "Sangat tidak setuju", MaxLabel: "Sangat setuju"}
	} else {
		for range 4 {
			item.Choices = append(item.Choices, &v1.Choice{Id: shortID("c")})
		}
	}
	return &v1.Exam{
		Kind: kind, Title: title, Issuer: "Organisasi Anda", Tier: tier, PassPct: 70,
		Shuffle: !survey, Timed: kind == v1.Kind_KIND_EXAM, Anonymous: survey,
		Sections: []*v1.Section{{Id: shortID("s"), Name: "Bagian 1", Minutes: 30, Items: []*v1.Item{item}}},
	}
}

// shortID makes ids for things inside an instrument (sections, items,
// choices), in the prototype's style.
func shortID(prefix string) string { return prefix + "_" + strings.ToLower(secure.Token()[:10]) }

func (s *examService) CreateExam(ctx context.Context, req *connect.Request[v1.CreateExamRequest]) (*connect.Response[v1.CreateExamResponse], error) {
	kind := req.Msg.GetKind()
	if kind == v1.Kind_KIND_UNSPECIFIED || kind > v1.Kind_KIND_SURVEY {
		return nil, invalidArg(errors.New("kind must be exam, quiz or survey"))
	}
	var e *v1.Exam
	err := s.tx(ctx, func(tx pgx.Tx) (err error) {
		if e, err = insertExam(ctx, tx, blankExam(kind)); err != nil {
			return err
		}
		return audit(ctx, tx, "exam.created", e.GetId(), nil)
	})
	if err != nil {
		return nil, s.internal(ctx, "CreateExam", err)
	}
	return connect.NewResponse(&v1.CreateExamResponse{Exam: e}), nil
}

func (s *examService) SaveExam(ctx context.Context, req *connect.Request[v1.SaveExamRequest]) (*connect.Response[v1.SaveExamResponse], error) {
	in := req.Msg.GetExam()
	if in == nil {
		return nil, invalidArg(errors.New("exam is required"))
	}
	if err := domain.ValidateSections(in); err != nil {
		return nil, invalidArg(err)
	}
	mode := req.Msg.GetMode()
	if mode == v1.SaveMode_SAVE_MODE_UNSPECIFIED {
		mode = v1.SaveMode_SAVE_MODE_DRAFT
	}
	var out *v1.Exam
	err := s.tx(ctx, func(tx pgx.Tx) error {
		cur, err := getExam(ctx, tx, in.GetId(), true)
		if err != nil {
			return err
		}
		if exp := req.Msg.GetExpectedUpdatedAt(); exp != 0 && exp != cur.GetUpdatedAt() {
			return connect.NewError(connect.CodeAborted, errors.New("this instrument was changed elsewhere; reload before saving"))
		}
		next := proto.Clone(in).(*v1.Exam)
		// Lifecycle fields change only through their own operations.
		next.Status, next.Version, next.PublishedVersion = cur.Status, cur.Version, cur.PublishedVersion
		if next.GetKind() == v1.Kind_KIND_UNSPECIFIED {
			next.Kind = cur.Kind
		}
		live := cur.GetPublishedVersion() == cur.GetVersion() && cur.GetPublishedVersion() > 0
		switch mode {
		case v1.SaveMode_SAVE_MODE_NEW_VERSION:
			next.Version = cur.GetVersion() + 1
		case v1.SaveMode_SAVE_MODE_IN_PLACE:
			if !live {
				return failedPrecondition("there is no live version to update in place")
			}
			if err := domain.ReadyToPublish(next); err != nil {
				return invalidArg(err)
			}
		case v1.SaveMode_SAVE_MODE_DRAFT:
			// Saving a draft must never rewrite what candidates are sitting.
			if live && cur.GetStatus() == v1.Status_STATUS_PUBLISHED {
				return failedPrecondition("this version is live; save in place or as a new version")
			}
		default:
			return invalidArg(errors.New("unknown save mode"))
		}
		if out, err = writeExam(ctx, tx, next); err != nil {
			return err
		}
		if mode == v1.SaveMode_SAVE_MODE_IN_PLACE {
			// The frozen copy and what candidates get must never disagree.
			if err := snapshot(ctx, tx, out, "updated in place"); err != nil {
				return err
			}
		}
		return audit(ctx, tx, "exam.saved", out.GetId(), map[string]any{"mode": mode.String(), "version": out.GetVersion()})
	})
	if err != nil {
		return nil, s.internal(ctx, "SaveExam", err)
	}
	return connect.NewResponse(&v1.SaveExamResponse{Exam: out}), nil
}

func (s *examService) DuplicateExam(ctx context.Context, req *connect.Request[v1.DuplicateExamRequest]) (*connect.Response[v1.DuplicateExamResponse], error) {
	var out *v1.Exam
	err := s.tx(ctx, func(tx pgx.Tx) error {
		src, err := getExam(ctx, tx, req.Msg.GetId(), false)
		if err != nil {
			return err
		}
		copy := proto.Clone(src).(*v1.Exam)
		copy.Title = src.GetTitle() + " (salinan)"
		// New ids throughout, or the copy would share answer keys by reference.
		for _, sec := range copy.GetSections() {
			sec.Id = shortID("s")
			for _, it := range sec.GetItems() {
				it.Id = shortID("i")
				remap := map[string]string{}
				for _, c := range it.GetChoices() {
					remap[c.GetId()] = shortID("c")
					c.Id = remap[c.GetId()]
				}
				if len(it.GetChoices()) > 0 {
					for i, k := range it.GetCorrect() {
						if n, ok := remap[k]; ok {
							it.Correct[i] = n
						}
					}
				}
			}
		}
		if out, err = insertExam(ctx, tx, copy); err != nil {
			return err
		}
		return audit(ctx, tx, "exam.duplicated", out.GetId(), map[string]any{"from": src.GetId()})
	})
	if err != nil {
		return nil, s.internal(ctx, "DuplicateExam", err)
	}
	return connect.NewResponse(&v1.DuplicateExamResponse{Exam: out}), nil
}

func (s *examService) SetExamStatus(ctx context.Context, req *connect.Request[v1.SetExamStatusRequest]) (*connect.Response[v1.SetExamStatusResponse], error) {
	status := req.Msg.GetStatus()
	if status == v1.Status_STATUS_UNSPECIFIED || status > v1.Status_STATUS_ARCHIVED {
		return nil, invalidArg(errors.New("unknown status"))
	}
	var out *v1.Exam
	err := s.tx(ctx, func(tx pgx.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetId(), true)
		if err != nil {
			return err
		}
		e.Status = status
		if status == v1.Status_STATUS_PUBLISHED {
			if err := domain.ReadyToPublish(e); err != nil {
				return invalidArg(err)
			}
			// Publishing is what freezes a version.
			e.PublishedVersion = e.GetVersion()
		}
		if out, err = writeExam(ctx, tx, e); err != nil {
			return err
		}
		if status == v1.Status_STATUS_PUBLISHED {
			if err := snapshot(ctx, tx, out, "published"); err != nil {
				return err
			}
			if err := s.emit(ctx, tx, EventExamPublished, "", map[string]any{
				"exam_id": out.GetId(), "version": out.GetPublishedVersion(), "kind": out.GetKind().String(), "title": out.GetTitle(),
			}); err != nil {
				return err
			}
		}
		return audit(ctx, tx, "exam.status", out.GetId(), map[string]any{"status": status.String(), "version": out.GetVersion()})
	})
	if err != nil {
		return nil, s.internal(ctx, "SetExamStatus", err)
	}
	return connect.NewResponse(&v1.SetExamStatusResponse{Exam: out}), nil
}

func (s *examService) DeleteExam(ctx context.Context, req *connect.Request[v1.DeleteExamRequest]) (*connect.Response[v1.DeleteExamResponse], error) {
	err := s.tx(ctx, func(tx pgx.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetId(), true)
		if err != nil {
			return err
		}
		if e.GetStatus() != v1.Status_STATUS_ARCHIVED {
			return failedPrecondition("archive an instrument before deleting it")
		}
		var sat bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM attempts WHERE exam_id = $1)`, e.GetId()).Scan(&sat); err != nil {
			return err
		}
		if sat {
			return failedPrecondition("this instrument has been sat; its attempts are evidence and it can only stay archived")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM exams WHERE id = $1`, e.GetId()); err != nil {
			return err
		}
		return audit(ctx, tx, "exam.deleted", e.GetId(), map[string]any{"title": e.GetTitle()})
	})
	if err != nil {
		return nil, s.internal(ctx, "DeleteExam", err)
	}
	return connect.NewResponse(&v1.DeleteExamResponse{}), nil
}

func (s *examService) ListExamVersions(ctx context.Context, req *connect.Request[v1.ListExamVersionsRequest]) (*connect.Response[v1.ListExamVersionsResponse], error) {
	var out []*v1.ExamVersion
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := getExam(ctx, tx, req.Msg.GetExamId(), false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT version, published_at, note, snapshot FROM exam_versions
			WHERE exam_id = $1 ORDER BY version DESC`, req.Msg.GetExamId())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				v    v1.ExamVersion
				at   time.Time
				snap []byte
			)
			if err := rows.Scan(&v.Version, &at, &v.Note, &snap); err != nil {
				return err
			}
			e, err := decodeSnapshot(snap)
			if err != nil {
				return err
			}
			v.ExamId, v.PublishedAt, v.Exam = req.Msg.GetExamId(), at.UnixMilli(), e
			out = append(out, &v)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, s.internal(ctx, "ListExamVersions", err)
	}
	return connect.NewResponse(&v1.ListExamVersionsResponse{Versions: out}), nil
}

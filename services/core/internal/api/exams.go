package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/gsoultan/storm/runtime"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/exam"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/examversion"
)

type examService struct{ *Server }

// maxVersions bounds a version history read; nobody publishes more.
const maxVersions = 10_000

// examOf is the API's view of an instrument row.
func examOf(r exam.Row) (*v1.Exam, error) {
	e := &v1.Exam{
		Id: idText(r.ID), Kind: v1.Kind(r.Kind), Status: v1.Status(r.Status), Version: r.Version,
		PublishedVersion: r.PublishedVersion, Title: r.Title, Issuer: r.Issuer, Tier: int32(r.Tier),
		PassPct: int32(r.PassPct), Shuffle: r.Shuffle, Timed: r.Timed, Anonymous: r.Anonymous,
		UpdatedAt: r.UpdatedAt.UnixMilli(),
	}
	srcID, okID := r.SourceExamID.Get()
	srcVer, okVer := r.SourceVersion.Get()
	srcName, okName := r.SourceTenantName.Get()
	if okID && okVer && okName {
		e.SharedFrom = &v1.SharedFrom{ExamId: idText(srcID), Version: srcVer, TenantName: srcName}
	}
	var holder v1.Exam
	if err := protojson.Unmarshal(r.Sections, &holder); err != nil {
		return nil, fmt.Errorf("decode sections of %s: %w", e.Id, err)
	}
	e.Sections = holder.Sections
	return e, nil
}

func encodeSections(e *v1.Exam) ([]byte, error) {
	return protojson.Marshal(&v1.Exam{Sections: e.GetSections()})
}

// encodeSnapshot freezes what candidates are given. Where a shared copy came
// from is the organiser's business, not the candidate's, so it stays out.
func encodeSnapshot(e *v1.Exam) ([]byte, error) {
	if e.GetSharedFrom() != nil {
		e = proto.Clone(e).(*v1.Exam)
		e.SharedFrom = nil
	}
	return protojson.Marshal(e)
}

func decodeSnapshot(b []byte) (*v1.Exam, error) {
	var e v1.Exam
	if err := protojson.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func getExam(ctx context.Context, tx *db.Tx, id string, lock bool) (*v1.Exam, error) {
	if !validUUID(id) {
		return nil, notFound("instrument")
	}
	q := exam.New().IDEq(key(id))
	if lock {
		q = q.ForUpdate()
	}
	r, ok, err := q.One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound("instrument")
	}
	return examOf(r)
}

// examAt returns the version an attempt or invitation is pinned to.
func examAt(ctx context.Context, tx *db.Tx, examID string, version int32) (*v1.Exam, error) {
	r, ok, err := examversion.New().
		Where(examversion.ExamID.Eq(key(examID)), examversion.Version.Eq(version)).One(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound("instrument version")
	}
	return decodeSnapshot(r.Snapshot)
}

// snapshot freezes e's current version, replacing an earlier freeze of
// the same version (an in-place update).
func snapshot(ctx context.Context, tx *db.Tx, e *v1.Exam, note string) error {
	b, err := encodeSnapshot(e)
	if err != nil {
		return err
	}
	n := examversion.Create()
	n.SetTenantID(tx.Tenant())
	n.SetExamID(key(e.GetId()))
	n.SetVersion(e.GetVersion())
	n.SetNote(note)
	n.SetSnapshot(b)
	n.SetPublishedAt(time.Now())
	_, err = n.OnConflictExamIDVersion().Insert(ctx, tx)
	return err
}

// setExam assigns every field an organiser edits.
func setExam(m interface {
	SetKind(int16)
	SetStatus(int16)
	SetVersion(int32)
	SetPublishedVersion(int32)
	SetTitle(string)
	SetIssuer(string)
	SetTier(int16)
	SetPassPct(int16)
	SetShuffle(bool)
	SetTimed(bool)
	SetAnonymous(bool)
	SetSections(runtime.JSON)
}, e *v1.Exam) error {
	sections, err := encodeSections(e)
	if err != nil {
		return err
	}
	m.SetKind(int16(e.GetKind()))
	m.SetStatus(int16(e.GetStatus()))
	m.SetVersion(e.GetVersion())
	m.SetPublishedVersion(e.GetPublishedVersion())
	m.SetTitle(e.GetTitle())
	m.SetIssuer(e.GetIssuer())
	m.SetTier(int16(e.GetTier()))
	m.SetPassPct(int16(e.GetPassPct()))
	m.SetShuffle(e.GetShuffle())
	m.SetTimed(e.GetTimed())
	m.SetAnonymous(e.GetAnonymous())
	m.SetSections(sections)
	return nil
}

func writeExam(ctx context.Context, tx *db.Tx, e *v1.Exam) (*v1.Exam, error) {
	m := exam.MutateKey(key(e.GetId()))
	if err := setExam(&m, e); err != nil {
		return nil, err
	}
	// The client's optimistic check compares this stamp, so it must move
	// on every save, even two in one transaction.
	m.SetUpdatedAt(time.Now())
	if err := m.Update(ctx, tx); err != nil {
		return nil, err
	}
	return getExam(ctx, tx, e.GetId(), false)
}

func insertExam(ctx context.Context, tx *db.Tx, e *v1.Exam) (*v1.Exam, error) {
	e = proto.Clone(e).(*v1.Exam)
	e.Status, e.Version, e.PublishedVersion = v1.Status_STATUS_DRAFT, 1, 0
	n := exam.Create()
	n.SetTenantID(tx.Tenant())
	if err := setExam(&n, e); err != nil {
		return nil, err
	}
	r, err := n.Insert(ctx, tx)
	if err != nil {
		return nil, err
	}
	return examOf(r)
}

func (s *examService) ListExams(ctx context.Context, _ *connect.Request[v1.ListExamsRequest]) (*connect.Response[v1.ListExamsResponse], error) {
	var out []*v1.Exam
	err := s.tx(ctx, func(tx *db.Tx) error {
		rows, err := exam.New().Order(exam.UpdatedAt.Desc()).Limit(1000).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, r := range rows {
			e, err := examOf(r)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "ListExams", err)
	}
	return connect.NewResponse(&v1.ListExamsResponse{Exams: out}), nil
}

func (s *examService) GetExam(ctx context.Context, req *connect.Request[v1.GetExamRequest]) (*connect.Response[v1.GetExamResponse], error) {
	var e *v1.Exam
	err := s.tx(ctx, func(tx *db.Tx) (err error) {
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
	err := s.tx(ctx, func(tx *db.Tx) (err error) {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetId(), true)
		if err != nil {
			return err
		}
		if e.GetStatus() != v1.Status_STATUS_ARCHIVED {
			return failedPrecondition("archive an instrument before deleting it")
		}
		sat, err := attempt.New().Where(attempt.ExamID.Eq(key(e.GetId()))).Exists(ctx, tx)
		if err != nil {
			return err
		}
		if sat {
			return failedPrecondition("this instrument has been sat; its attempts are evidence and it can only stay archived")
		}
		if err := exam.Delete(ctx, tx, key(e.GetId())); err != nil {
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
	err := s.tx(ctx, func(tx *db.Tx) error {
		if _, err := getExam(ctx, tx, req.Msg.GetExamId(), false); err != nil {
			return err
		}
		rows, err := examversion.New().Where(examversion.ExamID.Eq(key(req.Msg.GetExamId()))).
			Order(examversion.Version.Desc()).Limit(maxVersions).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		for _, r := range rows {
			e, err := decodeSnapshot(r.Snapshot)
			if err != nil {
				return err
			}
			out = append(out, &v1.ExamVersion{ExamId: req.Msg.GetExamId(), Version: r.Version,
				PublishedAt: r.PublishedAt.UnixMilli(), Note: r.Note, Exam: e})
		}
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "ListExamVersions", err)
	}
	return connect.NewResponse(&v1.ListExamVersionsResponse{Versions: out}), nil
}

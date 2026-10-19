package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/domain"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/analysisrequest"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/attempt"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/itemstat"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/tenant"
)

// MaxReviewNote bounds what a reviewer writes back to a candidate.
const MaxReviewNote = 2000

func (s *resultService) ListReviews(ctx context.Context, req *connect.Request[v1.ListReviewsRequest]) (*connect.Response[v1.ListReviewsResponse], error) {
	out := &v1.ListReviewsResponse{}
	err := s.tx(ctx, func(tx *db.Tx) error {
		if _, err := getExam(ctx, tx, req.Msg.GetExamId(), false); err != nil {
			return err
		}
		rows, err := attempt.New().
			Where(attempt.ExamID.Eq(key(req.Msg.GetExamId())), attempt.SubmittedAt.IsNotNull(), attempt.PurgedAt.IsNull()).
			Order(attempt.SubmittedAt.Asc(), attempt.ID.Asc()).Limit(50000).All(ctx, tx, nil)
		if err != nil {
			return err
		}
		atts := make([]*attemptRow, len(rows))
		for i, r := range rows {
			atts[i] = attemptOf(r)
		}
		// Every essay for the exam, done or not, so an answer's number does
		// not change when the reviewer shows or hides scored ones.
		type entry struct {
			task   *v1.ReviewTask
			number int
			order  []byte
		}
		var all []entry
		me := "staff:" + auth.From(ctx).StaffID
		versions := map[int32]*v1.Exam{}
		for _, a := range atts {
			e, ok := versions[a.ExamVersion]
			if !ok {
				if e, err = examAt(ctx, tx, a.ExamID, a.ExamVersion); err != nil {
					return err
				}
				versions[a.ExamVersion] = e
			}
			essays := essaysOf(e)
			if len(essays) == 0 {
				continue
			}
			events, _, _, err := s.loadEvents(ctx, tx, a)
			if err != nil {
				return err
			}
			answers, _ := domain.Fold(events, a.deadline())
			reviews := domain.Reviews(events)
			number := questionNumbers(e)
			for _, it := range essays {
				task, err := s.reviewTask(a.ID, it.GetId())
				if err != nil {
					return err
				}
				r, done := reviews[it.GetId()]
				ans := answers[it.GetId()]
				t := &v1.ReviewTask{Task: task, ItemId: it.GetId(), Number: int32(number(it.GetId())),
					Stem: it.GetStem(), Text: ans.GetText(), File: ans.GetFile(), Points: it.GetPoints(), Reviewed: done}
				if done && r.By == me {
					t.Given, t.Note = r.Points, r.Note
				} else if done {
					t.ScoredByOther = true
				}
				all = append(all, entry{t, number(it.GetId()), s.Keys.ReviewOrder(it.GetId(), a.ID)})
			}
		}
		slices.SortFunc(all, func(x, y entry) int {
			if c := cmp.Compare(x.number, y.number); c != 0 {
				return c
			}
			if c := strings.Compare(x.task.GetItemId(), y.task.GetItemId()); c != 0 {
				return c
			}
			return bytes.Compare(x.order, y.order)
		})
		n := 0
		for i, en := range all {
			if i == 0 || en.task.GetItemId() != all[i-1].task.GetItemId() {
				n = 0
			}
			n++
			en.task.Label = "Answer " + strconv.Itoa(n)
			if !en.task.GetReviewed() || req.Msg.GetIncludeDone() {
				out.Tasks = append(out.Tasks, en.task)
			}
		}
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "ListReviews", err)
	}
	return connect.NewResponse(out), nil
}

// reviewLabel is the associated data a review task is sealed under.
const reviewLabel = "review-task"

// reviewTask is the opaque handle the queue gives for one answer: the
// attempt and the question, sealed, so the reviewer holds nothing that leads
// back to the candidate.
func (s *Server) reviewTask(attemptID, itemID string) (string, error) {
	b, err := s.Keys.Seal(reviewLabel, []byte(attemptID+"\n"+itemID))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// openReviewTask reverses reviewTask.
func (s *Server) openReviewTask(task string) (attemptID, itemID string, err error) {
	b, err := base64.RawURLEncoding.DecodeString(task)
	if err == nil {
		b, err = s.Keys.Open(reviewLabel, b)
	}
	if err != nil {
		return "", "", invalidArg(errors.New("this review task is not one the queue gave out"))
	}
	attemptID, itemID, _ = strings.Cut(string(b), "\n")
	return attemptID, itemID, nil
}

func essaysOf(e *v1.Exam) []*v1.Item {
	var out []*v1.Item
	for _, it := range domain.AllItems(e) {
		if it.GetType() == v1.ItemType_ITEM_TYPE_ESSAY {
			out = append(out, it)
		}
	}
	return out
}

func (s *resultService) ScoreAnswer(ctx context.Context, req *connect.Request[v1.ScoreAnswerRequest]) (*connect.Response[v1.ScoreAnswerResponse], error) {
	m := req.Msg
	attemptID, itemID := m.GetAttemptId(), m.GetItemId()
	if m.GetTask() != "" {
		var err error
		if attemptID, itemID, err = s.openReviewTask(m.GetTask()); err != nil {
			return nil, err
		}
	}
	note := strings.TrimSpace(m.GetNote())
	if utf8.RuneCountInString(note) > MaxReviewNote {
		return nil, invalidArg(fmt.Errorf("the note is longer than %d characters", MaxReviewNote))
	}
	out := &v1.ScoreAnswerResponse{}
	err := s.tx(ctx, func(tx *db.Tx) error {
		att, err := attemptBy(ctx, tx, "id", attemptID, true)
		if err != nil {
			return err
		}
		if att.SubmittedAt == nil {
			return failedPrecondition("this attempt has not been handed in yet")
		}
		if err := att.open(); err != nil {
			return err
		}
		e, err := examAt(ctx, tx, att.ExamID, att.ExamVersion)
		if err != nil {
			return err
		}
		var item *v1.Item
		for _, it := range essaysOf(e) {
			if it.GetId() == itemID {
				item = it
			}
		}
		if item == nil {
			return invalidArg(errors.New("only an essay question is scored by a person"))
		}
		if m.GetPoints() < 0 || m.GetPoints() > item.GetPoints() {
			return invalidArg(fmt.Errorf("points must be between 0 and %d", item.GetPoints()))
		}
		if e.GetKind() == v1.Kind_KIND_SURVEY {
			return failedPrecondition("a questionnaire is not scored")
		}
		by := "staff:" + auth.From(ctx).StaffID
		if err := s.appendEvents(ctx, tx, att, []*v1.AttemptEvent{{
			Kind: domain.KindReviewScored, ItemId: item.GetId(), Values: []string{strconv.Itoa(int(m.GetPoints()))},
			Detail: note, Text: by, ClientTime: s.now().UnixMilli(),
		}}); err != nil {
			return err
		}
		events, intact, _, err := s.loadEvents(ctx, tx, att)
		if err != nil {
			return err
		}
		answers, _ := domain.Fold(events, att.deadline())
		reviews := domain.Reviews(events)
		before := storedScore(att.Score)
		out.Score = domain.Score(e, answers, reviews)
		if err := storeScore(ctx, tx, att, e, answers, reviews, out.Score); err != nil {
			return err
		}
		if err := s.emit(ctx, tx, EventAttemptScored, att.PersonID, attemptData(att, out.Score)); err != nil {
			return err
		}
		cred, err := s.settleCredential(ctx, tx, att, e, out.Score, intact)
		if err != nil {
			return err
		}
		if cred != "" {
			out.CredentialUrl = s.CredentialURL(cred)
		}
		// A review that decides or changes the final result tells the
		// candidate. A pass is told by the credential's own mail; anything
		// else (a fail, or a pass taken back) is told here.
		final := out.Score.GetPendingHuman() == 0
		changed := before == nil || before.GetPendingHuman() > 0 || before.GetPct() != out.Score.GetPct() || before.GetPassed() != out.Score.GetPassed()
		if final && changed && cred == "" {
			if err := s.mailResult(ctx, tx, att, e, out.Score); err != nil {
				return err
			}
		}
		return audit(ctx, tx, "attempt.reviewed", att.ID, map[string]any{"item": item.GetId(), "points": m.GetPoints()})
	})
	if err != nil {
		return nil, s.internal(ctx, "ScoreAnswer", err)
	}
	return connect.NewResponse(out), nil
}

func (s *resultService) GetItemAnalysis(ctx context.Context, req *connect.Request[v1.GetItemAnalysisRequest]) (*connect.Response[v1.GetItemAnalysisResponse], error) {
	out := &v1.GetItemAnalysisResponse{}
	err := s.tx(ctx, func(tx *db.Tx) error {
		e, err := getExam(ctx, tx, req.Msg.GetExamId(), false)
		if err != nil {
			return err
		}
		v := req.Msg.GetExamVersion()
		st, ok, err := itemstat.New().Where(itemstat.ExamID.Eq(key(e.GetId()))).
			WhereIf(v > 0, itemstat.ExamVersion.Eq(v)).
			Order(itemstat.ExamVersion.Desc()).One(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			out.Pending = true
			out.ExamVersion = v
			return nil
		}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(st.Stats, out); err != nil {
			return err
		}
		// Stale while newer hand-ins wait in the queue.
		queued, err := analysisrequest.New().
			Where(analysisrequest.ExamID.Eq(st.ExamID), analysisrequest.ExamVersion.Eq(out.GetExamVersion())).Exists(ctx, tx)
		if err != nil {
			return err
		}
		out.Pending = queued
		return nil
	})
	if err != nil {
		return nil, s.internal(ctx, "GetItemAnalysis", err)
	}
	return connect.NewResponse(out), nil
}

// storedScore is the score as last stored, or nil.
func storedScore(stored []byte) *v1.Score {
	var sc v1.Score
	if stored == nil || protojson.Unmarshal(stored, &sc) != nil {
		return nil
	}
	return &sc
}

// mailResult tells the candidate their final result once nothing is left to
// score, for a result that earned no credential.
func (s *Server) mailResult(ctx context.Context, tx *db.Tx, att *attemptRow, e *v1.Exam, sc *v1.Score) error {
	ten, ok, err := tenant.New().IDEq(tx.Tenant()).One(ctx, tx)
	if err != nil {
		return err
	}
	if !ok {
		return notFound("tenant")
	}
	tenantName := ten.Name
	// The attempt carries its person, copied from the assignment.
	email, err := personEmail(ctx, tx, att.PersonID)
	if err != nil {
		return err
	}
	verdict := "belum mencapai ambang lulus"
	if sc.GetPassed() {
		verdict = "lulus"
	}
	body := fmt.Sprintf("Penilaian %s sudah selesai.\n\nSkor akhir Anda %d%% (%d dari %d poin); ambang lulus %d%%. Hasil: %s.\n\n"+
		"Jika ada yang ingin Anda tanyakan tentang penilaian ini, hubungi penyelenggara.\n\n— %s",
		e.GetTitle(), sc.GetPct(), sc.GetRaw(), sc.GetMax(), e.GetPassPct(), verdict, tenantName)
	return s.queueMail(ctx, tx, "result", email, "Hasil Anda: "+e.GetTitle(), body)
}

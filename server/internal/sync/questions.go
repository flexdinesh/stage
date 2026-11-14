package sync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Conflict codes for answers (error body "code").
const (
	CodeAlreadyAnswered = "already_answered"
	CodeRequestStale    = "request_stale"
)

// refreshQuestionCount counts the answerable questions among a thread's
// pending requests (one t3_pending_request list) after a mirrored read.
// A failed count is logged and keeps the previous one.
func (s *Service) refreshQuestionCount(ctx context.Context, d t3.ThreadDetail) error {
	if d.PendingRequestCount == 0 {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ids, err := s.t3.ListPendingQuestions(callCtx, d.ThreadID)
	cancel()
	if err != nil {
		s.log.Warn("list pending questions", "thread", d.ThreadID, "err", err)
		return nil
	}
	return s.store.SetPendingQuestionCount(ctx, d.ThreadID, len(ids))
}

// PendingRequests is a thread's pending input: answerable questions plus
// approvals stage cannot resolve.
type PendingRequests struct {
	PendingRequestCount int                  `json:"pendingRequestCount"`
	ApprovalCount       int                  `json:"approvalCount"`
	Requests            []t3.PendingQuestion `json:"requests"`
}

// PendingQuestions reads the thread and its pending questions fresh from T3
// and refreshes the mirror. Requests answered meanwhile are skipped.
func (s *Service) PendingQuestions(ctx context.Context, threadID string) (PendingRequests, error) {
	out := PendingRequests{Requests: []t3.PendingQuestion{}}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d, err := s.t3.ReadThread(callCtx, threadID)
	if errors.Is(err, t3.ErrNotFound) {
		return out, db.ErrNotFound
	}
	if err != nil {
		return out, t3Err(err)
	}
	if err := s.store.UpsertThreadDetail(ctx, d); err != nil {
		return out, err
	}
	out.PendingRequestCount = d.PendingRequestCount
	if d.PendingRequestCount == 0 {
		return out, nil
	}
	ids, err := s.t3.ListPendingQuestions(callCtx, threadID)
	if err != nil {
		return out, t3Err(err)
	}
	if err := s.store.SetPendingQuestionCount(ctx, threadID, len(ids)); err != nil {
		return out, err
	}
	out.ApprovalCount = max(0, d.PendingRequestCount-len(ids))
	for _, id := range ids {
		q, err := s.t3.ReadPendingQuestion(callCtx, threadID, id)
		if errors.Is(err, t3.ErrRequestAnswered) || errors.Is(err, t3.ErrRequestStale) {
			continue
		}
		if err != nil {
			return out, t3Err(err)
		}
		if q.Questions == nil {
			q.Questions = []t3.Question{}
		}
		out.Requests = append(out.Requests, q)
	}
	return out, nil
}

// Answered is a sent answer.
type Answered struct {
	RequestID   string    `json:"requestId"`
	RespondedAt time.Time `json:"respondedAt"`
}

func answered(r db.QuestionResponse) Answered {
	return Answered{RequestID: r.RequestID, RespondedAt: r.RespondedAt}
}

// AnswerQuestion validates answers against the request's questions, sends
// them (t3_pending_request respond), records them and refreshes the thread
// and its task. Idempotent: resending the answers stage already sent returns
// the first result without calling T3. Conflicts: already_answered (answered
// in T3, or here with other answers), request_stale (gone/expired).
func (s *Service) AnswerQuestion(ctx context.Context, threadID, requestID string, answers map[string]t3.Answer) (Answered, error) {
	if len(answers) == 0 {
		return Answered{}, db.Invalid("answers are required")
	}
	// prior resolves a T3 "already answered" against what stage recorded.
	prior := func() (Answered, error) {
		r, err := s.store.GetQuestionResponse(ctx, threadID, requestID)
		if errors.Is(err, db.ErrNotFound) {
			return Answered{}, &db.ConflictError{Msg: "This question was already answered in T3.", Code: CodeAlreadyAnswered}
		}
		if err != nil {
			return Answered{}, err
		}
		if !reflect.DeepEqual(r.Answers, answers) {
			return Answered{}, &db.ConflictError{Msg: "This question was already answered with different answers.", Code: CodeAlreadyAnswered}
		}
		return answered(r), nil
	}
	if _, err := s.store.GetQuestionResponse(ctx, threadID, requestID); err == nil {
		return prior()
	} else if !errors.Is(err, db.ErrNotFound) {
		return Answered{}, err
	}
	classify := func(err error) error {
		switch {
		case errors.Is(err, t3.ErrRequestStale):
			return &db.ConflictError{Msg: "This question is no longer pending in T3.", Code: CodeRequestStale}
		case errors.Is(err, t3.ErrNotFound):
			return db.ErrNotFound
		}
		return t3Err(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	q, err := s.t3.ReadPendingQuestion(callCtx, threadID, requestID)
	if errors.Is(err, t3.ErrRequestAnswered) {
		return prior()
	}
	if err != nil {
		return Answered{}, classify(err)
	}
	if err := t3.ValidateAnswers(q.Questions, answers); err != nil {
		return Answered{}, db.Invalid("%s", err.Error())
	}
	err = s.t3.RespondPendingQuestion(callCtx, threadID, requestID, t3.NormalizeAnswers(q.Questions, answers))
	if errors.Is(err, t3.ErrRequestAnswered) {
		return prior() // a concurrent identical submit won the race
	}
	if err != nil {
		return Answered{}, classify(err)
	}
	r, err := s.store.RecordQuestionResponse(ctx, threadID, requestID, answers)
	if err != nil {
		return Answered{}, fmt.Errorf("record answer: %w", err)
	}
	s.refreshAnswered(ctx, threadID)
	return answered(r), nil
}

// refreshAnswered re-reads the thread and polls its task so state leaves
// needs input promptly. Best effort.
func (s *Service) refreshAnswered(ctx context.Context, threadID string) {
	if _, err := s.readSession(ctx, threadID); err != nil {
		s.log.Warn("refresh after answer", "thread", threadID, "err", err)
		return
	}
	th, err := s.store.GetThread(ctx, threadID)
	if err != nil || th.TaskID == nil {
		return
	}
	task, err := s.store.GetTask(ctx, *th.TaskID)
	if err == nil {
		err = s.PollTask(ctx, task)
	}
	if err != nil {
		s.log.Warn("poll task after answer", "task", *th.TaskID, "err", err)
	}
}

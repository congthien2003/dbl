package fakedb

import (
	"context"
	"sync"

	"dbl/internal/database"
)

type SQLSession struct {
	Run         func(context.Context, string) (*database.SQLExecution, error)
	SelectInput func(database.SQLInput) (database.SQLSelection, error)
	mu          sync.Mutex
	calls       []string
	closed      int
	done        chan struct{}
}

func (a *Adapter) QueueSQLSession(s *SQLSession) {
	a.mu.Lock()
	a.sqlQueue = append(a.sqlQueue, s)
	a.mu.Unlock()
}
func (a *Adapter) OpenSQLSession(ctx context.Context) (database.SQLSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || len(a.sqlQueue) == 0 {
		return nil, &database.Error{Kind: database.KindUnsupported, Message: "No scripted SQL session is available."}
	}
	s := a.sqlQueue[0]
	a.sqlQueue = a.sqlQueue[1:]
	return s, nil
}
func (s *SQLSession) Select(in database.SQLInput) (database.SQLSelection, error) {
	if s.SelectInput != nil {
		return s.SelectInput(in)
	}
	text := in.Text
	for _, span := range in.Selections {
		if span.Start < span.End {
			text = in.Text[span.Start:span.End]
			break
		}
	}
	return database.SQLSelection{SQL: text}, nil
}
func (s *SQLSession) Execute(ctx context.Context, text string) (*database.SQLExecution, error) {
	s.mu.Lock()
	s.calls = append(s.calls, text)
	s.mu.Unlock()
	if s.Run != nil {
		return s.Run(ctx, text)
	}
	return &database.SQLExecution{Ready: true}, nil
}
func (s *SQLSession) Close(context.Context) error {
	s.mu.Lock()
	if s.closed == 0 {
		s.closed++
		if s.done == nil {
			s.done = make(chan struct{})
		}
		close(s.done)
	}
	s.mu.Unlock()
	return nil
}
func (s *SQLSession) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done == nil {
		s.done = make(chan struct{})
	}
	return s.done
}
func (s *SQLSession) CloseCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.closed }
func (s *SQLSession) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

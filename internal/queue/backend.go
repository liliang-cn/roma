package queue

import "context"

// Backend captures the queue capabilities used by ROMA.
type Backend interface {
	Enqueue(ctx context.Context, req Request) error
	Update(ctx context.Context, req Request) error
	Get(ctx context.Context, id string) (Request, error)
	List(ctx context.Context) ([]Request, error)
	NextPending(ctx context.Context) (Request, bool, error)
	RecoverInterrupted(ctx context.Context) error
}

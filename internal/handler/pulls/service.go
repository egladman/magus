package pulls

import (
	"context"

	"connectrpc.com/connect"

	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1/pullsv1alpha1connect"
)

// Service implements PullsService over a Poller and the author store.
type Service struct {
	poller *Poller
	store  Store
	run    runner
}

var _ pullsv1alpha1connect.PullsServiceHandler = (*Service)(nil)

// NewService returns the handler. GetPulls reads the poller's last board
// and never calls the forge.
func NewService(poller *Poller, store Store) *Service {
	return &Service{poller: poller, store: store, run: execRunner}
}

// GetPulls returns the last board the poller built.
func (s *Service) GetPulls(_ context.Context, _ *connect.Request[pullsv1.GetPullsRequest]) (*connect.Response[pullsv1.GetPullsResponse], error) {
	board, err := s.poller.Board()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pullsv1.GetPullsResponse{Board: board}), nil
}

// SetAuthors saves the filter and asks the poller for one sync, which
// still waits out the poll floor.
func (s *Service) SetAuthors(ctx context.Context, req *connect.Request[pullsv1.SetAuthorsRequest]) (*connect.Response[pullsv1.SetAuthorsResponse], error) {
	if err := s.store.Set(ctx, req.Msg.GetAuthors()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	s.poller.Kick()
	board, err := s.poller.Board()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pullsv1.SetAuthorsResponse{Board: board}), nil
}

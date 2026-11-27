package cats

import (
	"context"
	"time"
)

type Provider interface {
	GIF(context.Context) ([]byte, error)
}
type Service struct{ provider Provider }

func New(p Provider) *Service { return &Service{provider: p} }
func (s *Service) GIF(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	return s.provider.GIF(ctx)
}

package server

import (
	"context"
	"log/slog"
	"time"

	"zen/internal/data"
)

type Server struct {
	ctx  context.Context
	data *data.Data
}

func NewServer(ctx context.Context, data *data.Data) (*Server, error) {
	return &Server{ctx: ctx, data: data}, nil
}

func (s *Server) Run() error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			slog.Info("runner stoping...")

			return nil
		case <-ticker.C:
			slog.Info("runner working...")

			resp, err := s.data.Etcd.Get(s.ctx, "foo")
			if err != nil {
				return err
			}

			if len(resp.Kvs) > 0 {
				kv := resp.Kvs[0]
				slog.Info("runner got resp",
					"key", string(kv.Key),
					"value", string(kv.Value),
					"version", kv.Version,
				)
			} else {
				slog.Info("runner got resp", "key", "foo", "value", "not found")
			}
		}
	}
}

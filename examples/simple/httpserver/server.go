package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

const shutdownTimeout = 5 * time.Second

type ClosableServer struct {
	server *http.Server
	mux    *http.ServeMux
}

func New(port string) *ClosableServer {
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}

	return &ClosableServer{
		server: srv,
		mux:    mux,
	}
}

func (s *ClosableServer) Start(_ context.Context) error {
	log.Info().Str("Address", s.server.Addr).Msg("Starting http server")
	err := s.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}

func (s *ClosableServer) Close() {
	log.Info().Msg("Shutting down HTTP server")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	err := s.server.Shutdown(ctx)
	if err != nil {
		log.Error().Err(err).Msg("err while shutting down HTTP server")
	}
	log.Info().Msg("HTTP server stopped")
}

func (s *ClosableServer) Quit() {
	log.Info().Msg("Quitting HTTP server")
	err := s.server.Close()
	if err != nil {
		log.Error().Err(err).Msg("err while shutting down HTTP server")
	}
	log.Info().Msg("HTTP server stopped")
}

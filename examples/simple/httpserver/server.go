package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

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

func (s *ClosableServer) Start() error {
	log.Info().Str("Address", s.server.Addr).Msg("Starting http server")
	return s.server.ListenAndServe()
}

func (s *ClosableServer) Close() {
	log.Info().Msg("Shutting down HTTP server")
	err := s.server.Shutdown(context.Background())
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

package server

import (
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
)

type Server struct {
	db       *sql.DB
	frontend fs.FS
	mux      *http.ServeMux
}

func NewServer(db *sql.DB, frontend fs.FS) *Server {
	s := &Server{
		db:       db,
		frontend: frontend,
		mux:      http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/entries", s.handleEntries)
	s.mux.HandleFunc("/api/cards/image-url", s.handleCardImageURLs)
	s.mux.HandleFunc("POST /api/entries/{id}/giver", s.handleSetGiver)
	s.mux.HandleFunc("DELETE /api/entries/{id}/giver", s.handleClearGiver)
	s.mux.HandleFunc("POST /api/entries/{id}/remove", s.handleRemoveEntry)

	if s.frontend != nil {
		s.mux.Handle("/", http.FileServer(http.FS(s.frontend)))
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) ListenAndServe(addr string) error {
	fmt.Printf("server running - http://localhost%s\n", addr)
	return http.ListenAndServe(addr, s)
}

func (s *Server) DB() *sql.DB {
	return s.db
}

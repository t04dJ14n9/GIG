package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/t04dJ14n9/gig/internal/study"
)

const (
	maxSourceBytes = 64 << 10
	maxBodyBytes   = maxSourceBytes + 1024
	analysisLimit  = 5 * time.Second
)

//go:embed web/*
var webFiles embed.FS

func newHandler() http.Handler {
	assets, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/lessons", serveLessons)
	mux.HandleFunc("/api/analyze", serveAnalysis)
	mux.Handle("/", http.FileServer(http.FS(assets)))
	return recoverMiddleware(securityHeaders(mux))
}

func serveLessons(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, study.Lessons())
}

func serveAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request struct {
		Source string `json:"source"`
	}
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "source exceeds 64 KiB")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON request: "+err.Error())
		return
	}
	if err := requireEOF(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return
	}
	if len(request.Source) > maxSourceBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "source exceeds 64 KiB")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), analysisLimit)
	defer cancel()
	writeJSON(w, http.StatusOK, study.Analyze(ctx, request.Source))
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("ssa-study: recovered request panic: %v", recovered)
				writeError(w, http.StatusInternalServerError, "analysis failed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The status line is already written, so an encode failure cannot
	// change the response; log it instead of dropping it silently.
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("ssa-study: encode response: %v", err)
	}
}

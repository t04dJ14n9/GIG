package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t04dJ14n9/gig/internal/study"
)

func TestHandlerServesIndexLessonsAndAnalysis(t *testing.T) {
	t.Parallel()
	handler := newHandler()

	t.Run("index", func(t *testing.T) {
		t.Parallel()
		response := request(t, handler, http.MethodGet, "/", nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "SSA Study Lab") {
			t.Fatalf("index response = %d %q", response.Code, response.Body.String())
		}
		for _, marker := range []string{`id="source-editor"`, `id="stage-tabs"`, `id="stage-output"`, `src="/app.js"`, `href="/styles.css"`} {
			if !strings.Contains(response.Body.String(), marker) {
				t.Fatalf("index missing application marker %q", marker)
			}
		}
		assertSecurityHeaders(t, response)
	})

	t.Run("assets", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{"/styles.css", "/app.js", "/renderers.js"} {
			response := request(t, handler, http.MethodGet, path, nil)
			if response.Code != http.StatusOK || response.Body.Len() < 100 {
				t.Fatalf("asset %s response = %d (%d bytes)", path, response.Code, response.Body.Len())
			}
		}
	})

	t.Run("lessons", func(t *testing.T) {
		t.Parallel()
		response := request(t, handler, http.MethodGet, "/api/lessons", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("lessons status = %d: %s", response.Code, response.Body.String())
		}
		var lessons []study.Lesson
		if err := json.Unmarshal(response.Body.Bytes(), &lessons); err != nil {
			t.Fatalf("decode lessons: %v", err)
		}
		if len(lessons) != 4 {
			t.Fatalf("lesson count = %d, want 4", len(lessons))
		}
	})

	t.Run("analysis", func(t *testing.T) {
		t.Parallel()
		body := bytes.NewBufferString(`{"source":"package main\nfunc add(a, b int) int { return a+b }\n"}`)
		response := request(t, handler, http.MethodPost, "/api/analyze", body)
		if response.Code != http.StatusOK {
			t.Fatalf("analysis status = %d: %s", response.Code, response.Body.String())
		}
		var result study.Result
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode analysis: %v", err)
		}
		if len(result.Tokens) == 0 || len(result.AST) == 0 || len(result.Functions) == 0 {
			t.Fatalf("incomplete analysis: %#v", result)
		}
	})
}

func TestHandlerRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	handler := newHandler()

	tests := []struct {
		name   string
		method string
		path   string
		body   *bytes.Buffer
		status int
	}{
		{name: "method", method: http.MethodGet, path: "/api/analyze", body: &bytes.Buffer{}, status: http.StatusMethodNotAllowed},
		{name: "malformed", method: http.MethodPost, path: "/api/analyze", body: bytes.NewBufferString(`{"source":`), status: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, path: "/api/analyze", body: bytes.NewBufferString(`{"source":"package main","extra":true}`), status: http.StatusBadRequest},
		{
			name:   "oversized",
			method: http.MethodPost,
			path:   "/api/analyze",
			body:   bytes.NewBufferString(`{"source":"` + strings.Repeat("x", 70<<10) + `"}`),
			status: http.StatusRequestEntityTooLarge,
		},
		{name: "unknown", method: http.MethodGet, path: "/missing", body: &bytes.Buffer{}, status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := request(t, handler, test.method, test.path, test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			assertSecurityHeaders(t, response)
		})
	}
}

func request(t *testing.T, handler http.Handler, method, path string, body *bytes.Buffer) *httptest.ResponseRecorder {
	t.Helper()
	if body == nil {
		body = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func assertSecurityHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := response.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
}

package httpmw

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// statusRecorder captures the status code and size of a response while remaining transparent for streaming (Flush) and connection takeover (Hijack).
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Flush lets streaming handlers (SSE) flush through the middleware chain.
func (s *statusRecorder) Flush() {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	_ = http.NewResponseController(s.ResponseWriter).Flush()
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Started reports whether the response header was already sent.
func (s *statusRecorder) Started() bool { return s.wrote }

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijacking not supported")
}

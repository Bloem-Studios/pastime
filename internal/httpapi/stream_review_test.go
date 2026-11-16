package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFailedStartedExportAbortsTransport(t *testing.T) {
	s := &Server{logger: slog.Default()}
	w := httptest.NewRecorder()
	sw, buf := newExport(w)
	sw.Write([]byte("partial,"))
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("started export failure looked successful")
		}
	}()
	s.finishStream(sw, httptest.NewRequest("GET", "/api/v1/export", nil), buf, errors.New("synthetic write error"))
}

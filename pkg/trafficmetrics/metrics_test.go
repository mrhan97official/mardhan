package trafficmetrics

import (
	"net/http/httptest"
	"testing"
)

func TestCountingWriterPreservesResponseAndCountsBytes(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &CountingWriter{ResponseWriter: recorder}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(202)
	if _, err := w.Write([]byte("123")); err != nil { t.Fatal(err) }
	if _, err := w.Write([]byte("45")); err != nil { t.Fatal(err) }
	if w.BytesWritten != 5 || recorder.Code != 202 || recorder.Body.String() != "12345" {
		t.Fatalf("unexpected response: bytes=%d code=%d body=%q", w.BytesWritten, recorder.Code, recorder.Body.String())
	}
}

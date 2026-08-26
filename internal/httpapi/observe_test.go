package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type observation struct {
	route  string
	method string
	status int
}

func recordingObserver(into *[]observation) Observers {
	return Observers{
		Request: func(route, method string, status int, _ time.Duration) {
			*into = append(*into, observation{route: route, method: method, status: status})
		},
	}
}

func TestObservedRequestsReportTheRouteNotThePath(t *testing.T) {
	var seen []observation
	handler := NewObserved(recordingObserver(&seen), silenceConfig(), &fakeStore{}, operator(), newFakeSilencer())

	request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/42", nil)
	request.Header.Set("Authorization", "Bearer session-token")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if len(seen) != 1 {
		t.Fatalf("observations = %#v, want exactly one", seen)
	}
	// The path carries an alert id. Reporting it would mint a time series per
	// alert and cost the Prometheus doing the scraping far more than the metric
	// is worth, so the label has to be the pattern that matched.
	if seen[0].route != "GET /api/v1/alerts/{id}" {
		t.Errorf("route = %q, want the matched pattern rather than the path", seen[0].route)
	}
	if seen[0].method != http.MethodGet {
		t.Errorf("method = %q, want GET", seen[0].method)
	}
}

func TestObservedRequestsRecordTheStatusThatWasWritten(t *testing.T) {
	var seen []observation
	handler := NewObserved(recordingObserver(&seen), silenceConfig(), &fakeStore{}, operator(), newFakeSilencer())

	request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/not-a-number", nil)
	request.Header.Set("Authorization", "Bearer session-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if len(seen) != 1 || seen[0].status != response.Code {
		t.Fatalf("observed %#v, want the status the handler actually wrote (%d)", seen, response.Code)
	}
}

func TestAPathNothingServesIsNotItsOwnTimeSeries(t *testing.T) {
	var seen []observation
	handler := NewObserved(recordingObserver(&seen), silenceConfig(), &fakeStore{}, operator(), newFakeSilencer())

	// A method with no route at all: the SPA fallback only answers GET, so this
	// reaches nothing. An unmatched request must not name itself, or anyone
	// scanning the deployment writes a new series with every probe.
	request := httptest.NewRequest(http.MethodDelete, "/wp-login.php", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if len(seen) != 1 {
		t.Fatalf("observations = %#v, want exactly one", seen)
	}
	if seen[0].route == "/wp-login.php" {
		t.Fatal("an unmatched path became a label value")
	}
	if seen[0].route != "unmatched" {
		t.Errorf("route = %q, want the fixed unmatched label", seen[0].route)
	}
}

// The stream is a long-lived SSE response. A wrapper that swallowed Flush would
// leave every event in a buffer, and the console would sit there looking
// connected while receiving nothing - a failure no status code would show.
func TestTheObservedWriterStillFlushes(t *testing.T) {
	underlying := httptest.NewRecorder()
	recorder := &statusRecorder{ResponseWriter: underlying, status: http.StatusOK}

	flusher, ok := any(recorder).(http.Flusher)
	if !ok {
		t.Fatal("the wrapper does not implement http.Flusher; SSE would stall behind it")
	}
	if _, err := recorder.Write([]byte("data: hello\n\n")); err != nil {
		t.Fatal(err)
	}
	flusher.Flush()
	if !underlying.Flushed {
		t.Error("Flush did not reach the underlying writer")
	}
}

func TestTheFirstStatusWrittenIsTheOneReported(t *testing.T) {
	recorder := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	recorder.WriteHeader(http.StatusNotFound)
	// net/http warns on a second WriteHeader and keeps the first; the recorder
	// has to agree with it rather than report the one that was ignored.
	recorder.WriteHeader(http.StatusInternalServerError)
	if recorder.status != http.StatusNotFound {
		t.Errorf("status = %d, want the first one written", recorder.status)
	}
}

func TestNewWithoutAnObserverIsNotWrapped(t *testing.T) {
	// An uninstrumented deployment should pay nothing, not even a wrapper.
	if _, ok := New(silenceConfig(), &fakeStore{}, operator(), newFakeSilencer()).(*http.ServeMux); !ok {
		t.Error("New returned a wrapped handler when no observer was given")
	}
}

// countingObservers tracks the stream hooks without a registry.
type countingObservers struct {
	opened, closed, polls, events int
}

func (c *countingObservers) observers() Observers {
	return Observers{
		Request:      func(string, string, int, time.Duration) {},
		StreamOpened: func() { c.opened++ },
		StreamClosed: func() { c.closed++ },
		StreamPolled: func(events int) { c.polls++; c.events += events },
	}
}

func TestAStreamThatEndsEarlyStillClosesItsCount(t *testing.T) {
	counts := &countingObservers{}
	// A cursor that will not parse: the handler answers 400 and returns before
	// the loop begins. Every early return has to unwind the gauge, or a client
	// that never really connected is counted forever.
	handler := NewObserved(counts.observers(), silenceConfig(), &fakeStore{}, operator(), newFakeSilencer())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream?cursor=not-a-number", nil)
	request.Header.Set("Authorization", "Bearer session-token")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if counts.opened != counts.closed {
		t.Errorf("opened %d, closed %d; the gauge would drift upward by the difference",
			counts.opened, counts.closed)
	}
}

func TestAStreamCountsItsPollsAndReleasesTheClient(t *testing.T) {
	counts := &countingObservers{}
	store := &fakeStore{}
	handler := NewObserved(counts.observers(), silenceConfig(), store, operator(), newFakeSilencer())

	// fakeStore cancels the request context on its first StreamEvents call, so
	// the loop polls once and then unwinds the way a disconnect would.
	ctx, cancel := context.WithCancel(context.Background())
	store.cancel = cancel
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer session-token")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if counts.opened != 1 {
		t.Errorf("opened = %d, want 1", counts.opened)
	}
	if counts.closed != 1 {
		t.Errorf("closed = %d, want 1; a disconnect must release the client", counts.closed)
	}
	// Every open console does this on a timer whether or not anything happened,
	// which is exactly why it is worth counting.
	if counts.polls < 1 {
		t.Errorf("polls = %d, want at least one read recorded", counts.polls)
	}
}

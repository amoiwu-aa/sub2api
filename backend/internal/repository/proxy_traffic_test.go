package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

type proxyTrafficTestRecord struct {
	proxyID       int64
	uploadBytes   int64
	downloadBytes int64
}

type proxyTrafficTestRecorder struct {
	mu      sync.Mutex
	records []proxyTrafficTestRecord
}

func (r *proxyTrafficTestRecorder) ResolveProxyID(context.Context, int64, string) int64 {
	return 1
}

func (r *proxyTrafficTestRecorder) Record(proxyID, uploadBytes, downloadBytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, proxyTrafficTestRecord{
		proxyID:       proxyID,
		uploadBytes:   uploadBytes,
		downloadBytes: downloadBytes,
	})
}

func (r *proxyTrafficTestRecorder) Stop() {}

type proxyTrafficTestRoundTripper func(*http.Request) (*http.Response, error)

func (fn proxyTrafficTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestProxyTrafficRoundTripperRecordsRequestAndResponseBodies(t *testing.T) {
	recorder := &proxyTrafficTestRecorder{}
	transport := &proxyTrafficRoundTripper{
		proxyID:  8,
		recorder: recorder,
		base: proxyTrafficTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			if _, err := io.ReadAll(req.Body); err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("response")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", strings.NewReader("request"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}

	if len(recorder.records) != 1 {
		t.Fatalf("expected 1 traffic record, got %d", len(recorder.records))
	}
	got := recorder.records[0]
	if got.proxyID != 8 || got.uploadBytes != int64(len("request")) || got.downloadBytes != int64(len("response")) {
		t.Fatalf("unexpected record: %#v", got)
	}
}

func TestProxyTrafficRoundTripperRecordsPartialUploadOnTransportError(t *testing.T) {
	recorder := &proxyTrafficTestRecorder{}
	transport := &proxyTrafficRoundTripper{
		proxyID:  4,
		recorder: recorder,
		base: proxyTrafficTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			_, _ = io.ReadAll(req.Body)
			return nil, errors.New("upstream unavailable")
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.com", strings.NewReader("failed"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected transport error")
	}
	if len(recorder.records) != 1 {
		t.Fatalf("expected 1 traffic record, got %d", len(recorder.records))
	}
	got := recorder.records[0]
	if got.proxyID != 4 || got.uploadBytes != int64(len("failed")) || got.downloadBytes != 0 {
		t.Fatalf("unexpected record: %#v", got)
	}
}

func TestProxyTrafficRecorderPersist(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectExec("UPDATE proxies AS p").
		WithArgs(int64(3), "2026-08-06", int64(12), int64(34)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	recorder := &proxyTrafficRecorder{db: db}
	err = recorder.persist(context.Background(), []proxyTrafficDelta{{
		proxyID:       3,
		day:           "2026-08-06",
		uploadBytes:   12,
		downloadBytes: 34,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

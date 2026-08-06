package service

import (
	"testing"
	"time"
)

func TestProxyTrafficTodayClearsStaleDailyCounters(t *testing.T) {
	proxy := &Proxy{
		TrafficTodayUploadBytes:   12,
		TrafficTodayDownloadBytes: 34,
		TrafficTodayDate:          time.Now().UTC().AddDate(0, 0, -1),
	}
	uploadBytes, downloadBytes := proxy.TrafficToday()
	if uploadBytes != 0 || downloadBytes != 0 {
		t.Fatalf("expected stale counters to be hidden, got upload=%d download=%d", uploadBytes, downloadBytes)
	}
}

func TestProxyTrafficTodayReturnsCurrentDailyCounters(t *testing.T) {
	proxy := &Proxy{
		TrafficTodayUploadBytes:   12,
		TrafficTodayDownloadBytes: 34,
		TrafficTodayDate:          time.Now().UTC(),
	}
	uploadBytes, downloadBytes := proxy.TrafficToday()
	if uploadBytes != 12 || downloadBytes != 34 {
		t.Fatalf("unexpected current counters: upload=%d download=%d", uploadBytes, downloadBytes)
	}
}

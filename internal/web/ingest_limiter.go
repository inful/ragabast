package web

import (
	"net/http"
	"strconv"
	"time"
)

// IngestLimiter provides a simple non-blocking semaphore for ingestion endpoints.
//
// When saturated, callers should return HTTP 429 with a Retry-After header.
// This avoids piling up long-running ingestion requests and protects downstream
// dependencies like Ollama and the vector DB.
type IngestLimiter struct {
	sem        chan struct{}
	retryAfter time.Duration
}

func NewIngestLimiter(maxConcurrent int, retryAfter time.Duration) *IngestLimiter {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	if retryAfter <= 0 {
		retryAfter = 1 * time.Second
	}
	return &IngestLimiter{
		sem:        make(chan struct{}, maxConcurrent),
		retryAfter: retryAfter,
	}
}

func (l *IngestLimiter) TryAcquire() bool {
	if l == nil {
		return true
	}
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *IngestLimiter) Release() {
	if l == nil {
		return
	}
	select {
	case <-l.sem:
		return
	default:
		return
	}
}

func (l *IngestLimiter) RetryAfterSeconds() int {
	if l == nil {
		return 1
	}
	secs := int(l.retryAfter.Seconds())
	if secs <= 0 {
		secs = 1
	}
	return secs
}

func (l *IngestLimiter) RetryAfterHeader() http.Header {
	h := make(http.Header)
	h.Set("Retry-After", strconv.Itoa(l.RetryAfterSeconds()))
	return h
}

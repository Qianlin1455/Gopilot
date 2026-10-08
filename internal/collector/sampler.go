package collector

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	DefaultSampleInterval = time.Second
	DefaultHistorySize    = 300
)

type RuntimeSource interface {
	Runtime(context.Context) (RuntimeSample, RequestMeta, error)
}

type CollectionError struct {
	At      time.Time `json:"at"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

type SamplerStatus struct {
	LastAttempt time.Time        `json:"last_attempt,omitempty"`
	LastSuccess time.Time        `json:"last_success,omitempty"`
	LastError   *CollectionError `json:"last_error,omitempty"`
	Stored      int              `json:"stored_samples"`
	Capacity    int              `json:"capacity"`
}

type Sampler struct {
	mu       sync.RWMutex
	source   RuntimeSource
	interval time.Duration
	buffer   []RuntimeSample
	next     int
	count    int
	status   SamplerStatus
}

func NewSampler(source RuntimeSource, interval time.Duration, capacity int) (*Sampler, error) {
	if source == nil {
		return nil, fmt.Errorf("runtime source is required")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("sample interval must be positive")
	}
	if capacity < 2 {
		return nil, fmt.Errorf("history capacity must be at least 2")
	}
	return &Sampler{
		source:   source,
		interval: interval,
		buffer:   make([]RuntimeSample, capacity),
		status:   SamplerStatus{Capacity: capacity},
	}, nil
}

func (s *Sampler) Run(ctx context.Context) {
	s.SampleOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.SampleOnce(ctx)
		}
	}
}

func (s *Sampler) SampleOnce(ctx context.Context) {
	attemptedAt := time.Now().UTC()
	sample, _, err := s.source.Runtime(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastAttempt = attemptedAt
	if err != nil {
		s.status.LastError = &CollectionError{At: attemptedAt, Code: "collection_failed", Message: err.Error()}
		return
	}
	if sample.Timestamp.IsZero() {
		sample.Timestamp = attemptedAt
	}
	s.buffer[s.next] = sample
	s.next = (s.next + 1) % len(s.buffer)
	if s.count < len(s.buffer) {
		s.count++
	}
	s.status.LastSuccess = sample.Timestamp
	s.status.LastError = nil
	s.status.Stored = s.count
}

func (s *Sampler) Recent(window time.Duration, now time.Time) ([]RuntimeSample, SamplerStatus) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.snapshotLocked()
	cutoff := now.Add(-window)
	first := 0
	for first < len(all) && all[first].Timestamp.Before(cutoff) {
		first++
	}
	result := append([]RuntimeSample(nil), all[first:]...)
	status := s.status
	if s.status.LastError != nil {
		lastError := *s.status.LastError
		status.LastError = &lastError
	}
	return result, status
}

func (s *Sampler) Status() SamplerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	if s.status.LastError != nil {
		lastError := *s.status.LastError
		status.LastError = &lastError
	}
	return status
}

func (s *Sampler) Interval() time.Duration {
	return s.interval
}

func (s *Sampler) snapshotLocked() []RuntimeSample {
	result := make([]RuntimeSample, 0, s.count)
	start := 0
	if s.count == len(s.buffer) {
		start = s.next
	}
	for index := 0; index < s.count; index++ {
		result = append(result, s.buffer[(start+index)%len(s.buffer)])
	}
	return result
}

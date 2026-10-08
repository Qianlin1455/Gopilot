package collector

import (
	"context"
	"errors"
	"testing"
	"time"
)

type sequenceSource struct {
	samples []RuntimeSample
	errors  []error
	index   int
}

func (s *sequenceSource) Runtime(context.Context) (RuntimeSample, RequestMeta, error) {
	index := s.index
	s.index++
	if index < len(s.errors) && s.errors[index] != nil {
		return RuntimeSample{}, RequestMeta{}, s.errors[index]
	}
	if index >= len(s.samples) {
		return RuntimeSample{}, RequestMeta{}, errors.New("no configured sample")
	}
	return s.samples[index], RequestMeta{HTTPStatus: 200}, nil
}

func TestSamplerKeepsBoundedChronologicalHistory(t *testing.T) {
	base := time.Now().UTC().Add(-time.Minute)
	source := &sequenceSource{samples: []RuntimeSample{
		{Timestamp: base, Goroutines: 1},
		{Timestamp: base.Add(time.Second), Goroutines: 2},
		{Timestamp: base.Add(2 * time.Second), Goroutines: 3},
		{Timestamp: base.Add(3 * time.Second), Goroutines: 4},
	}}
	sampler, err := NewSampler(source, time.Second, 3)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	for range 4 {
		sampler.SampleOnce(context.Background())
	}

	samples, status := sampler.Recent(time.Hour, base.Add(time.Minute))
	if len(samples) != 3 {
		t.Fatalf("stored samples = %d, want 3", len(samples))
	}
	for index, want := range []int{2, 3, 4} {
		if samples[index].Goroutines != want {
			t.Fatalf("samples[%d].Goroutines = %d, want %d", index, samples[index].Goroutines, want)
		}
	}
	if status.Stored != 3 || status.Capacity != 3 {
		t.Fatalf("status = %+v, want stored=3 capacity=3", status)
	}
}

func TestSamplerRecordsFailureWithoutAddingFakeSample(t *testing.T) {
	now := time.Now().UTC()
	source := &sequenceSource{
		samples: []RuntimeSample{{Timestamp: now, Goroutines: 8}},
		errors:  []error{nil, errors.New("temporary outage")},
	}
	sampler, err := NewSampler(source, time.Second, 5)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	sampler.SampleOnce(context.Background())
	sampler.SampleOnce(context.Background())

	samples, status := sampler.Recent(time.Minute, now.Add(time.Second))
	if len(samples) != 1 {
		t.Fatalf("stored samples = %d, want 1", len(samples))
	}
	if status.LastError == nil || status.LastError.Code != "collection_failed" {
		t.Fatalf("last error = %+v, want structured collection failure", status.LastError)
	}
}

func TestSamplerFiltersRecentWindow(t *testing.T) {
	base := time.Now().UTC().Add(-10 * time.Second)
	source := &sequenceSource{samples: []RuntimeSample{
		{Timestamp: base, Goroutines: 5},
		{Timestamp: base.Add(6 * time.Second), Goroutines: 6},
		{Timestamp: base.Add(9 * time.Second), Goroutines: 7},
	}}
	sampler, err := NewSampler(source, time.Second, 5)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	for range 3 {
		sampler.SampleOnce(context.Background())
	}
	samples, _ := sampler.Recent(5*time.Second, base.Add(10*time.Second))
	if len(samples) != 2 || samples[0].Goroutines != 6 || samples[1].Goroutines != 7 {
		t.Fatalf("recent samples = %+v, want goroutine counts [6 7]", samples)
	}
}

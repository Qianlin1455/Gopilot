package tools

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"gopilot/internal/collector"
)

type fakeClient struct {
	health  collector.Health
	runtime collector.RuntimeSample
	leak    collector.LeakStatus
	profile collector.ProfilePayload
	err     error
}

func (f *fakeClient) Health(context.Context) (collector.Health, collector.RequestMeta, error) {
	return f.health, collector.RequestMeta{HTTPStatus: http.StatusOK, Latency: 2 * time.Millisecond}, f.err
}

func (f *fakeClient) Runtime(context.Context) (collector.RuntimeSample, collector.RequestMeta, error) {
	return f.runtime, collector.RequestMeta{HTTPStatus: http.StatusOK}, f.err
}

func (f *fakeClient) LeakStatus(context.Context) (collector.LeakStatus, collector.RequestMeta, error) {
	return f.leak, collector.RequestMeta{HTTPStatus: http.StatusOK}, f.err
}

func (f *fakeClient) GoroutineProfile(context.Context, int) (collector.ProfilePayload, error) {
	return f.profile, f.err
}

type staticRuntimeSource struct {
	sample collector.RuntimeSample
}

func (s staticRuntimeSource) Runtime(context.Context) (collector.RuntimeSample, collector.RequestMeta, error) {
	return s.sample, collector.RequestMeta{HTTPStatus: http.StatusOK}, nil
}

func newTestSampler(t *testing.T, sample collector.RuntimeSample) *collector.Sampler {
	t.Helper()
	sampler, err := collector.NewSampler(staticRuntimeSource{sample: sample}, time.Second, 10)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	return sampler
}

func TestCalculateRuntimeMetrics(t *testing.T) {
	base := time.Now().UTC().Add(-5 * time.Second)
	samples := []collector.RuntimeSample{
		{Timestamp: base, Goroutines: 10, HeapAllocBytes: 1000, HeapObjects: 100},
		{Timestamp: base.Add(2 * time.Second), Goroutines: 35, HeapAllocBytes: 1600, HeapObjects: 130},
	}
	data := calculateRuntimeMetrics(samples, collector.SamplerStatus{}, 5, base.Add(5*time.Second), time.Second)
	if data.Goroutines.Start != 10 || data.Goroutines.End != 35 || data.Goroutines.AbsoluteDelta != 25 {
		t.Fatalf("goroutine trend = %+v", data.Goroutines)
	}
	if data.Goroutines.Minimum != 10 || data.Goroutines.Maximum != 35 || data.Goroutines.GrowthRatePerSecond != 12.5 {
		t.Fatalf("goroutine calculation = %+v", data.Goroutines)
	}
	if data.Heap.DeltaAllocBytes != 600 || data.Heap.DeltaObjects != 30 {
		t.Fatalf("heap trend = %+v", data.Heap)
	}
}

func TestRuntimeMetricsReportsInsufficientHistory(t *testing.T) {
	now := time.Now().UTC()
	samples := []collector.RuntimeSample{{Timestamp: now, Goroutines: 10}}
	data := calculateRuntimeMetrics(samples, collector.SamplerStatus{}, 30, now, time.Second)
	if !data.InsufficientHistory || data.InsufficientReason == "" {
		t.Fatalf("insufficient history fields = %+v", data)
	}
}

func TestInvalidToolArguments(t *testing.T) {
	now := time.Now().UTC()
	sampler := newTestSampler(t, collector.RuntimeSample{Timestamp: now})
	service := NewService(&fakeClient{}, sampler)

	metrics := service.GetRuntimeMetrics(MinMetricsWindow - 1)
	if metrics.OK || metrics.Error == nil || metrics.Error.Code != "invalid_argument" {
		t.Fatalf("metrics envelope = %+v", metrics)
	}
	profile := service.GetGoroutineProfile(context.Background(), MaxProfileTopN+1)
	if profile.OK || profile.Error == nil || profile.Error.Code != "invalid_argument" {
		t.Fatalf("profile envelope = %+v", profile)
	}
}

func TestDemoServiceUnavailableReturnsStructuredError(t *testing.T) {
	client, err := collector.NewClient("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sampler := newTestSampler(t, collector.RuntimeSample{Timestamp: time.Now().UTC()})
	service := NewService(client, sampler)
	envelope := service.GetServiceStatus(context.Background())
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "target_unavailable" {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestGoroutineProfileBoundsTopN(t *testing.T) {
	fixture := `goroutine 1 [chan receive]:
example.com/one()
	/one.go:1

goroutine 2 [select]:
example.com/two()
	/two.go:2

goroutine 3 [IO wait]:
example.com/three()
	/three.go:3
`
	sampler := newTestSampler(t, collector.RuntimeSample{Timestamp: time.Now().UTC()})
	client := &fakeClient{profile: collector.ProfilePayload{
		Body:       []byte(fixture),
		RawBytes:   len(fixture),
		LimitBytes: collector.DefaultProfileLimit,
		Meta:       collector.RequestMeta{HTTPStatus: 200},
	}}
	service := NewService(client, sampler)
	envelope := service.GetGoroutineProfile(context.Background(), 2)
	if !envelope.OK {
		t.Fatalf("profile envelope error = %+v", envelope.Error)
	}
	data := envelope.Data.(GoroutineProfileData)
	if len(data.Groups) != 2 || !data.Truncation.GroupsTruncated || data.Truncation.TotalGroups != 3 {
		t.Fatalf("profile data = %+v", data)
	}
}

func TestEnvelopeFactoryCreatesGoPilotObservationIDs(t *testing.T) {
	factory := NewEnvelopeFactory()
	fixed := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	factory.now = func() time.Time { return fixed }
	first := factory.Success("test_tool", map[string]bool{"real": true})
	second := factory.Failure("test_tool", "test_error", "failed", nil)
	if first.ObservationID == "" || first.ObservationID == second.ObservationID {
		t.Fatalf("observation IDs = %q and %q", first.ObservationID, second.ObservationID)
	}
	if first.CapturedAt != fixed || second.CapturedAt != fixed || !first.OK || second.OK {
		t.Fatalf("envelopes = %+v %+v", first, second)
	}
}

func TestRuntimeMetricsReturnsErrorWhenCollectionHasNoEvidence(t *testing.T) {
	source := &failingRuntimeSource{err: errors.New("service offline")}
	sampler, err := collector.NewSampler(source, time.Second, 5)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	sampler.SampleOnce(context.Background())
	service := NewService(&fakeClient{}, sampler)
	envelope := service.GetRuntimeMetrics(5)
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "evidence_unavailable" {
		t.Fatalf("envelope = %+v", envelope)
	}
}

type failingRuntimeSource struct{ err error }

func (f *failingRuntimeSource) Runtime(context.Context) (collector.RuntimeSample, collector.RequestMeta, error) {
	return collector.RuntimeSample{}, collector.RequestMeta{}, f.err
}

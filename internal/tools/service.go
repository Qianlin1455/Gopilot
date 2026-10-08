package tools

import (
	"context"
	"errors"
	"math"
	"time"

	"gopilot/internal/collector"
)

const (
	ServiceStatusTool    = "get_service_status"
	RuntimeMetricsTool   = "get_runtime_metrics"
	GoroutineProfileTool = "get_goroutine_profile"
	MinMetricsWindow     = 5
	MaxMetricsWindow     = 120
	DefaultMetricsWindow = 30
	MinProfileTopN       = 1
	MaxProfileTopN       = 20
	DefaultProfileTopN   = 10
)

type EvidenceClient interface {
	Health(context.Context) (collector.Health, collector.RequestMeta, error)
	Runtime(context.Context) (collector.RuntimeSample, collector.RequestMeta, error)
	LeakStatus(context.Context) (collector.LeakStatus, collector.RequestMeta, error)
	GoroutineProfile(context.Context, int) (collector.ProfilePayload, error)
}

type Service struct {
	client  EvidenceClient
	sampler *collector.Sampler
	factory *EnvelopeFactory
}

func NewService(client EvidenceClient, sampler *collector.Sampler) *Service {
	return &Service{client: client, sampler: sampler, factory: NewEnvelopeFactory()}
}

func (s *Service) Failure(tool, code, message string, details map[string]any) Envelope {
	return s.factory.Failure(tool, code, message, details)
}

type ServiceStatusData struct {
	ServiceIdentity       string    `json:"service_identity"`
	HealthStatus          string    `json:"health_status"`
	HTTPStatus            int       `json:"http_status"`
	RequestLatencyMillis  float64   `json:"request_latency_ms"`
	UptimeMillis          int64     `json:"uptime_ms"`
	CurrentGoroutines     int       `json:"current_goroutine_count"`
	ActiveIntentionalLeak int       `json:"active_intentional_leak_count"`
	ServiceStartedAt      time.Time `json:"service_started_at"`
}

func (s *Service) GetServiceStatus(ctx context.Context) Envelope {
	health, meta, err := s.client.Health(ctx)
	if err != nil {
		return s.upstreamFailure(ServiceStatusTool, "health", err)
	}
	runtimeSample, _, err := s.client.Runtime(ctx)
	if err != nil {
		return s.upstreamFailure(ServiceStatusTool, "runtime", err)
	}
	leakStatus, _, err := s.client.LeakStatus(ctx)
	if err != nil {
		return s.upstreamFailure(ServiceStatusTool, "leak_status", err)
	}
	return s.factory.Success(ServiceStatusTool, ServiceStatusData{
		ServiceIdentity:       health.Service,
		HealthStatus:          health.Status,
		HTTPStatus:            meta.HTTPStatus,
		RequestLatencyMillis:  durationMillis(meta.Latency),
		UptimeMillis:          runtimeSample.UptimeMillis,
		CurrentGoroutines:     runtimeSample.Goroutines,
		ActiveIntentionalLeak: leakStatus.Active,
		ServiceStartedAt:      health.StartedAt,
	})
}

type GoroutineTrend struct {
	Start               int     `json:"start"`
	End                 int     `json:"end"`
	Minimum             int     `json:"minimum"`
	Maximum             int     `json:"maximum"`
	AbsoluteDelta       int     `json:"absolute_delta"`
	GrowthRatePerSecond float64 `json:"growth_rate_per_second"`
}

type HeapTrend struct {
	StartAllocBytes uint64 `json:"start_alloc_bytes"`
	EndAllocBytes   uint64 `json:"end_alloc_bytes"`
	DeltaAllocBytes int64  `json:"delta_alloc_bytes"`
	StartObjects    uint64 `json:"start_objects"`
	EndObjects      uint64 `json:"end_objects"`
	DeltaObjects    int64  `json:"delta_objects"`
}

type CaptureWindow struct {
	RequestedStart   time.Time `json:"requested_start"`
	ActualStart      time.Time `json:"actual_start,omitempty"`
	ActualEnd        time.Time `json:"actual_end,omitempty"`
	RequestedSeconds int       `json:"requested_seconds"`
	ActualSeconds    float64   `json:"actual_seconds"`
}

type RuntimeMetricsData struct {
	Samples             []collector.RuntimeSample `json:"samples"`
	SampleCount         int                       `json:"sample_count"`
	Goroutines          GoroutineTrend            `json:"goroutines"`
	Heap                HeapTrend                 `json:"heap"`
	Window              CaptureWindow             `json:"capture_window"`
	InsufficientHistory bool                      `json:"insufficient_history"`
	InsufficientReason  string                    `json:"insufficient_history_reason,omitempty"`
	Collection          collector.SamplerStatus   `json:"collection"`
}

func (s *Service) GetRuntimeMetrics(windowSeconds int) Envelope {
	if windowSeconds < MinMetricsWindow || windowSeconds > MaxMetricsWindow {
		return s.factory.Failure(RuntimeMetricsTool, "invalid_argument", "window_seconds must be between 5 and 120", map[string]any{
			"argument": "window_seconds",
			"minimum":  MinMetricsWindow,
			"maximum":  MaxMetricsWindow,
			"received": windowSeconds,
		})
	}

	now := time.Now().UTC()
	window := time.Duration(windowSeconds) * time.Second
	samples, collectionStatus := s.sampler.Recent(window, now)
	if len(samples) == 0 && collectionStatus.LastError != nil {
		return s.factory.Failure(RuntimeMetricsTool, "evidence_unavailable", "no runtime samples are available", map[string]any{
			"collection": collectionStatus,
		})
	}

	data := calculateRuntimeMetrics(samples, collectionStatus, windowSeconds, now, s.sampler.Interval())
	return s.factory.Success(RuntimeMetricsTool, data)
}

func calculateRuntimeMetrics(samples []collector.RuntimeSample, status collector.SamplerStatus, windowSeconds int, now time.Time, interval time.Duration) RuntimeMetricsData {
	data := RuntimeMetricsData{
		Samples:     samples,
		SampleCount: len(samples),
		Window: CaptureWindow{
			RequestedStart:   now.Add(-time.Duration(windowSeconds) * time.Second),
			RequestedSeconds: windowSeconds,
		},
		Collection: status,
	}
	if len(samples) == 0 {
		data.InsufficientHistory = true
		data.InsufficientReason = "no samples in the requested window"
		return data
	}

	first := samples[0]
	last := samples[len(samples)-1]
	minimum, maximum := first.Goroutines, first.Goroutines
	for _, sample := range samples[1:] {
		minimum = min(minimum, sample.Goroutines)
		maximum = max(maximum, sample.Goroutines)
	}
	actualDuration := last.Timestamp.Sub(first.Timestamp)
	growthRate := 0.0
	if actualDuration > 0 {
		growthRate = float64(last.Goroutines-first.Goroutines) / actualDuration.Seconds()
	}
	data.Goroutines = GoroutineTrend{
		Start:               first.Goroutines,
		End:                 last.Goroutines,
		Minimum:             minimum,
		Maximum:             maximum,
		AbsoluteDelta:       last.Goroutines - first.Goroutines,
		GrowthRatePerSecond: math.Round(growthRate*1000) / 1000,
	}
	data.Heap = HeapTrend{
		StartAllocBytes: first.HeapAllocBytes,
		EndAllocBytes:   last.HeapAllocBytes,
		DeltaAllocBytes: int64(last.HeapAllocBytes) - int64(first.HeapAllocBytes),
		StartObjects:    first.HeapObjects,
		EndObjects:      last.HeapObjects,
		DeltaObjects:    int64(last.HeapObjects) - int64(first.HeapObjects),
	}
	data.Window.ActualStart = first.Timestamp
	data.Window.ActualEnd = last.Timestamp
	data.Window.ActualSeconds = math.Round(actualDuration.Seconds()*1000) / 1000

	if len(samples) < 2 {
		data.InsufficientHistory = true
		data.InsufficientReason = "at least two samples are required to calculate a trend"
	} else if first.Timestamp.After(data.Window.RequestedStart.Add(interval * 2)) {
		data.InsufficientHistory = true
		data.InsufficientReason = "collector does not yet cover the full requested window"
	}
	return data
}

type ProfileTruncation struct {
	InputTruncated      bool `json:"input_truncated"`
	GroupsTruncated     bool `json:"groups_truncated"`
	RawBytes            int  `json:"raw_bytes"`
	RawLimitBytes       int  `json:"raw_limit_bytes"`
	TotalGroups         int  `json:"total_groups"`
	ReturnedGroups      int  `json:"returned_groups"`
	FramesPerGroupLimit int  `json:"frames_per_group_limit"`
}

type GoroutineProfileData struct {
	TotalGoroutines      int                    `json:"total_goroutine_count"`
	Groups               []collector.StackGroup `json:"stack_groups"`
	RequestLatencyMillis float64                `json:"request_latency_ms"`
	Truncation           ProfileTruncation      `json:"truncation"`
}

func (s *Service) GetGoroutineProfile(ctx context.Context, topN int) Envelope {
	if topN < MinProfileTopN || topN > MaxProfileTopN {
		return s.factory.Failure(GoroutineProfileTool, "invalid_argument", "top_n must be between 1 and 20", map[string]any{
			"argument": "top_n",
			"minimum":  MinProfileTopN,
			"maximum":  MaxProfileTopN,
			"received": topN,
		})
	}
	payload, err := s.client.GoroutineProfile(ctx, collector.DefaultProfileLimit)
	if err != nil {
		return s.upstreamFailure(GoroutineProfileTool, "goroutine_profile", err)
	}
	parsed := collector.ParseGoroutineProfile(payload.Body, collector.DefaultMaxFramesPerGroup)
	returnedGroups := parsed.Groups
	if len(returnedGroups) > topN {
		returnedGroups = returnedGroups[:topN]
	}
	return s.factory.Success(GoroutineProfileTool, GoroutineProfileData{
		TotalGoroutines:      parsed.TotalGoroutines,
		Groups:               returnedGroups,
		RequestLatencyMillis: durationMillis(payload.Meta.Latency),
		Truncation: ProfileTruncation{
			InputTruncated:      payload.InputTruncated,
			GroupsTruncated:     len(parsed.Groups) > len(returnedGroups),
			RawBytes:            payload.RawBytes,
			RawLimitBytes:       payload.LimitBytes,
			TotalGroups:         len(parsed.Groups),
			ReturnedGroups:      len(returnedGroups),
			FramesPerGroupLimit: collector.DefaultMaxFramesPerGroup,
		},
	})
}

func (s *Service) upstreamFailure(tool, stage string, err error) Envelope {
	code := "target_unavailable"
	details := map[string]any{"stage": stage}
	var httpError *collector.HTTPError
	if errors.As(err, &httpError) && httpError.StatusCode != 0 {
		code = "upstream_http_error"
		details["http_status"] = httpError.StatusCode
	}
	return s.factory.Failure(tool, code, err.Error(), details)
}

func durationMillis(duration time.Duration) float64 {
	return math.Round(float64(duration.Microseconds())/1000*1000) / 1000
}

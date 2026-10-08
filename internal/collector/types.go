package collector

import "time"

type RuntimeSample struct {
	Timestamp      time.Time `json:"timestamp"`
	GoVersion      string    `json:"go_version"`
	Goroutines     int       `json:"goroutines"`
	HeapAllocBytes uint64    `json:"heap_alloc_bytes"`
	HeapObjects    uint64    `json:"heap_objects"`
	UptimeMillis   int64     `json:"uptime_ms"`
}

type Health struct {
	Status    string    `json:"status"`
	Service   string    `json:"service"`
	StartedAt time.Time `json:"started_at"`
}

type LeakStatus struct {
	Active     int    `json:"active"`
	Generation uint64 `json:"generation"`
	MaxBatch   int    `json:"max_batch"`
	MaxActive  int    `json:"max_active"`
}

type RequestMeta struct {
	HTTPStatus int           `json:"http_status"`
	Latency    time.Duration `json:"-"`
}

type ProfilePayload struct {
	Body           []byte
	RawBytes       int
	LimitBytes     int
	InputTruncated bool
	Meta           RequestMeta
}

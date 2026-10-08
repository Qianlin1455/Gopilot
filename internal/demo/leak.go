package demo

import (
	"errors"
	"sync"
)

const (
	DefaultLeakCount = 25
	MaxLeakBatch     = 100
	MaxActiveLeaks   = 500
)

var (
	ErrInvalidLeakCount = errors.New("leak count must be between 1 and 100")
	ErrTooManyLeaks     = errors.New("active leak limit would be exceeded")
)

type LeakStatus struct {
	Active     int    `json:"active"`
	Generation uint64 `json:"generation"`
	MaxBatch   int    `json:"max_batch"`
	MaxActive  int    `json:"max_active"`
}

type leakGeneration struct {
	stop   chan struct{}
	wg     sync.WaitGroup
	active int
	id     uint64
}

// LeakController owns only the goroutines intentionally leaked by this demo.
// Each reset swaps generations and closes the old generation's stop channel.
type LeakController struct {
	mu      sync.Mutex
	current *leakGeneration
}

func NewLeakController() *LeakController {
	return &LeakController{current: newLeakGeneration(1)}
}

func newLeakGeneration(id uint64) *leakGeneration {
	return &leakGeneration{stop: make(chan struct{}), id: id}
}

func (c *LeakController) Inject(count int) (LeakStatus, error) {
	if count < 1 || count > MaxLeakBatch {
		return c.Status(), ErrInvalidLeakCount
	}

	c.mu.Lock()
	generation := c.current
	if generation.active+count > MaxActiveLeaks {
		status := c.statusLocked()
		c.mu.Unlock()
		return status, ErrTooManyLeaks
	}

	ready := make(chan struct{}, count)
	generation.active += count
	generation.wg.Add(count)
	for range count {
		go c.waitForever(generation.stop, ready, &generation.wg)
	}
	status := c.statusLocked()
	c.mu.Unlock()

	// Do not report success until every goroutine has entered waitForever.
	for range count {
		<-ready
	}
	return status, nil
}

// waitForever is deliberately named so the root cause is unambiguous in a
// real goroutine profile. It blocks until Reset closes its generation channel.
func (c *LeakController) waitForever(stop <-chan struct{}, ready chan<- struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	ready <- struct{}{}
	<-stop
}

func (c *LeakController) Reset() LeakStatus {
	c.mu.Lock()
	previous := c.current
	c.current = newLeakGeneration(previous.id + 1)
	close(previous.stop)
	status := c.statusLocked()
	c.mu.Unlock()

	previous.wg.Wait()
	return status
}

func (c *LeakController) Status() LeakStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *LeakController) statusLocked() LeakStatus {
	return LeakStatus{
		Active:     c.current.active,
		Generation: c.current.id,
		MaxBatch:   MaxLeakBatch,
		MaxActive:  MaxActiveLeaks,
	}
}

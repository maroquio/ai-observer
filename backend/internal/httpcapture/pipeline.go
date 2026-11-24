package httpcapture

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tobilg/ai-observer/internal/logger"
	"github.com/tobilg/ai-observer/internal/selfmetrics"
	"github.com/tobilg/ai-observer/internal/storage"
)

// Store is the storage the capture writes to (storage.DuckDBStore)
type Store interface {
	StoreHTTPCaptureBatch(ctx context.Context, b storage.HTTPCaptureBatch) error
}

// Notifier announces stored exchanges to live clients (the WebSocket hub)
type Notifier interface {
	HTTPExchangesStored(runIDs []string, timestamps []time.Time)
}

// job is a capture record waiting to be stored
type job struct {
	started  *storage.HTTPExchangeRecord
	finished *storage.HTTPExchangeFinish
}

// Pipeline stores capture records in the background. Enqueue never blocks:
// when the queue is full the record is dropped and counted, so a slow disk
// or database never delays the proxied traffic. One writer stores the
// queued records in batches, one transaction per batch, in arrival order.
type Pipeline struct {
	store    Store
	notifier Notifier
	queue    chan job
	stats    *Stats

	closeOnce sync.Once
	done      chan struct{}
	stop      chan struct{}
	closed    atomic.Bool
	mu        sync.Mutex // serializes Enqueue against Close
}

// maxBatch bounds the records stored in one transaction
const maxBatch = 64

// NewPipeline starts a pipeline with a queue of size records
func NewPipeline(store Store, notifier Notifier, size int, stats *Stats) *Pipeline {
	p := &Pipeline{
		store:    store,
		notifier: notifier,
		queue:    make(chan job, size),
		stats:    stats,
		done:     make(chan struct{}),
		stop:     make(chan struct{}),
	}
	selfmetrics.HTTPCaptureQueueCapacity.With().Set(int64(size))
	go p.run()
	return p
}

// enqueue queues j unless the queue is full or closed; it reports whether
// it was queued
func (p *Pipeline) enqueue(j job) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed.Load() {
		p.stats.droppedRecords.Add(1)
		selfmetrics.HTTPCaptureDropped.With("closed").Inc()
		return false
	}
	select {
	case p.queue <- j:
		selfmetrics.HTTPCaptureQueueLength.With().Set(int64(len(p.queue)))
		return true
	default:
		p.stats.droppedRecords.Add(1)
		selfmetrics.HTTPCaptureDropped.With("queue_full").Inc()
		return false
	}
}

func (p *Pipeline) run() {
	defer close(p.done)
	for {
		select {
		case j, ok := <-p.queue:
			if !ok {
				return
			}
			p.storeBatch(p.collect(j))
		case <-p.stop:
			// Drain what is queued, then stop
			for {
				select {
				case j, ok := <-p.queue:
					if !ok {
						return
					}
					p.storeBatch(p.collect(j))
				default:
					return
				}
			}
		}
	}
}

// collect takes first and up to maxBatch-1 more queued records
func (p *Pipeline) collect(first job) storage.HTTPCaptureBatch {
	var b storage.HTTPCaptureBatch
	add := func(j job) {
		if j.started != nil {
			b.Started = append(b.Started, *j.started)
		}
		if j.finished != nil {
			b.Finished = append(b.Finished, *j.finished)
		}
	}
	add(first)
	for n := 1; n < maxBatch; n++ {
		select {
		case j, ok := <-p.queue:
			if !ok {
				return b
			}
			add(j)
		default:
			return b
		}
	}
	return b
}

// storeBatch stores b; a failure is counted and logged, the traffic it
// describes has already been forwarded
func (p *Pipeline) storeBatch(b storage.HTTPCaptureBatch) {
	selfmetrics.HTTPCaptureQueueLength.With().Set(int64(len(p.queue)))
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := p.store.StoreHTTPCaptureBatch(ctx, b)
	cancel()
	selfmetrics.HTTPCapturePersistSeconds.With().ObserveSince(start)
	if err != nil {
		n := len(b.Started) + len(b.Finished)
		p.stats.storeErrors.Add(int64(n))
		selfmetrics.HTTPCaptureErrors.With("store").Add(n)
		logger.Warn("HTTP capture: storing exchanges failed; the proxied traffic was not affected", "records", n, "error", err)
		return
	}
	if p.notifier == nil {
		return
	}
	var runs []string
	var times []time.Time
	seen := map[string]bool{}
	note := func(e storage.HTTPExchangeRecord) {
		if !seen[e.Exchange.RunID] {
			seen[e.Exchange.RunID] = true
			runs = append(runs, e.Exchange.RunID)
		}
		times = append(times, e.Exchange.StartedAt)
	}
	for _, s := range b.Started {
		note(s)
	}
	for _, f := range b.Finished {
		note(f.Record)
	}
	p.notifier.HTTPExchangesStored(runs, times)
}

// Close stops accepting records and stores those queued, until ctx ends
func (p *Pipeline) Close(ctx context.Context) error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed.Store(true)
		p.mu.Unlock()
		close(p.stop)
	})
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

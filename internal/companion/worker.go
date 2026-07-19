package companion

import (
	"context"
	"errors"
	"sync"
	"time"
)

// SubscriberWorker drives a Subscriber over a set of followed publishers on a
// ticker (plus an immediate trigger when a new follow is added).
type SubscriberWorker struct {
	sub *Subscriber

	mu        sync.Mutex
	follows   map[[32]byte]string     // pubkey → label
	lastSync  map[[32]byte]SyncResult // pubkey → last outcome
	totalRuns int

	trigger chan struct{} // buffered cap 1
	stopCh  chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// NewSubscriberWorker wraps a Subscriber.
func NewSubscriberWorker(sub *Subscriber) (*SubscriberWorker, error) {
	if sub == nil {
		return nil, errors.New("companion: nil subscriber")
	}
	return &SubscriberWorker{
		sub:      sub,
		follows:  make(map[[32]byte]string),
		lastSync: make(map[[32]byte]SyncResult),
		trigger:  make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
	}, nil
}

// Follow adds (or relabels) a followed publisher and nudges an immediate sync.
func (w *SubscriberWorker) Follow(pubkey [32]byte, label string) {
	w.mu.Lock()
	w.follows[pubkey] = label
	w.mu.Unlock()
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

// Unfollow drops a followed publisher and its last-sync record. An in-flight
// sync runs to completion and its result is dropped (runOnce re-checks).
func (w *SubscriberWorker) Unfollow(pubkey [32]byte) {
	w.mu.Lock()
	delete(w.follows, pubkey)
	delete(w.lastSync, pubkey)
	w.mu.Unlock()
	w.sub.Forget(pubkey) // prune the Subscriber's dedup maps (no unbounded leak)
}

// Following returns a snapshot of the follow-set.
func (w *SubscriberWorker) Following() map[[32]byte]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[[32]byte]string, len(w.follows))
	for k, v := range w.follows {
		out[k] = v
	}
	return out
}

// LastSync returns the last outcome for pubkey (zero value if never synced).
func (w *SubscriberWorker) LastSync(pubkey [32]byte) SyncResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastSync[pubkey]
}

// TotalRuns returns the number of completed sweep cycles.
func (w *SubscriberWorker) TotalRuns() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.totalRuns
}

// Start launches the worker goroutine (idempotent).
func (w *SubscriberWorker) Start() {
	w.startOnce.Do(func() {
		w.wg.Add(1)
		go w.run()
	})
}

// Stop signals the worker and waits for it (idempotent). The run-context is
// cancelled so an in-flight Sync is not left blocking up to FetchTimeout.
func (w *SubscriberWorker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *SubscriberWorker) run() {
	defer w.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-w.stopCh
		cancel()
	}()
	w.runOnce(ctx)
	tick := time.NewTicker(w.sub.opts.Interval)
	defer tick.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-tick.C:
			w.runOnce(ctx)
		case <-w.trigger:
			w.runOnce(ctx)
		}
	}
}

// runOnce sweeps every followed publisher once. A publisher unfollowed during
// its (slow) Sync has its result dropped by the post-sync membership re-check.
func (w *SubscriberWorker) runOnce(ctx context.Context) {
	w.mu.Lock()
	pubs := make([][32]byte, 0, len(w.follows))
	for pk := range w.follows {
		pubs = append(pubs, pk)
	}
	w.mu.Unlock()
	for _, pub := range pubs {
		select {
		case <-w.stopCh:
			return
		default:
		}
		res := w.sub.Sync(ctx, pub)
		w.mu.Lock()
		if _, ok := w.follows[pub]; ok {
			w.lastSync[pub] = res
		}
		w.mu.Unlock()
	}
	w.mu.Lock()
	w.totalRuns++
	w.mu.Unlock()
}

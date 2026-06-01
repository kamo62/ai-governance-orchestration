package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ChainAppender wraps an audit Store and maintains a per-session tamper-evident
// hash chain. It automatically sets PrevEventHash and EventHash on every appended
// event without requiring callers to manage chain state.
//
// ChainAppender implements the full audit.Store interface and can be used
// anywhere audit.Store is required, including in the Governance Shell,
// Orchestrator, model proxy and MCP proxy.
type ChainAppender struct {
	store    Store
	lastHash map[string]string
	mu       sync.Mutex
	now      func() time.Time
}

// NewChainAppender wraps the given store with hash-chain tracking.
func NewChainAppender(store Store) *ChainAppender {
	return &ChainAppender{
		store:    store,
		lastHash: make(map[string]string),
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// Append records the event through the underlying store after populating
// PrevEventHash and EventHash. It is safe for concurrent use.
func (c *ChainAppender) Append(ctx context.Context, event Event) (Event, error) {
	if c == nil || c.store == nil {
		return Event{}, errors.New("chain appender: store is nil")
	}
	if event.EventID == "" {
		return Event{}, errors.New("chain appender: event_id is required")
	}
	if event.EventType == "" {
		return Event{}, errors.New("chain appender: event_type is required")
	}
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}

	// Ensure RecordedAt is set before persisting so the hash is stable
	// regardless of whether the store back-fills it.
	if event.RecordedAt.IsZero() {
		now := c.now
		if now == nil {
			now = func() time.Time { return time.Now().UTC() }
		}
		event.RecordedAt = now().UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	type compareAppender interface {
		AppendIfPrev(context.Context, Event, string) (Event, bool, error)
	}
	if store, ok := c.store.(compareAppender); ok && event.SessionID != "" {
		for attempt := 0; attempt < 3; attempt++ {
			latest, err := c.latestHash(ctx, event.SessionID)
			if err != nil {
				return Event{}, err
			}
			candidate := event
			candidate.PrevEventHash = latest
			candidate.EventHash = canonicalHash(candidate)
			persisted, appended, err := store.AppendIfPrev(ctx, candidate, latest)
			if err != nil {
				return Event{}, fmt.Errorf("chain append: %w", err)
			}
			if appended {
				c.lastHash[persisted.SessionID] = persisted.EventHash
				return persisted, nil
			}
		}
		return Event{}, errors.New("chain append: concurrent append conflict")
	}

	// Link to the latest persisted hash for this session. This is process-local
	// for stores that do not expose a compare-and-append primitive.
	if event.SessionID != "" {
		latest, err := c.latestHash(ctx, event.SessionID)
		if err != nil {
			return Event{}, err
		}
		event.PrevEventHash = latest
	}
	event.EventHash = canonicalHash(event)

	persisted, err := c.store.Append(ctx, event)
	if err != nil {
		return Event{}, fmt.Errorf("chain append: %w", err)
	}
	if persisted.SessionID != "" && persisted.EventHash != "" {
		c.lastHash[persisted.SessionID] = persisted.EventHash
	}

	return persisted, nil
}

func (c *ChainAppender) latestHash(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	events, err := c.store.EventsBySession(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("load previous audit hash: %w", err)
	}
	if len(events) == 0 {
		return "", nil
	}
	latest := events[len(events)-1].EventHash
	c.lastHash[sessionID] = latest
	return latest, nil
}

// EventsBySession delegates to the underlying store.
func (c *ChainAppender) EventsBySession(ctx context.Context, sessionID string) ([]Event, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("chain appender: store is nil")
	}
	return c.store.EventsBySession(ctx, sessionID)
}

// AllEvents delegates to the underlying store if it supports it.
func (c *ChainAppender) AllEvents(ctx context.Context) ([]Event, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("chain appender: store is nil")
	}
	type allEventStore interface {
		AllEvents(context.Context) ([]Event, error)
	}
	if store, ok := c.store.(allEventStore); ok {
		return store.AllEvents(ctx)
	}
	return nil, errors.New("chain appender: underlying store does not support AllEvents")
}

// RetentionPolicy applies chain-aware audit retention. Linked session events
// must be purged as whole chains, otherwise verification of surviving events is
// permanently broken.
func (c *ChainAppender) RetentionPolicy(ctx context.Context, maxAge time.Duration) (int64, error) {
	if c == nil || c.store == nil {
		return 0, errors.New("chain appender: store is nil")
	}
	type chainRetentionPurger interface {
		ChainRetentionPolicy(context.Context, time.Duration) (int64, error)
	}
	store, ok := c.store.(chainRetentionPurger)
	if !ok {
		return 0, errors.New("chain appender: underlying store does not support chain-aware retention")
	}
	return store.ChainRetentionPolicy(ctx, maxAge)
}

// canonicalHash returns a SHA-256 hex hash of the event's canonical JSON
// representation with EventHash cleared.  The hash includes PrevEventHash
// so that reordering is detectable.
func canonicalHash(event Event) string {
	e := event
	e.EventHash = ""
	data, err := json.Marshal(e)
	if err != nil {
		// json.Marshal on this struct should never fail, but if it does
		// return a sentinel that still permits verification to detect
		// mismatches.
		return "sha256:ERR"
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// VerifyChain checks that the provided events form a valid tamper-evident
// chain for their session.  It returns an error if any event hash does not
// match its canonical form, if prev-event-hash linkage is broken, or if the
// events are reordered.
//
// The caller is responsible for ensuring the slice is ordered by occurrence
// (e.g. by RecordedAt ascending).
func VerifyChain(events []Event) error {
	if len(events) == 0 {
		return nil
	}

	var prevHash string
	for i, event := range events {
		expectedHash := canonicalHash(event)
		if event.EventHash != expectedHash {
			return fmt.Errorf("event %d (%s): hash mismatch: computed %s, stored %s", i, event.EventID, expectedHash, event.EventHash)
		}

		if i == 0 {
			if event.PrevEventHash != "" {
				return fmt.Errorf("event %d (%s): first event in chain must have empty prev_event_hash", i, event.EventID)
			}
		} else {
			if event.PrevEventHash != prevHash {
				return fmt.Errorf("event %d (%s): prev hash mismatch: expected %s, got %s", i, event.EventID, prevHash, event.PrevEventHash)
			}
		}

		prevHash = event.EventHash
	}

	return nil
}

// VerifyChainForSession loads all events for the given session from the
// store and runs VerifyChain on them.
func VerifyChainForSession(ctx context.Context, store Store, sessionID string) error {
	events, err := store.EventsBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	return VerifyChain(events)
}

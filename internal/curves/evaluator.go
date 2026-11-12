package curves

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

// CurveEvaluator evaluates every curve exactly once per cycle, in dependency order (the sub-curves of a
// function curve before the function curve), from a single goroutine. This includes curves no fan uses.
// Each curve stores its result (see SpeedCurve.CurrentResult), which the fan controllers, the REST API
// and the statistics read.
//
// After each cycle, it notifies all subscribers (see Subscribe), so the fan controllers act on the values
// of the cycle right away, and all fans that use the same curve see the same value.
type CurveEvaluator struct {
	// curves in evaluation order
	order    []SpeedCurve
	tickRate time.Duration
	// clock is the time source of the ticker (nil means util.RealClock)
	clock util.Clock

	// cycleMu serializes the cycles, so the curves are evaluated by one goroutine at a time
	// even if RunCycle is called while Run is active
	cycleMu sync.Mutex

	mu          sync.Mutex
	subscribers []chan struct{}
}

// NewCurveEvaluator returns a CurveEvaluator for the given curves, which runs a cycle every tickRate.
// It returns an error if a function curve references a curve that isn't in curves, or if the references
// form a cycle (config validation rejects both, so this is a safety net).
func NewCurveEvaluator(curves []SpeedCurve, tickRate time.Duration) (*CurveEvaluator, error) {
	if tickRate <= 0 {
		return nil, fmt.Errorf("invalid curve evaluation tick rate %s", tickRate)
	}
	order, err := evaluationOrder(curves)
	if err != nil {
		return nil, err
	}
	return &CurveEvaluator{
		order:    order,
		tickRate: tickRate,
	}, nil
}

// evaluationOrder sorts curves topologically: every curve comes after the curves it depends on.
// Curves without a dependency between them are sorted by id, so the order is deterministic.
func evaluationOrder(curves []SpeedCurve) ([]SpeedCurve, error) {
	byId := make(map[string]SpeedCurve, len(curves))
	ids := make([]string, 0, len(curves))
	for _, curve := range curves {
		if curve == nil {
			continue
		}
		if _, exists := byId[curve.GetId()]; exists {
			return nil, fmt.Errorf("duplicate curve id '%s'", curve.GetId())
		}
		byId[curve.GetId()] = curve
		ids = append(ids, curve.GetId())
	}
	sort.Strings(ids)

	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(ids))
	order := make([]SpeedCurve, 0, len(ids))
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		switch state[id] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("curve dependency cycle: %s", strings.Join(append(path, id), " -> "))
		}
		state[id] = visiting
		curve := byId[id]
		if dependent, ok := curve.(interface{ dependencies() []string }); ok {
			for _, dependency := range dependent.dependencies() {
				if _, exists := byId[dependency]; !exists {
					return fmt.Errorf("curve '%s' references unknown curve '%s'", id, dependency)
				}
				if err := visit(dependency, append(path, id)); err != nil {
					return err
				}
			}
		}
		state[id] = done
		order = append(order, curve)
		return nil
	}
	for _, id := range ids {
		if err := visit(id, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// SetClock replaces the time source of the evaluator and of all its curves that support it
// (used for the staleness check of sensor values and the time of the results). Intended for tests;
// call it before Run.
func (e *CurveEvaluator) SetClock(clock util.Clock) {
	e.clock = clock
	for _, curve := range e.order {
		if c, ok := curve.(interface{ SetClock(util.Clock) }); ok {
			c.SetClock(clock)
		}
	}
}

func (e *CurveEvaluator) getClock() util.Clock {
	return getClock(e.clock)
}

// Subscribe returns a channel that receives a notification after each cycle. The channel has a buffer
// of one notification: if the subscriber hasn't received the previous one yet, the new one is dropped,
// so a slow subscriber never blocks the evaluator, and finds at most one pending notification.
func (e *CurveEvaluator) Subscribe() <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan struct{}, 1)
	e.subscribers = append(e.subscribers, ch)
	return ch
}

// Run runs a cycle right away, and then one every tickRate, until ctx is done.
func (e *CurveEvaluator) Run(ctx context.Context) error {
	ticker := e.getClock().NewTicker(e.tickRate)
	defer ticker.Stop()

	e.RunCycle()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			e.RunCycle()
		}
	}
}

// RunCycle evaluates every curve once, in dependency order, and then notifies the subscribers.
func (e *CurveEvaluator) RunCycle() {
	e.cycleMu.Lock()
	defer e.cycleMu.Unlock()
	clock := e.getClock()
	start := clock.Now()
	for _, curve := range e.order {
		e.evaluate(curve)
	}
	if duration := clock.Now().Sub(start); duration > e.tickRate {
		ui.Debug("Evaluating all curves took %s, longer than the tick rate of %s", duration, e.tickRate)
	}
	e.notify()
}

// evaluate evaluates a single curve. A panic is logged and stored as the error of the curve's result,
// so the fans using it apply their onSensorFailure action, and the other curves are still evaluated.
func (e *CurveEvaluator) evaluate(curve SpeedCurve) {
	defer func() {
		if r := recover(); r != nil {
			ui.Error("Panic while evaluating curve %s: %v\n%s", curve.GetId(), r, debug.Stack())
			if store, ok := curve.(resultStore); ok {
				_, _ = store.storeResult(0, fmt.Errorf("panic while evaluating curve %s: %v", curve.GetId(), r), e.getClock().Now())
			}
		}
	}()
	_, err := curve.Evaluate()
	if err != nil {
		// the fan controllers using the curve log the failure (once, not every cycle)
		ui.Debug("Error evaluating curve %s: %v", curve.GetId(), err)
	}
}

func (e *CurveEvaluator) notify() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ch := range e.subscribers {
		select {
		case ch <- struct{}{}:
		default:
			// the subscriber hasn't handled the previous notification yet
		}
	}
}

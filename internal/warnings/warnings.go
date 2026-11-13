// Package warnings holds the problems fan2go detected about its configured items (fans, sensors, curves) that the
// user should know about, so that the REST API can show them besides the log.
package warnings

import "sync"

// Kind is the kind of item a warning is about.
type Kind string

const (
	KindFan    Kind = "fan"
	KindSensor Kind = "sensor"
	KindCurve  Kind = "curve"
)

// Code is a stable identifier of the kind of a warning. API clients match on it, so existing codes must not change.
type Code string

const (
	// CodeFlatRpmCurve is set on a fan while its RPM curve data is flat, i.e. the fan doesn't react to PWM
	// (see fans.IsFlatRpmCurve).
	CodeFlatRpmCurve Code = "flatRpmCurve"
)

// Warning is a problem fan2go detected about an item.
type Warning struct {
	// Code identifies the kind of warning.
	Code Code `json:"code"`
	// Message is the human-readable description of the warning.
	Message string `json:"message"`
}

// Entry is a warning together with the item it is about.
type Entry struct {
	Kind    Kind   `json:"kind"`
	ID      string `json:"id"`
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// Store is the set of warnings that currently apply to the items of a config, keyed by (kind, id, code).
// It holds state, not history: there is at most one warning per code and item, and a warning is removed when its
// condition goes away.
// Safe for concurrent use; its lock is a leaf lock. All methods are no-ops on a nil Store (and the getters return
// empty lists), so code that runs without one (e.g. CLI commands, tests) doesn't need to check.
type Store struct {
	mu sync.RWMutex
	// in the order the warnings were first set
	entries []Entry
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{}
}

// Set adds the warning with the given code to the item, or replaces the message of the existing warning with that
// code (it keeps its position).
func (s *Store) Set(kind Kind, id string, code Code, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.indexOf(kind, id, code); i >= 0 {
		s.entries[i].Message = message
		return
	}
	s.entries = append(s.entries, Entry{Kind: kind, ID: id, Code: code, Message: message})
}

// Clear removes the warning with the given code from the item, a no-op if it isn't set.
func (s *Store) Clear(kind Kind, id string, code Code) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.indexOf(kind, id, code); i >= 0 {
		s.entries = append(s.entries[:i], s.entries[i+1:]...)
	}
}

// Has returns whether the item has the warning with the given code.
func (s *Store) Has(kind Kind, id string, code Code) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexOf(kind, id, code) >= 0
}

// For returns the current warnings of the item, in the order they were first set. Never nil, so that the REST API
// shows an empty list rather than null.
func (s *Store) For(kind Kind, id string) []Warning {
	result := []Warning{}
	if s == nil {
		return result
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.entries {
		if entry.Kind == kind && entry.ID == id {
			result = append(result, Warning{Code: entry.Code, Message: entry.Message})
		}
	}
	return result
}

// All returns all current warnings, in the order they were first set. Never nil.
func (s *Store) All() []Entry {
	if s == nil {
		return []Entry{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Entry, len(s.entries))
	copy(result, s.entries)
	return result
}

// indexOf returns the index of the warning in entries, -1 if it isn't set. Must be called with mu held.
func (s *Store) indexOf(kind Kind, id string, code Code) int {
	for i, entry := range s.entries {
		if entry.Kind == kind && entry.ID == id && entry.Code == code {
			return i
		}
	}
	return -1
}

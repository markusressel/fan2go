package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/markusressel/fan2go/internal/ui"
	bolt "go.etcd.io/bbolt"
)

const BucketFanOriginalState = "fanOriginalState"

// originalStateDbTimeout is how long the original state functions wait for the database lock.
// Shorter than for the analysis data: they run right before fan2go takes over a fan and right after
// it handed the fan back, and a locked database must not delay that for long (they are best effort).
var originalStateDbTimeout = 5 * time.Second

// FanOriginalState is the state a fan had before fan2go took control of it. It is saved when fan2go
// takes over the fan and deleted once fan2go handed the fan back, so an entry that is found on start
// means that the last run didn't hand the fan back (e.g. because it was killed).
type FanOriginalState struct {
	// Hardware identifies the device the state belongs to (see controller.originalStateHardwareKey)
	Hardware string `json:"hardware"`
	// ControlMode is the fans.ControlMode, nil if unknown
	ControlMode *int `json:"controlMode"`
	// AutomaticControlMode is the driver specific value behind the automatic control mode
	// (e.g. pwm_enable=5 of hwmon fans), nil if unknown or not applicable
	AutomaticControlMode *int `json:"automaticControlMode,omitempty"`
	// Pwm is the raw pwm value (no pwmMap applied), nil if unknown
	Pwm *int `json:"pwm"`
}

func (s FanOriginalState) validate() error {
	// only fans.ControlModeDisabled, ControlModePWM and ControlModeAutomatic are saved
	if s.ControlMode != nil && (*s.ControlMode < 0 || *s.ControlMode > 2) {
		return fmt.Errorf("invalid control mode %d", *s.ControlMode)
	}
	if s.Pwm != nil && (*s.Pwm < 0 || *s.Pwm > 255) {
		return fmt.Errorf("invalid pwm value %d", *s.Pwm)
	}
	return nil
}

// SaveFanOriginalState saves the original state of the given fan, overwriting a saved one.
func (p persistence) SaveFanOriginalState(fanId string, state FanOriginalState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	db, err := p.openPersistenceWithTimeout(originalStateDbTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(BucketFanOriginalState))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		return b.Put([]byte(fanId), data)
	})
}

// LoadFanOriginalState loads the saved original state of the given fan.
// Returns os.ErrNotExist if there is none, and ErrCorruptData (after deleting it) if it can't be read.
func (p persistence) LoadFanOriginalState(fanId string) (FanOriginalState, error) {
	db, err := p.openPersistenceWithTimeout(originalStateDbTimeout)
	if err != nil {
		return FanOriginalState{}, err
	}
	defer func() { _ = db.Close() }()

	var state FanOriginalState
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanOriginalState))
		if b == nil {
			return os.ErrNotExist
		}
		v := b.Get([]byte(fanId))
		if v == nil {
			return os.ErrNotExist
		}
		decoded, decodeErr := decodeFanOriginalState(fanId, v)
		state = decoded
		return decodeErr
	})
	if errors.Is(err, ErrCorruptData) {
		deleteCorruptKey(db, BucketFanOriginalState, fanId)
		return FanOriginalState{}, err
	}
	if err != nil {
		return FanOriginalState{}, err
	}
	return state, nil
}

// LoadFanOriginalStates loads the saved original states of all fans, by fan id.
// Corrupt entries are skipped, logged and deleted.
func (p persistence) LoadFanOriginalStates() (map[string]FanOriginalState, error) {
	db, err := p.openPersistenceWithTimeout(originalStateDbTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	result := map[string]FanOriginalState{}
	var corrupt []string
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanOriginalState))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			state, err := decodeFanOriginalState(string(k), v)
			if err == nil {
				result[string(k)] = state
			} else {
				corrupt = append(corrupt, string(k))
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	for _, key := range corrupt {
		deleteCorruptKey(db, BucketFanOriginalState, key)
	}
	return result, nil
}

// DeleteFanOriginalState deletes the saved original state of the given fan (a no-op if there is none).
func (p persistence) DeleteFanOriginalState(fanId string) error {
	db, err := p.openPersistenceWithTimeout(originalStateDbTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanOriginalState))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(fanId))
	})
}

func decodeFanOriginalState(fanId string, v []byte) (FanOriginalState, error) {
	var state FanOriginalState
	err := json.Unmarshal(v, &state)
	if err == nil {
		err = state.validate()
	}
	if err != nil {
		ui.Warning("Unable to read saved original state of fan %s: %v", fanId, err)
		return FanOriginalState{}, fmt.Errorf("%w: original state of %s: %v", ErrCorruptData, fanId, err)
	}
	return state, nil
}

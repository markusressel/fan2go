package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/markusressel/fan2go/internal/ui"
	bolt "go.etcd.io/bbolt"
)

const BucketFanAnalysisInfo = "fanAnalysisInfo"

// FanAnalysisInfo describes how the saved analysis data of a fan (RPM curve, setPwmToGetPwmMap, pwmMap)
// was measured. If it differs from the current one, the data is measured again.
type FanAnalysisInfo struct {
	// Quirk is the id of the known hardware quirks entry that applied to the fan, "" if none
	Quirk string `json:"quirk"`
	// QuirkVersion is the version of that entry, 0 if none
	QuirkVersion int `json:"quirkVersion"`
	// ReadbackDelayMillis is the longest time (in milliseconds) the fan took to report a written PWM value when
	// it was read back, measured during the analysis. nil if it wasn't measured (e.g. data saved by an older
	// version, or a fan whose PWM can't be read). It doesn't invalidate the analysis data when it changes.
	ReadbackDelayMillis *int64 `json:"readbackDelayMillis,omitempty"`
	// BreakawayPwm is the lowest PWM value (before the pwmMap) that started the fan from standstill during the
	// analysis. nil if it wasn't measured (e.g. data saved by an older version, a fan that doesn't stop, or
	// kickStart.mode isn't auto). It doesn't invalidate the analysis data when it changes.
	BreakawayPwm *int `json:"breakawayPwm,omitempty"`
	// TachIntervalMillis is the time (in milliseconds) between two updates of the RPM value the fan reports,
	// measured during the breakaway measurement. nil if it wasn't measured. Used to time the kick start.
	TachIntervalMillis *int64 `json:"tachIntervalMillis,omitempty"`
	// SpinUpMillis is the time (in milliseconds) from writing the breakaway PWM value (once the fan reported it)
	// to the first RPM reading that showed the fan spinning, when it started from standstill. nil if it wasn't
	// measured. Used to time the kick start.
	SpinUpMillis *int64 `json:"spinUpMillis,omitempty"`
	// CoastDownMillis is the longest time (in milliseconds) the fan took from a write of a PWM value that stops it
	// until its RPM readings showed it standing still, measured during the breakaway measurement. nil if it wasn't
	// measured. Used to time the kick start.
	CoastDownMillis *int64 `json:"coastDownMillis,omitempty"`
}

// SaveFanAnalysisInfo saves the analysis info of the given fan, overwriting a saved one.
func (p persistence) SaveFanAnalysisInfo(fanId string, info FanAnalysisInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(BucketFanAnalysisInfo))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		return b.Put([]byte(fanId), data)
	})
}

// LoadFanAnalysisInfo loads the analysis info of the given fan.
// Returns os.ErrNotExist if there is none, and ErrCorruptData (after deleting it) if it can't be read.
func (p persistence) LoadFanAnalysisInfo(fanId string) (FanAnalysisInfo, error) {
	db, err := p.openPersistence()
	if err != nil {
		return FanAnalysisInfo{}, err
	}
	defer func() { _ = db.Close() }()

	var info FanAnalysisInfo
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanAnalysisInfo))
		if b == nil {
			return os.ErrNotExist
		}
		v := b.Get([]byte(fanId))
		if v == nil {
			return os.ErrNotExist
		}
		if err := json.Unmarshal(v, &info); err != nil {
			ui.Warning("Unable to read saved analysis info of fan %s: %v", fanId, err)
			return fmt.Errorf("%w: analysis info of %s: %v", ErrCorruptData, fanId, err)
		}
		return nil
	})
	if errors.Is(err, ErrCorruptData) {
		deleteCorruptKey(db, BucketFanAnalysisInfo, fanId)
		return FanAnalysisInfo{}, err
	}
	if err != nil {
		return FanAnalysisInfo{}, err
	}
	return info, nil
}

// DeleteFanAnalysisInfo deletes the analysis info of the given fan (a no-op if there is none).
func (p persistence) DeleteFanAnalysisInfo(fanId string) error {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanAnalysisInfo))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(fanId))
	})
}

// FanAnalysisData is the complete analysis data of a fan, see ReplaceFanAnalysisData.
type FanAnalysisData struct {
	// RpmCurve is the RPM curve, nil for a fan without RPM curve (saved as null)
	RpmCurve map[int]float64
	// SetPwmToGetPwmMap is the measured setPwmToGetPwmMap, nil if none is saved (config, readback formula)
	SetPwmToGetPwmMap map[int]int
	// PwmMap is the computed pwmMap (256 values), nil if none is saved (configured)
	PwmMap []int
	// Info is the analysis info, nil if none is saved
	Info *FanAnalysisInfo
}

// ReplaceFanAnalysisData replaces all saved analysis data of the given fan (RPM curve, setPwmToGetPwmMap, pwmMap,
// analysis info) in a single transaction: either everything is replaced, or nothing changes. Parts that are nil
// are deleted (except the RPM curve, which is saved as null).
func (p persistence) ReplaceFanAnalysisData(fanId string, data FanAnalysisData) error {
	if data.PwmMap != nil && len(data.PwmMap) != 256 {
		return errors.New("pwmMapping slice must have exactly 256 elements")
	}
	values := map[string][]byte{}
	var err error
	if values[BucketFans], err = json.Marshal(data.RpmCurve); err != nil {
		return err
	}
	if data.SetPwmToGetPwmMap != nil {
		if values[BucketFanSetPwmToSetPwmMap], err = json.Marshal(data.SetPwmToGetPwmMap); err != nil {
			return err
		}
	}
	if data.PwmMap != nil {
		if values[BucketFanPwmMap], err = json.Marshal(data.PwmMap); err != nil {
			return err
		}
	}
	if data.Info != nil {
		if values[BucketFanAnalysisInfo], err = json.Marshal(*data.Info); err != nil {
			return err
		}
	}

	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	return db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range []string{BucketFans, BucketFanSetPwmToSetPwmMap, BucketFanPwmMap, BucketFanAnalysisInfo} {
			b, err := tx.CreateBucketIfNotExists([]byte(bucket))
			if err != nil {
				return fmt.Errorf("create bucket: %s", err)
			}
			if value, ok := values[bucket]; ok {
				err = b.Put([]byte(fanId), value)
			} else {
				err = b.Delete([]byte(fanId))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

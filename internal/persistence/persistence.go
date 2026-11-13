package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/ui"
	bolt "go.etcd.io/bbolt"
)

const (
	BucketFans                 = "fans"
	BucketFanPwmMap            = "fanPwmMapping"
	BucketFanSetPwmToSetPwmMap = "fanSetPwmToGetPwmMap"
)

// ErrCorruptData is returned by the Load* functions if the stored data of a fan can't be read.
// The corrupt entry has been deleted (if possible) by then, so callers should treat it like
// missing data (os.ErrNotExist), e.g. by running the fan initialization again.
var ErrCorruptData = errors.New("corrupt data")

// IsMissingOrCorrupt returns true if err means that there is no usable stored data:
// it doesn't exist (os.ErrNotExist) or it was corrupt (ErrCorruptData).
// Any other error (e.g. the database can't be opened) means the data may exist, but couldn't be read.
func IsMissingOrCorrupt(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrCorruptData)
}

type Persistence interface {
	Init() error

	LoadFanRpmData(fan fans.Fan) (map[int]float64, error)
	SaveFanRpmData(fan fans.Fan) (err error)
	DeleteFanRpmData(fan fans.Fan) (err error)

	LoadFanSetPwmToGetPwmMap(fanId string) (map[int]int, error)
	SaveFanSetPwmToGetPwmMap(fanId string, pwmMap map[int]int) (err error)
	DeleteFanSetPwmToGetPwmMap(fanId string) (err error)

	// the returned slice has exactly 256 elements (unless there was an error and it's nil)
	LoadFanPwmMap(fanId string) ([]int, error)
	// pwmMapping must have exactly 256 elements (it's an array mapping PWM i to pwmMapping[i] for PWMs 0-255)
	SaveFanPwmMap(fanId string, pwmMapping []int) (err error)
	DeleteFanPwmMap(fanId string) (err error)

	// the state of a fan before fan2go took control of it, see FanOriginalState
	LoadFanOriginalState(fanId string) (FanOriginalState, error)
	LoadFanOriginalStates() (map[string]FanOriginalState, error)
	SaveFanOriginalState(fanId string, state FanOriginalState) error
	DeleteFanOriginalState(fanId string) error

	// how the analysis data of a fan was measured, see FanAnalysisInfo
	LoadFanAnalysisInfo(fanId string) (FanAnalysisInfo, error)
	SaveFanAnalysisInfo(fanId string, info FanAnalysisInfo) error
	DeleteFanAnalysisInfo(fanId string) error

	// replaces all analysis data of a fan in one transaction, see FanAnalysisData
	ReplaceFanAnalysisData(fanId string, data FanAnalysisData) error
}

type persistence struct {
	dbPath string
}

func NewPersistence(dbPath string) Persistence {
	p := &persistence{
		dbPath: dbPath,
	}
	return p
}

func (p persistence) Init() (err error) {
	// get parent path of dbPath
	parentDir := filepath.Dir(p.dbPath)
	_, err = os.Stat(parentDir)
	if errors.Is(err, os.ErrNotExist) {
		// create directory
		ui.Info("Creating directory for db: %s", parentDir)
		err = os.MkdirAll(parentDir, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p persistence) openPersistence() (db *bolt.DB, err error) {
	return p.openPersistenceWithTimeout(1 * time.Minute)
}

// openPersistenceWithTimeout opens the database, waiting at most timeout for its file lock.
func (p persistence) openPersistenceWithTimeout(timeout time.Duration) (db *bolt.DB, err error) {
	db, err = bolt.Open(p.dbPath, 0600, &bolt.Options{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// SaveFanPwmData saves the fan curve data of the given fan to persistence
func (p persistence) SaveFanRpmData(fan fans.Fan) (err error) {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)

	key := fan.GetId()

	// convert the curve data moving window to a map to arrays, so we can persist them
	fanCurveDataMap := map[int]float64{}

	if curveData := fan.GetFanRpmCurveData(); curveData == nil {
		fanCurveDataMap = nil
	} else {
		for key, value := range *curveData {
			fanCurveDataMap[key] = value
		}
	}

	data, err := json.Marshal(fanCurveDataMap)
	if err != nil {
		return err
	}

	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(BucketFans))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		err = b.Put([]byte(key), data)
		return err
	})
}

// LoadFanPwmData loads the fan curve data from persistence
func (p persistence) LoadFanRpmData(fan fans.Fan) (map[int]float64, error) {
	db, err := p.openPersistence()
	if err != nil {
		return nil, err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)

	key := fan.GetId()

	var fanCurveDataMap map[int]float64
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFans))
		if b == nil {
			return os.ErrNotExist
		}
		v := b.Get([]byte(key))
		if v == nil {
			return os.ErrNotExist
		}

		err := json.Unmarshal(v, &fanCurveDataMap)
		if err != nil {
			ui.Warning("Unable to unmarshal saved fan data for %s: %v", key, err)
			return fmt.Errorf("%w: fan data of %s: %v", ErrCorruptData, key, err)
		}

		return nil
	})
	if errors.Is(err, ErrCorruptData) {
		// if we cannot read the saved data, delete it
		// (in a separate, writable transaction: View transactions are read-only)
		deleteCorruptKey(db, BucketFans, key)
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	return fanCurveDataMap, nil
}

// deleteCorruptKey deletes the corrupt entry key from bucket, logging (but otherwise ignoring) errors:
// the entry is overwritten anyway, once the data has been computed again.
func deleteCorruptKey(db *bolt.DB, bucket string, key string) {
	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
	if err != nil {
		ui.Error("Unable to delete corrupt data key %s: %v", key, err)
	}
}

func (p persistence) DeleteFanRpmData(fan fans.Fan) error {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)

	key := fan.GetId()

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFans))
		if b == nil {
			// no fan bucket yet
			return nil
		}
		v := b.Get([]byte(key))
		if v == nil {
			// no data for given key
			return nil
		}

		return b.Delete([]byte(key))
	})
}

// LoadFanSetPwmToGetPwmMap loads the "pwm requested" -> "actual pwm" map of the given fan from persistence
func (p persistence) LoadFanSetPwmToGetPwmMap(fanId string) (map[int]int, error) {
	db, err := p.openPersistence()
	if err != nil {
		return nil, err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)
	return p.loadFanSetPwmToGetPwmMap(db, fanId)
}

// loadFanSetPwmToGetPwmMap loads the "pwm requested" -> "actual pwm" map of the given fan from persistence
func (p persistence) loadFanSetPwmToGetPwmMap(db *bolt.DB, fanId string) (map[int]int, error) {
	key := fanId

	var pwmMap map[int]int
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanSetPwmToSetPwmMap))
		if b == nil {
			return os.ErrNotExist
		}
		v := b.Get([]byte(key))
		if v == nil {
			return os.ErrNotExist
		}

		err := json.Unmarshal(v, &pwmMap)
		if err != nil {
			ui.Warning("Unable to unmarshal saved setPwmToGetPwmMap data for %s: %v", key, err)
			return fmt.Errorf("%w: setPwmToGetPwmMap of %s: %v", ErrCorruptData, key, err)
		}

		return nil
	})
	if errors.Is(err, ErrCorruptData) {
		// if we cannot read the saved data, delete it
		deleteCorruptKey(db, BucketFanSetPwmToSetPwmMap, key)
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	if pwmMap != nil && len(pwmMap) <= 0 {
		// remove invalid data from the db
		fanId := fanId
		_ = p.deleteFanSetPwmToGetPwmMap(db, fanId)
		return nil, os.ErrNotExist
	}

	return pwmMap, nil
}

// SaveFanSetPwmToGetPwmMap saves the "pwm requested" -> "actual pwm" map of the given fan to persistence
func (p persistence) SaveFanSetPwmToGetPwmMap(fanId string, pwmMap map[int]int) (err error) {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)

	key := fanId

	data, err := json.Marshal(pwmMap)
	if err != nil {
		return err
	}

	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(BucketFanSetPwmToSetPwmMap))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		err = b.Put([]byte(key), data)
		return err
	})
}

// DeleteFanSetPwmToGetPwmMap deletes the "pwm requested" -> "actual pwm" map of the given fan from persistence
func (p persistence) DeleteFanSetPwmToGetPwmMap(fanId string) error {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)
	return p.deleteFanSetPwmToGetPwmMap(db, fanId)
}

func (p persistence) deleteFanSetPwmToGetPwmMap(db *bolt.DB, fanId string) error {
	key := fanId

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanSetPwmToSetPwmMap))
		if b == nil {
			// no fan bucket yet
			return nil
		}
		v := b.Get([]byte(key))
		if v == nil {
			// no data for given key
			return nil
		}

		return b.Delete([]byte(key))
	})
}

// SaveFanPwmMap saves the "pwm requested" -> "actual pwm" map of the given fan to persistence
func (p persistence) SaveFanPwmMap(fanId string, pwmMapping []int) (err error) {
	if len(pwmMapping) != 256 {
		return errors.New("pwmMapping slice must have exactly 256 elements")
	}
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)

	key := fanId

	data, err := json.Marshal(pwmMapping)
	if err != nil {
		return err
	}

	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(BucketFanPwmMap))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		err = b.Put([]byte(key), data)
		return err
	})
}

// LoadFanPwmMap loads the fan curve data from persistence
func (p persistence) LoadFanPwmMap(fanId string) ([]int, error) {
	db, err := p.openPersistence()
	if err != nil {
		return nil, err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)

	key := fanId

	var pwmMapping []int
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanPwmMap))
		if b == nil {
			return os.ErrNotExist
		}
		v := b.Get([]byte(key))
		if v == nil {
			return os.ErrNotExist
		}

		err := json.Unmarshal(v, &pwmMapping)
		if err != nil {
			ui.Warning("Unable to unmarshal saved pwmMap data for %s: %v", key, err)
			return fmt.Errorf("%w: pwmMap of %s: %v", ErrCorruptData, key, err)
		}
		if len(pwmMapping) != 256 {
			ui.Warning("Saved pwmMap data for %s has %d elements instead of 256", key, len(pwmMapping))
			return fmt.Errorf("%w: pwmMap of %s has %d elements instead of 256", ErrCorruptData, key, len(pwmMapping))
		}

		return nil
	})
	if errors.Is(err, ErrCorruptData) {
		// if we cannot read the saved data, delete it
		deleteCorruptKey(db, BucketFanPwmMap, key)
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	return pwmMapping, nil
}

func (p persistence) DeleteFanPwmMap(fanId string) error {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer func(db *bolt.DB) {
		_ = db.Close()
	}(db)

	key := fanId

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanPwmMap))
		if b == nil {
			// no fan bucket yet
			return nil
		}
		v := b.Get([]byte(key))
		if v == nil {
			// no data for given key
			return nil
		}

		return b.Delete([]byte(key))
	})
}

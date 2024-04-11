package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/labstack/gommon/log"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/ui"
	bolt "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"time"
)

const (
	BucketFans      = "fans"
	BucketFanPwmMap = "fanPwmMap"
)

type Persistence interface {
	Init() error

	LoadFanPwmData(fan fans.Fan) (map[int]float64, error)
	SaveFanPwmData(fan fans.Fan) (err error)
	DeleteFanPwmData(fan fans.Fan) (err error)

	LoadFanPwmMap(fanId string) (map[int]int, error)
	SaveFanPwmMap(fanId string, pwmMap map[int]int) (err error)
	DeleteFanPwmMap(fanId string) (err error)
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
		log.Info("Creating directory for db: %s", parentDir)
		err = os.MkdirAll(parentDir, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p persistence) openPersistence() (db *bolt.DB, err error) {
	db, err = bolt.Open(p.dbPath, 0600, &bolt.Options{Timeout: 1 * time.Minute})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// SaveFanPwmData saves the fan curve data of the given fan to persistence
func (p persistence) SaveFanPwmData(fan fans.Fan) (err error) {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer db.Close()

	key := fan.GetId()

	// convert the curve data moving window to a map to arrays, so we can persist them
	fanCurveDataMap := map[int]float64{}
	for key, value := range *fan.GetFanCurveData() {
		fanCurveDataMap[key] = value
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
func (p persistence) LoadFanPwmData(fan fans.Fan) (map[int]float64, error) {
	db, err := p.openPersistence()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	key := fan.GetId()

	var fanCurveDataMap map[int]float64
	err = db.Update(func(tx *bolt.Tx) error {
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
			// if we cannot read the saved data, delete it
			ui.Warning("Unable to unmarshal saved fan data for %s: %v", key, err)
			err := b.Delete([]byte(key))
			if err != nil {
				ui.Error("Unable to delete corrupt data key %s: %v", key, err)
			}
			return nil
		}

		return err
	})

	return fanCurveDataMap, err
}

func (p persistence) DeleteFanPwmData(fan fans.Fan) error {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer db.Close()

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

// SaveFanPwmMap saves the "pwm requested" -> "actual pwm" map of the given fan to persistence
func (p persistence) SaveFanPwmMap(fanId string, pwmMap map[int]int) (err error) {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer db.Close()

	key := fanId

	// convert the curve data moving window to a map to arrays, so we can persist them
	for key, value := range pwmMap {
		pwmMap[key] = value
	}

	data, err := json.Marshal(pwmMap)
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
func (p persistence) LoadFanPwmMap(fanId string) (map[int]int, error) {
	db, err := p.openPersistence()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	key := fanId

	var pwmMap map[int]int
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFanPwmMap))
		if b == nil {
			return os.ErrNotExist
		}
		v := b.Get([]byte(key))
		if v == nil {
			return os.ErrNotExist
		}

		err := json.Unmarshal(v, &pwmMap)
		if err != nil {
			// if we cannot read the saved data, delete it
			ui.Warning("Unable to unmarshal saved pwmMap data for %s: %v", key, err)
			err := b.Delete([]byte(key))
			if err != nil {
				ui.Error("Unable to delete corrupt data key %s: %v", key, err)
			}
			return nil
		}

		return err
	})

	return pwmMap, err
}

func (p persistence) DeleteFanPwmMap(fanId string) error {
	db, err := p.openPersistence()
	if err != nil {
		return err
	}
	defer db.Close()

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

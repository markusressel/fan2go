package internal

import (
	"encoding/json"
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/ui"
	bolt "go.etcd.io/bbolt"
	"os"
	"time"
)

const (
	BucketFans = "fans"
)

func OpenPersistence(dbPath string) *bolt.DB {
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		ui.Error("Could not open database file: %v", err)
		os.Exit(1)
	}
	return db
}

// SaveFanPwmData saves the fan curve data of the given fan to persistence
func SaveFanPwmData(db *bolt.DB, fan Fan) (err error) {
	key := fan.GetId()

	// convert the curve data moving window to a map to arrays, so we can persist them
	fanCurveDataMap := map[int][]float64{}
	for key, value := range *fan.GetFanCurveData() {
		var pwmValues []float64
		value.Reduce(func(window rolling.Window) float64 {
			pwmValues = append(pwmValues, window[0][0])
			return 0
		})

		fanCurveDataMap[key] = pwmValues
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
func LoadFanPwmData(db *bolt.DB, fan Fan) (map[int][]float64, error) {
	key := fan.GetId()

	fanCurveDataMap := map[int][]float64{}
	err := db.Update(func(tx *bolt.Tx) error {
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

package internal

import (
	"encoding/json"
	"fmt"
	"github.com/asecurityteam/rolling"
	bolt "go.etcd.io/bbolt"
	"log"
	"os"
	"time"
)

const (
	BucketFans = "fans"
)

func OpenPersistence(dbPath string) *bolt.DB {
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		log.Fatalf("Could not open database file: %s", err.Error())
	}
	return db
}

// SaveFanPwmData saves the fan curve data of the given fan
func SaveFanPwmData(db *bolt.DB, fan *Fan) (err error) {
	key := fan.PwmOutput

	// convert the curve data moving window to a map to arrays, so we can persist them
	fanCurveDataMap := map[int][]float64{}
	for key, value := range *fan.FanCurveData {
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

// LoadFanPwmData loads the fan curve data and attaches it to the given fan
func LoadFanPwmData(db *bolt.DB, fan *Fan) error {
	key := fan.PwmOutput

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
			log.Printf("Unable to unmarshal saved fan data for %s: %s", key, err.Error())
			err := b.Delete([]byte(key))
			if err != nil {
				log.Printf("Unable to delete corrupt data key %s: %s", key, err.Error())
			}
			return nil
		}

		return err
	})

	// convert the persisted map to arrays back to a moving window and attach it to the fan
	for key, value := range fanCurveDataMap {
		fanCurveMovingWindow := rolling.NewPointPolicy(rolling.NewWindow(CurrentConfig.RpmRollingWindowSize))
		for _, rpm := range value {
			fanCurveMovingWindow.Append(rpm)
		}
		(*fan.FanCurveData)[key] = fanCurveMovingWindow
	}

	return err
}

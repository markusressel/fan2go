package persistence

import (
	"encoding/json"
	"fan2go/internal/config"
	"fan2go/internal/data"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"log"
	"os"
	"time"
)

const (
	BucketFans = "fans"
)

var (
	Database *bolt.DB
)

func Open() *bolt.DB {
	DB, err := bolt.Open(config.CurrentConfig.DbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		log.Fatal(err)
	}
	Database = DB
	return Database
}

func SaveFanPwmData(fan *data.Fan) (err error) {
	key := fan.PwmOutput
	data, err := json.Marshal(fan)
	if err != nil {
		return err
	}
	return Database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(BucketFans))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		err = b.Put([]byte(key), data)
		return err
	})
}

func LoadFanPwmData(fan *data.Fan) (*data.Fan, error) {
	key := fan.PwmOutput
	var result data.Fan
	err := Database.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketFans))
		if b == nil {
			return os.ErrNotExist
		}
		v := b.Get([]byte(key))
		if v == nil {
			return os.ErrNotExist
		}

		err := json.Unmarshal(v, &result)
		if err != nil {
			// if we cannot read the saved data, delete it
			log.Printf("Unable to unmarshal saved fan data for %s: %s", key, err.Error())
			err := b.Delete([]byte(key))
			if err != nil {
				log.Printf("Unable to delete corrupt data key %s: %s", key, err.Error())
			}
		}

		return err
	})
	return &result, err
}

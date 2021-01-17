package persistence

import (
	"fan2go/config"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"log"
	"os"
	"strconv"
	"time"
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

func ReadInt(bucket string, key string) (result int, err error) {
	err = Database.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		v := b.Get([]byte(key))
		if v == nil {
			return os.ErrNotExist
		}
		result, err = strconv.Atoi(string(v))
		return err
	})
	return result, err
}

func StoreInt(bucket string, key string, value int) (err error) {
	return Database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		err = b.Put([]byte(key), []byte(strconv.Itoa(value)))
		return nil
	})
}

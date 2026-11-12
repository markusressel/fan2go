package persistence

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

// putRaw stores value under key in bucket of the database at dbPath, bypassing the persistence API.
func putRaw(t *testing.T, dbPath string, bucket string, key string, value []byte) {
	t.Helper()
	db, err := bolt.Open(dbPath, 0600, nil)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), value)
	}))
}

// getRaw returns the value stored under key in bucket of the database at dbPath (nil if there is none).
func getRaw(t *testing.T, dbPath string, bucket string, key string) []byte {
	t.Helper()
	db, err := bolt.Open(dbPath, 0600, nil)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var result []byte
	require.NoError(t, db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		if v := b.Get([]byte(key)); v != nil {
			result = append([]byte{}, v...)
		}
		return nil
	}))
	return result
}

func newTempPersistence(t *testing.T) (Persistence, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fan2go.db")
	p := NewPersistence(dbPath)
	require.NoError(t, p.Init())
	return p, dbPath
}

func TestPersistence_LoadFanRpmData_CorruptData(t *testing.T) {
	// GIVEN
	p, dbPath := newTempPersistence(t)
	fan, _ := createFan(false, LinearFan)
	putRaw(t, dbPath, BucketFans, fan.GetId(), []byte("garbage"))

	// WHEN
	data, err := p.LoadFanRpmData(fan)

	// THEN
	assert.Nil(t, data)
	assert.True(t, errors.Is(err, ErrCorruptData), "expected ErrCorruptData, got %v", err)
	assert.True(t, IsMissingOrCorrupt(err))
	assert.Nil(t, getRaw(t, dbPath, BucketFans, fan.GetId()), "the corrupt entry must be deleted")

	// the next load sees no data at all
	data, err = p.LoadFanRpmData(fan)
	assert.Nil(t, data)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected os.ErrNotExist, got %v", err)
}

func TestPersistence_LoadFanSetPwmToGetPwmMap_CorruptData(t *testing.T) {
	// GIVEN
	p, dbPath := newTempPersistence(t)
	putRaw(t, dbPath, BucketFanSetPwmToSetPwmMap, "fan1", []byte("garbage"))

	// WHEN
	data, err := p.LoadFanSetPwmToGetPwmMap("fan1")

	// THEN
	assert.Nil(t, data)
	assert.True(t, errors.Is(err, ErrCorruptData), "expected ErrCorruptData, got %v", err)
	assert.Nil(t, getRaw(t, dbPath, BucketFanSetPwmToSetPwmMap, "fan1"), "the corrupt entry must be deleted")

	// the next load sees no data at all
	data, err = p.LoadFanSetPwmToGetPwmMap("fan1")
	assert.Nil(t, data)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected os.ErrNotExist, got %v", err)
}

func TestPersistence_LoadFanPwmMap_CorruptData(t *testing.T) {
	for name, value := range map[string][]byte{
		"garbage":            []byte("garbage"),
		"wrong length array": []byte("[0,1,2]"),
	} {
		t.Run(name, func(t *testing.T) {
			// GIVEN
			p, dbPath := newTempPersistence(t)
			putRaw(t, dbPath, BucketFanPwmMap, "fan1", value)

			// WHEN
			data, err := p.LoadFanPwmMap("fan1")

			// THEN
			assert.Nil(t, data)
			assert.True(t, errors.Is(err, ErrCorruptData), "expected ErrCorruptData, got %v", err)
			assert.Nil(t, getRaw(t, dbPath, BucketFanPwmMap, "fan1"), "the corrupt entry must be deleted")

			// the next load sees no data at all
			data, err = p.LoadFanPwmMap("fan1")
			assert.Nil(t, data)
			assert.True(t, errors.Is(err, os.ErrNotExist), "expected os.ErrNotExist, got %v", err)
		})
	}
}

func TestPersistence_LoadFanPwmMap_NotExisting(t *testing.T) {
	// GIVEN
	p, _ := newTempPersistence(t)

	// WHEN
	data, err := p.LoadFanPwmMap("fan1")

	// THEN
	assert.Nil(t, data)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected os.ErrNotExist, got %v", err)
}

func TestPersistence_SaveAndLoadFanPwmMap(t *testing.T) {
	// GIVEN
	p, _ := newTempPersistence(t)
	expected := make([]int, 256)
	for i := range expected {
		expected[i] = i / 2
	}
	require.NoError(t, p.SaveFanPwmMap("fan1", expected))

	// WHEN
	data, err := p.LoadFanPwmMap("fan1")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, expected, data)
}

func TestIsMissingOrCorrupt(t *testing.T) {
	assert.True(t, IsMissingOrCorrupt(os.ErrNotExist))
	assert.True(t, IsMissingOrCorrupt(ErrCorruptData))
	assert.False(t, IsMissingOrCorrupt(nil))
	assert.False(t, IsMissingOrCorrupt(errors.New("timeout")))
}

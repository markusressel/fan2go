package internal

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileWatcherTest runs watchConfigFile on a config file in a temporary directory, with a fake clock
// for the debounce.
type fileWatcherTest struct {
	t          *testing.T
	dir        string
	configPath string
	// a watched directory in which other files are ignored (used to check that the watcher runs)
	markerDir  string
	clock      *util.FakeClock
	reloadChan chan struct{}
	// what the watcher did (see configWatchHook)
	actions chan string
	done    chan error
}

// startFileWatcherTest creates the config file fan2go.yaml in a temporary directory and starts the watcher on it.
// If setUp is set, it creates the config file instead, and returns its path and the marker directory.
func startFileWatcherTest(t *testing.T, setUp func(dir string) (configPath string, markerDir string)) *fileWatcherTest {
	test := &fileWatcherTest{
		t:     t,
		dir:   t.TempDir(),
		clock: util.NewFakeClock(time.Unix(1000, 0)),
		// buffered, so every requested reload can be counted
		reloadChan: make(chan struct{}, 10),
		actions:    make(chan string, 1000),
		done:       make(chan error, 1),
	}
	if setUp != nil {
		test.configPath, test.markerDir = setUp(test.dir)
	} else {
		test.markerDir = test.dir
		test.configPath = filepath.Join(test.dir, "fan2go.yaml")
		writeConfigFile(t, test.configPath, "version: 1\n")
	}

	configWatchHook = func(what string) { test.actions <- what }
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		test.done <- watchConfigFile(ctx, test.configPath, test.reloadChan, test.clock)
	}()
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, receive(t, test.done, "the watcher to stop"))
		configWatchHook = nil
	})
	test.awaitWatching()
	return test
}

// awaitWatching waits until the watcher reports events: it writes a marker file into the directory
// until the watcher reports that it ignored it.
func (test *fileWatcherTest) awaitWatching() {
	test.t.Helper()
	marker := filepath.Join(test.markerDir, "marker")
	seen := make(chan string, 1)
	require.Eventually(test.t, func() bool {
		writeConfigFile(test.t, marker, "x")
		select {
		case what := <-test.actions:
			// only marker events can arrive before the watcher saw the marker (asserted below)
			seen <- what
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(test.t, "ignored:marker", <-seen)
	// drop the events of the remaining marker writes
	require.NoError(test.t, os.Remove(marker))
	test.awaitAction("ignored:marker")
	test.drainActions()
}

func (test *fileWatcherTest) drainActions() {
	for {
		select {
		case <-test.actions:
		default:
			return
		}
	}
}

// awaitAction waits until the watcher reports want, ignoring other actions.
func (test *fileWatcherTest) awaitAction(want string) {
	test.t.Helper()
	for {
		if receive(test.t, test.actions, want) == want {
			return
		}
	}
}

// settleUntil advances the fake clock past the debounce after each reported event, until the watcher reports
// want after checking the file. Other results are skipped: a change can cause several events, and an event
// that arrives after the debounce elapsed causes another check (which finds the content unchanged).
func (test *fileWatcherTest) settleUntil(want string) {
	test.t.Helper()
	for {
		what := receive(test.t, test.actions, want)
		if what == want {
			return
		}
		if what == "debounced" {
			test.clock.Advance(configWatchDebounce)
		}
	}
}

func (test *fileWatcherTest) reloads() int {
	return len(test.reloadChan)
}

func TestWatchConfigFile_InPlaceWrite_RequestsReload(t *testing.T) {
	test := startFileWatcherTest(t, nil)

	writeConfigFile(t, test.configPath, "version: 2\n")

	test.settleUntil("reload")
	assert.Equal(t, 1, test.reloads())
}

// Editors like vim, most GUI editors, sed -i and Ansible write a new file and rename it over the config file.
func TestWatchConfigFile_AtomicSave_RequestsReload(t *testing.T) {
	test := startFileWatcherTest(t, nil)

	for i, content := range []string{"version: 2\n", "version: 3\n"} {
		// WHEN: the file is replaced by renaming a new file over it
		tmp := filepath.Join(test.dir, ".fan2go.yaml.tmp")
		writeConfigFile(t, tmp, content)
		require.NoError(t, os.Rename(tmp, test.configPath))

		// THEN: one reload is requested per save, also for later saves (the watch survives the replacement)
		test.settleUntil("reload")
		assert.Equal(t, i+1, test.reloads())
	}
}

// Editors that keep a backup move the config file away first, then write a new one.
func TestWatchConfigFile_MoveAwayAndRecreate_RequestsOneReload(t *testing.T) {
	test := startFileWatcherTest(t, nil)

	// WHEN: the file is gone when the debounce elapses
	require.NoError(t, os.Rename(test.configPath, test.configPath+"~"))
	test.settleUntil("unreadable")

	// and is written again later
	writeConfigFile(t, test.configPath, "version: 2\n")

	// THEN
	test.settleUntil("reload")
	assert.Equal(t, 1, test.reloads())
}

func TestWatchConfigFile_IdenticalContent_NoReload(t *testing.T) {
	test := startFileWatcherTest(t, nil)

	writeConfigFile(t, test.configPath, "version: 1\n")

	test.settleUntil("unchanged")
	assert.Equal(t, 0, test.reloads())
}

func TestWatchConfigFile_OtherFileInDirectory_Ignored(t *testing.T) {
	test := startFileWatcherTest(t, nil)

	writeConfigFile(t, filepath.Join(test.dir, "other.yaml"), "x")

	test.awaitAction("ignored:other.yaml")
	assert.Equal(t, 0, test.clock.Waiters(), "no debounce started")
	assert.Equal(t, 0, test.reloads())
}

// Several changes within the debounce time request a single reload.
func TestWatchConfigFile_ChangesWithinDebounce_RequestOneReload(t *testing.T) {
	test := startFileWatcherTest(t, nil)

	writeConfigFile(t, test.configPath, "version: 2\n")
	test.awaitAction("debounced")
	writeConfigFile(t, test.configPath, "version: 3\n")
	test.awaitAction("debounced")
	test.drainActions()

	test.clock.Advance(configWatchDebounce)
	test.settleUntil("reload")
	assert.Equal(t, 1, test.reloads())
}

// A symlinked config file (e.g. into a dotfiles repository): changes of the target and retargeting the
// symlink are seen.
func TestWatchConfigFile_Symlink_TargetChangesAndRetargeting(t *testing.T) {
	var targetDir string
	test := startFileWatcherTest(t, func(dir string) (string, string) {
		targetDir = filepath.Join(dir, "dotfiles")
		require.NoError(t, os.Mkdir(targetDir, 0o700))
		writeConfigFile(t, filepath.Join(targetDir, "fan2go.yaml"), "version: 1\n")
		linkDir := filepath.Join(dir, "etc")
		require.NoError(t, os.Mkdir(linkDir, 0o700))
		link := filepath.Join(linkDir, "fan2go.yaml")
		require.NoError(t, os.Symlink(filepath.Join(targetDir, "fan2go.yaml"), link))
		// the directory of the symlink target is watched after the directory of the symlink
		return link, targetDir
	})

	// WHEN: the target is saved atomically in its own directory
	tmp := filepath.Join(targetDir, "tmp")
	writeConfigFile(t, tmp, "version: 2\n")
	require.NoError(t, os.Rename(tmp, filepath.Join(targetDir, "fan2go.yaml")))

	// THEN
	test.settleUntil("reload")
	assert.Equal(t, 1, test.reloads())

	// WHEN: the symlink is replaced by one to a file in another directory
	otherDir := filepath.Join(test.dir, "other")
	require.NoError(t, os.Mkdir(otherDir, 0o700))
	writeConfigFile(t, filepath.Join(otherDir, "fan2go.yaml"), "version: 3\n")
	tmpLink := filepath.Join(filepath.Dir(test.configPath), "tmp-link")
	require.NoError(t, os.Symlink(filepath.Join(otherDir, "fan2go.yaml"), tmpLink))
	require.NoError(t, os.Rename(tmpLink, test.configPath))

	// THEN
	test.settleUntil("reload")
	assert.Equal(t, 2, test.reloads())

	// WHEN: the new target changes
	writeConfigFile(t, filepath.Join(otherDir, "fan2go.yaml"), "version: 4\n")

	// THEN: the watcher followed the symlink to its new directory
	test.settleUntil("reload")
	assert.Equal(t, 3, test.reloads())
}

func TestWatchConfigFile_MissingDirectory_ReturnsError(t *testing.T) {
	err := watchConfigFile(context.Background(), filepath.Join(t.TempDir(), "missing", "fan2go.yaml"), make(chan struct{}, 1), util.NewFakeClock(time.Unix(1000, 0)))

	assert.ErrorContains(t, err, "could not watch the config directory")
}

// The automatic reload is optional: if the watcher can't be set up, the daemon keeps running.
func TestRunFileWatcher_SetupError_WarnsAndRunsUntilCancelled(t *testing.T) {
	notified := captureNotifications(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- runFileWatcher(ctx, filepath.Join(t.TempDir(), "missing", "fan2go.yaml"), make(chan struct{}, 1))
	}()

	// THEN: the user is warned, and the actor doesn't return (which would stop the daemon)
	assert.Contains(t, receive(t, notified, "the warning"), "Automatic config reload is disabled")
	assert.Empty(t, done)

	cancel()
	assert.NoError(t, receive(t, done, "runFileWatcher"))
}

// If the config directory itself is removed or renamed, inotify drops the watch: the watcher reports that
// as an error (so runFileWatcher warns that the automatic reload is disabled).
func TestWatchConfigFile_ConfigDirectoryRemovedOrRenamed_ReturnsError(t *testing.T) {
	for _, change := range []string{"removed", "renamed"} {
		t.Run(change, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "fan2go")
			require.NoError(t, os.Mkdir(dir, 0o700))
			configPath := filepath.Join(dir, "fan2go.yaml")
			writeConfigFile(t, configPath, "version: 1\n")
			done := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			watching := make(chan struct{})
			var once sync.Once
			configWatchHook = func(what string) { once.Do(func() { close(watching) }) }
			t.Cleanup(func() { configWatchHook = nil })
			go func() {
				done <- watchConfigFile(ctx, configPath, make(chan struct{}, 10), util.NewFakeClock(time.Unix(1000, 0)))
			}()
			// wait until the watcher reports an event, so it watches the directory
			require.Eventually(t, func() bool {
				writeConfigFile(t, filepath.Join(dir, "marker"), "x")
				select {
				case <-watching:
					return true
				default:
					return false
				}
			}, 5*time.Second, 10*time.Millisecond)

			if change == "removed" {
				require.NoError(t, os.RemoveAll(dir))
			} else {
				require.NoError(t, os.Rename(dir, dir+".old"))
			}

			assert.ErrorContains(t, receive(t, done, "the watcher to stop"), "was removed or renamed")
		})
	}
}

package persistence

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ProcessLockedError is returned by AcquireProcessLock if another process holds the lock.
type ProcessLockedError struct {
	// Path is the lock file
	Path string
	// Pid is the process id of the holder, 0 if unknown
	Pid int
}

func (e *ProcessLockedError) Error() string {
	holder := "another fan2go process"
	if e.Pid > 0 {
		holder = fmt.Sprintf("another fan2go process (PID %d)", e.Pid)
	}
	return fmt.Sprintf("%s uses the fan2go database (lock file %s)", holder, e.Path)
}

// ProcessLockPath returns the path of the lock file of the database at dbPath.
func ProcessLockPath(dbPath string) string {
	return dbPath + ".lock"
}

// AcquireProcessLock takes the lock that marks the process that controls the fans of the database at dbPath:
// the daemon, or `fan2go fan init` / `fan reset`. It doesn't wait: if another process holds it, it returns a
// *ProcessLockedError. The parent directory of dbPath must exist (see Persistence.Init).
//
// The lock is an advisory lock (flock) on the file ProcessLockPath(dbPath), which contains the PID of the holder
// for error messages. The kernel releases the lock when the process exits, also if it is killed, so a lock file
// left behind never blocks: a stale PID in it is ignored. bbolt's own file lock can't be used for this, because
// every process opens the database only for a single operation.
//
// release unlocks the lock. The file is left in place: deleting it would let a process that opened it just
// before lock a file that no longer exists.
func AcquireProcessLock(dbPath string) (release func(), err error) {
	path := ProcessLockPath(dbPath)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("unable to open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid := readLockPid(file)
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &ProcessLockedError{Path: path, Pid: pid}
		}
		return nil, fmt.Errorf("unable to lock %s: %w", path, err)
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// readLockPid returns the PID written to the lock file, 0 if it can't be read.
func readLockPid(file *os.File) int {
	buf := make([]byte, 32)
	n, _ := file.ReadAt(buf, 0)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

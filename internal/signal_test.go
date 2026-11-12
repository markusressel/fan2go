package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upper bound for things that are expected to happen right away
const signalTestTimeout = 5 * time.Second

// The child process runs the signal handler and signals itself, so the test binary itself keeps the
// default signal handling (e.g. Ctrl-C), and a panic or termination of the child can be observed.
const signalTestChildEnv = "FAN2GO_SIGNAL_TEST_CHILD"

func TestRunSignalHandler_SignalsAfterReturn_DoNotPanicOrTerminateProcess(t *testing.T) {
	if s := os.Getenv(signalTestChildEnv); s != "" {
		runSignalsAfterReturnInChild(s)
		return
	}

	for _, s := range []string{"SIGHUP", "SIGTERM", "SIGINT"} {
		t.Run(s, func(t *testing.T) {
			// WHEN
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunSignalHandler_SignalsAfterReturn_DoNotPanicOrTerminateProcess$")
			cmd.Env = append(os.Environ(), signalTestChildEnv+"="+s)
			out, err := cmd.CombinedOutput()

			// THEN: the child survived the signals and exited normally
			require.NoError(t, err, "child output:\n%s", out)
			assert.Contains(t, string(out), "child survived")
		})
	}
}

func runSignalsAfterReturnInChild(name string) {
	signals := map[string]syscall.Signal{"SIGHUP": syscall.SIGHUP, "SIGTERM": syscall.SIGTERM, "SIGINT": syscall.SIGINT}
	s, ok := signals[name]
	if !ok {
		os.Exit(4)
	}

	// GIVEN: a signal handler that returned, because the daemon is shutting down
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = runSignalHandler(ctx, make(chan struct{}, 1))

	// WHEN: signals arrive during shutdown
	for i := 0; i < 2; i++ {
		_ = syscall.Kill(os.Getpid(), s)
	}

	// wait until os/signal has processed signals sent after them
	observed := make(chan os.Signal, 1)
	signal.Notify(observed, syscall.SIGUSR1)
	for i := 0; i < 2; i++ {
		_ = syscall.Kill(os.Getpid(), syscall.SIGUSR1)
		select {
		case <-observed:
		case <-time.After(signalTestTimeout):
			os.Exit(3)
		}
	}
	fmt.Println("child survived")
}

func TestHandleSignals_SighupRequestsReload(t *testing.T) {
	sig := make(chan os.Signal)
	reloadChan := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- handleSignals(ctx, sig, reloadChan) }()

	sig <- syscall.SIGHUP

	select {
	case <-reloadChan:
	case <-time.After(signalTestTimeout):
		t.Fatal("SIGHUP did not request a reload")
	}
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(signalTestTimeout):
		t.Fatal("handleSignals did not return after ctx was cancelled")
	}
	close(sig)
}

func TestHandleSignals_SignalsDuringShutdownAreIgnored(t *testing.T) {
	// GIVEN: a signal handler that returned because of a SIGTERM
	sig := make(chan os.Signal)
	reloadChan := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- handleSignals(context.Background(), sig, reloadChan) }()

	sig <- syscall.SIGTERM
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(signalTestTimeout):
		t.Fatal("handleSignals did not return on SIGTERM")
	}

	// WHEN: further signals arrive while the daemon shuts down
	for _, s := range []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		// THEN: they are still received and logged (sig is unbuffered), and no reload is requested
		select {
		case sig <- s:
		case <-time.After(signalTestTimeout):
			t.Fatalf("%v was not received during shutdown", s)
		}
	}
	assert.Empty(t, reloadChan)
	close(sig)
}

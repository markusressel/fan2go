package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/markusressel/fan2go/internal/fans"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initFailingPersistence is a fakePersistence whose Init fails while failInit is set.
type initFailingPersistence struct {
	*fakePersistence
	failInit atomic.Bool
}

func (p *initFailingPersistence) Init() error {
	if p.failInit.Load() {
		return errors.New("database directory not writable")
	}
	return nil
}

// resetRuntimeState must keep the fan restorable after an incomplete restore: a Run that fails
// before storeInitialFanState (prepareController) still restores the fan, because handbackPending is set.
func TestFanController_RunAgainAfterIncompleteRestore_EarlyErrorStillRestores(t *testing.T) {
	// GIVEN: a run whose restore was incomplete
	fan := &failingModeFan{panickingFan: newPanickingFan()}
	p := &initFailingPersistence{fakePersistence: newFakePersistence()}
	test := startOriginalStateRun(t, fan, p, "")
	fan.setFailModeWrites(true)
	test.stop()
	fan.setFailModeWrites(false)
	require.Equal(t, fans.ControlModePWM, fan.ControlMode, "the restore was incomplete")

	// WHEN: the same controller runs again (failed reload) and fails right away
	p.failInit.Store(true)
	err := test.controller.Run(context.Background())
	require.Error(t, err)

	// THEN: the fan is handed back anyway
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)
}

// resetRuntimeState must reset originalFanStateSaveFailed: a Run that couldn't save the original
// state tries again in the next Run.
func TestFanController_RunAgainAfterFailedSave_SavesAgain(t *testing.T) {
	// GIVEN: a run that couldn't save the original state
	fan := newPanickingFan()
	p := newFakePersistence()
	p.saveOriginalStateErrors = []error{errors.New("database locked")}
	test := startOriginalStateRun(t, fan, p, "")
	_, ok := p.originalState("fan")
	require.False(t, ok)
	test.stop()

	// WHEN: the same controller runs again
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test.done = startRun(test.controller, ctx)
	test.cancel = cancel
	advanceThroughRunStartup(t, test.clock)
	test.curve.set(60, nil)
	test.awaitPwm(60)

	// THEN: the original state is saved this time
	_, ok = p.originalState("fan")
	assert.True(t, ok)
	test.stop()
}

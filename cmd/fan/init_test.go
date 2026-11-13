package fan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fan init prints the measured curve by calling the curve command directly, which only has RunE
// (calling its nil Run made every successful fan init panic at the end).
func TestInitCmd_CurveCmdIsCallableViaRunE(t *testing.T) {
	assert.Nil(t, curveCmd.Run)
	assert.NotNil(t, curveCmd.RunE)
}

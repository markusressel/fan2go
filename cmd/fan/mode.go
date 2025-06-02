package fan

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"strconv"
	"strings"
)

var modeCmd = &cobra.Command{
	Use:   "mode",
	Short: "Get/Set the current pwm mode setting of a fan",
	Long:  ``,
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pterm.DisableOutput()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		if len(args) > 0 {
			firstArg := args[0]
			argAsInt, err := strconv.Atoi(firstArg)
			var controlMode fans.ControlMode
			if err != nil {
				switch strings.ToLower(firstArg) {
				case "auto":
					controlMode = fans.ControlModeAutomatic
				case "pwm":
					controlMode = fans.ControlModePWM
				case "disabled":
					controlMode = fans.ControlModeDisabled
				default:
					return fmt.Errorf("unknown mode: %s, must be a integer in (1..3) or one of: 'auto', 'pwm', 'disabled'", firstArg)
				}
			} else {
				controlMode = fans.ControlMode(argAsInt)
				switch controlMode {
				case fans.ControlModeAutomatic, fans.ControlModePWM, fans.ControlModeDisabled:
					break
				default:
					return fmt.Errorf("unknown mode: %d, must be a integer in (1..3) or one of: 'auto', 'pwm', 'disabled'", argAsInt)
				}
			}
			err = fan.SetControlMode(controlMode)
			if err != nil {
				return err
			}
		}

		controlMode, err := fan.GetControlMode()
		if err != nil {
			return err
		}

		switch controlMode {
		case fans.ControlModeDisabled:
			fmt.Printf("No control, 100%% all the time (%d)", controlMode)
		case fans.ControlModePWM:
			fmt.Printf("Manual PWM control, gives fan2go control (%d)", controlMode)
		case fans.ControlModeAutomatic:
			fmt.Printf("Automatic control by integrated hardware (%d)", controlMode)
		default:
			fmt.Printf("Unknown (%d)", controlMode)
		}

		return err
	},
}

func init() {
	Command.AddCommand(modeCmd)
}

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
			var pwmEnabled fans.ControlMode
			if err != nil {
				switch strings.ToLower(firstArg) {
				case "auto":
					pwmEnabled = fans.ControlModeAutomatic
				case "pwm":
					pwmEnabled = fans.ControlModePWM
				case "disabled":
					pwmEnabled = fans.ControlModeDisabled
				default:
					return fmt.Errorf("unknown mode: %s, must be a integer in (1..3) or one of: 'auto', 'pwm', 'disabled'", firstArg)
				}
			} else {
				pwmEnabled = fans.ControlMode(argAsInt)
				switch pwmEnabled {
				case fans.ControlModeAutomatic, fans.ControlModePWM, fans.ControlModeDisabled:
					break
				default:
					return fmt.Errorf("unknown mode: %d, must be a integer in (1..3) or one of: 'auto', 'pwm', 'disabled'", argAsInt)
				}
			}
			err = fan.SetPwmEnabled(pwmEnabled)
			if err != nil {
				return err
			}
		}

		pwmEnabled, err := fan.GetPwmEnabled()
		if err != nil {
			return err
		}

		switch fans.ControlMode(pwmEnabled) {
		case fans.ControlModeDisabled:
			fmt.Printf("No control, 100%% all the time (%d)", pwmEnabled)
		case fans.ControlModePWM:
			fmt.Printf("Manual PWM control, gives fan2go control (%d)", pwmEnabled)
		case fans.ControlModeAutomatic:
			fmt.Printf("Automatic control by integrated hardware (%d)", pwmEnabled)
		default:
			fmt.Printf("Unknown (%d)", pwmEnabled)
		}

		return err
	},
}

func init() {
	Command.AddCommand(modeCmd)
}

package fan

import (
	"fmt"
	"strconv"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var speedCmd = &cobra.Command{
	Use:   "speed",
	Short: "Get/Set the current speed setting of a fan to the given PWM value ([0..255])",
	Long: `Without a value, prints the current PWM value of the fan. With a value, writes it to the fan; this is
refused while the fan2go daemon runs, because it controls the fan and would overwrite the value.`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pterm.DisableOutput()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		if len(args) > 0 {
			var pwmValue int
			pwmValue, err = parsePwmValue(args[0])
			if err != nil {
				return err
			}
			release, err := lockFanForWriting(configuration.CurrentConfig.DbPath, fan.GetId(), "speed")
			if err != nil {
				return err
			}
			defer release()
			err = fan.SetPwm(pwmValue)
			return err
		} else {
			if !fan.Supports(fans.FeaturePwmSensor) {
				fmt.Printf("N/A")
				return nil
			}

			var pwm int
			if pwm, err = fan.GetPwm(); err == nil {
				fmt.Printf("%d", pwm)
			}
		}

		return err
	},
}

// parsePwmValue parses a PWM value given on the command line, which must be an integer in [0..255]
func parsePwmValue(arg string) (int, error) {
	pwmValue, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("invalid PWM value '%s', must be an integer in [%d..%d]", arg, fans.MinPwmValue, fans.MaxPwmValue)
	}
	if pwmValue < fans.MinPwmValue || pwmValue > fans.MaxPwmValue {
		return 0, fmt.Errorf("invalid PWM value %d, must be in [%d..%d]", pwmValue, fans.MinPwmValue, fans.MaxPwmValue)
	}
	return pwmValue, nil
}

func init() {
	Command.AddCommand(speedCmd)
}

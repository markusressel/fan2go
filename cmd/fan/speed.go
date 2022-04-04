package fan

import (
	"fmt"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"strconv"
)

var speedCmd = &cobra.Command{
	Use:   "speed",
	Short: "Get/Set the current speed setting of a fan to the given PWM value ([0..255])",
	Long:  ``,
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pterm.DisableOutput()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		if len(args) > 0 {
			pwmValue, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}
			err = fan.SetPwm(pwmValue)
		} else {
			if pwm, err := fan.GetPwm(); err == nil {
				fmt.Printf("%d", pwm)
			}
		}

		return err
	},
}

func init() {
	Command.AddCommand(speedCmd)
}

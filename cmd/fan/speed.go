package fan

import (
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
	"strconv"
)

var speedCmd = &cobra.Command{
	Use:   "speed",
	Short: "Get/Set the current speed setting of a fan to the given PWM value ([0..255])",
	Long:  ``,
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fanIdFlag := cmd.Flag("id")
		fanId := fanIdFlag.Value.String()

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
			if err != nil {
				return err
			}
		} else {
			ui.Printfln("%d", fan.GetPwm())
			return nil
		}

		return err
	},
}

func init() {
	Command.AddCommand(speedCmd)
}

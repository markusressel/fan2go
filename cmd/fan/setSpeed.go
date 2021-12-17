package fan

import (
	"errors"
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/spf13/cobra"
	"regexp"
	"strconv"
)

var setSpeedCmd = &cobra.Command{
	Use:   "setSpeed",
	Short: "Set the speed of a fan to the given PWM value ([0..255])",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		fanIdFlag := cmd.Flag("id")
		fanId := fanIdFlag.Value.String()

		pwmValue, err := strconv.Atoi(args[0])
		if err != nil {
			return err
		}

		configuration.ReadConfigFile()

		controllers := hwmon.GetChips()

		for _, config := range configuration.CurrentConfig.Fans {
			if config.ID == fanId {
				if config.HwMon != nil {
					for _, controller := range controllers {
						matched, err := regexp.MatchString("(?i)"+config.HwMon.Platform, controller.Platform)
						if err != nil {
							return errors.New(fmt.Sprintf("Failed to match platform regex of %s (%s) against controller platform %s", config.ID, config.HwMon.Platform, controller.Platform))
						}
						if matched {
							index := config.HwMon.Index - 1
							if len(controller.Fans) > index {
								fan := controller.Fans[index]
								config.HwMon.PwmOutput = fan.PwmOutput
								config.HwMon.RpmInput = fan.RpmInput
								break
							}
						}
					}
				}

				if len(config.HwMon.PwmOutput) <= 0 {
					return errors.New(fmt.Sprintf("Unable to find pwm output for fan %s", fanId))
				}

				fan, err := fans.NewFan(config)
				if err != nil {
					return err
				}
				return fan.SetPwm(pwmValue)
			}
		}

		return errors.New(fmt.Sprintf("No fan with id found: %s", fanId))
	},
}

func init() {
	Command.AddCommand(setSpeedCmd)
}

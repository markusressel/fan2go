package fan

import (
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var skipAutoMap bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Runs the initialization sequence for a fan",
	Long:  ``,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		//pterm.DisableOutput()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		dbPath := configuration.CurrentConfig.DbPath
		ui.Info("Using persistence at: %s", dbPath)

		p := persistence.NewPersistence(dbPath)
		_, err = internal.InitializeObjects()
		if err != nil {
			return err
		}
		skipAutoPwmMapping := skipAutoMap
		if !skipAutoMap {
			// the --skipAutoMap commandline option has priority,
			// but if it's not set, use the setting from the config
			skipAutoPwmMapping = fan.GetConfig().SkipAutoPwmMap
		}
		fanController := controller.NewFanController(
			p,
			fan,
			control_loop.NewDirectControlLoop(nil),
			configuration.CurrentConfig.FanController.AdjustmentTickRate,
			skipAutoPwmMapping,
		)

		ui.Info("Deleting existing data for fan '%s'...", fan.GetId())

		if err = p.DeleteFanRpmData(fan); err != nil {
			return err
		}
		if err = p.DeleteFanSetPwmToGetPwmMap(fan.GetId()); err != nil {
			return err
		}
		if err = p.DeleteFanPwmMap(fan.GetId()); err != nil {
			return err
		}

		err = fanController.RunInitializationSequence()

		if err == nil {
			ui.Success("Done!")
			// print measured fan curve
			curveCmd.Run(curveCmd, []string{})
		}

		return err
	},
}

func init() {
	initCmd.Flags().IntP("fan-response-delay", "e", 2, "Delay in seconds to wait before checking that a fan has responded to a control change")
	_ = viper.BindPFlag("FanResponseDelay", initCmd.Flags().Lookup("fan-response-delay"))
	initCmd.Flags().BoolVarP(&skipAutoMap, "skip-auto-pwm-map", "s", false, "Skip automatic detection/calculation of PWM map")
	Command.AddCommand(initCmd)
}

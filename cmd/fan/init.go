package fan

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

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

		fanController := controller.NewFanController(
			p,
			fan,
			control_loop.NewDirectControlLoop(nil),
			configuration.CurrentConfig.ControllerAdjustmentTickRate)

		ui.Info("Deleting existing data for fan '%s'...", fan.GetId())

		if err = p.DeleteFanPwmData(fan); err != nil {
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
	Command.AddCommand(initCmd)
}

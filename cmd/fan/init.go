package fan

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/spf13/cobra"
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
			*util.NewPidLoop(
				0.03,
				0.002,
				0.0005,
			),
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
			return curveCmd.RunE(cmd, []string{fanId})
		}

		return err
	},
}

func init() {
	Command.AddCommand(initCmd)
}

package fan

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset all data associated with a given fan",
	Long:  ``,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		pterm.DisableOutput()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		configPath := configuration.DetectConfigFile()
		ui.Info("Using configuration file at: %s", configPath)
		configuration.LoadConfig()

		dbPath := configuration.CurrentConfig.DbPath
		ui.Info("Using persistence at: %s", dbPath)

		p := persistence.NewPersistence(dbPath)
		err = p.DeleteFanPwmData(fan)
		if err != nil {
			return err
		}
		err = p.DeleteFanPwmMap(fan.GetId())
		return err
	},
}

func init() {
	Command.AddCommand(resetCmd)
}

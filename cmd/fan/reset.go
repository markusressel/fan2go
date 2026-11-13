package fan

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
)

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset all data associated with a given fan",
	Long: `Deletes the saved analysis data of the fan, so it is analyzed again on the next start of the daemon.
Stop the fan2go daemon first: the command refuses to run while it uses the same database.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		dbPath := configuration.CurrentConfig.DbPath
		ui.Info("Using persistence at: %s", dbPath)

		p, release, err := openPersistenceExclusively(dbPath)
		if err != nil {
			return err
		}
		defer release()

		err = p.DeleteFanRpmData(fan)
		if err != nil {
			return err
		}
		err = p.DeleteFanSetPwmToGetPwmMap(fan.GetId())
		if err != nil {
			return err
		}
		err = p.DeleteFanPwmMap(fan.GetId())
		if err != nil {
			return err
		}
		err = p.DeleteFanAnalysisInfo(fan.GetId())

		if err == nil {
			ui.Success("Done!")
		}

		return err
	},
}

func init() {
	Command.AddCommand(resetCmd)
}

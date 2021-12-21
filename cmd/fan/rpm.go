package fan

import (
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
)

var rpmCmd = &cobra.Command{
	Use:   "rpm",
	Short: "Get the current RPM reading of a fan",
	Long:  ``,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fanIdFlag := cmd.Flag("id")
		fanId := fanIdFlag.Value.String()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		ui.Printfln("%d", fan.GetRpm())
		return nil
	},
}

func init() {
	Command.AddCommand(rpmCmd)
}

package fan

import (
	"fmt"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var rpmCmd = &cobra.Command{
	Use:   "rpm",
	Short: "Get the current RPM reading of a fan",
	Long:  ``,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		pterm.DisableOutput()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}
		if rpm, err := fan.GetRpm(); err == nil {
			fmt.Printf("%d", rpm)
		}
		return err
	},
}

func init() {
	Command.AddCommand(rpmCmd)
}

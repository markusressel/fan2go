package fan

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/fans"
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

		if !fan.Supports(fans.FeatureRpmSensor) {
			fmt.Printf("N/A")
			return nil
		}

		rpm, err := fan.GetRpm()
		if err == nil {
			fmt.Printf("RPM: %d", rpm)
		}
		return err
	},
}

func init() {
	Command.AddCommand(rpmCmd)
}

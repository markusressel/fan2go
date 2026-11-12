package config

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validates the current configuration",
	Long:  ``,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := configuration.LoadAndValidateConfigFile()
		if err != nil {
			return err
		}

		ui.Success("Config looks good! :)")
		return nil
	},
}

func init() {
	Command.AddCommand(validateCmd)
}

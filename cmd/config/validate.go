package config

import (
	"os"

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
		configPath := configuration.DetectAndReadConfigFile()

		ui.Info("Using configuration file at: %s", configPath)
		var err error
		configuration.CurrentConfig, err = configuration.LoadConfig()
		if err != nil {
			ui.Error("Parsing failed: %v", err)
			os.Exit(1)
		}

		if err := configuration.ValidateConfig(&configuration.CurrentConfig, configPath); err != nil {
			ui.Error("Validation failed: %v", err)
			os.Exit(1)
		}

		ui.Success("Config looks good! :)")
		return nil
	},
}

func init() {
	Command.AddCommand(validateCmd)
}

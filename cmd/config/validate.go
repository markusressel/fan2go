package config

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/quirks"
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

		// tips and warnings only: they must never make the validation fail or change the exit code
		for _, advice := range knownQuirksAdvice(hwmon.GetChips(), configuration.CurrentConfig, nil) {
			switch advice.Level {
			case quirks.AdviceWarning:
				ui.Warning("%s", advice.Message)
			default:
				ui.Info("Tip: %s", advice.Message)
			}
		}

		ui.Success("Config looks good! :)")
		return nil
	},
}

// knownQuirksAdvice returns the advice of the known hardware quirks registry for the hwmon fans of the config
// that can be found among the given controllers. Fans that can't be found, or have no registry entry, are skipped.
// readFile is passed on to the advice functions (os.ReadFile if nil).
func knownQuirksAdvice(
	controllers []*hwmon.HwMonController,
	config configuration.Configuration,
	readFile func(name string) ([]byte, error),
) []quirks.Advice {
	var result []quirks.Advice
	for _, fanConfig := range config.Fans {
		if fanConfig.HwMon == nil {
			continue
		}
		// resolve a copy, the config of the fan must not change
		hwMonConfig := *fanConfig.HwMon
		fanConfig.HwMon = &hwMonConfig
		if err := hwmon.UpdateFanConfigFromHwMonControllers(controllers, &fanConfig); err != nil {
			continue
		}
		entry := quirks.ForFan(fanConfig)
		if entry == nil {
			continue
		}
		result = append(result, entry.Check(quirks.AdviceContext{
			Fan:                fanConfig,
			AdjustmentTickRate: config.FanController.AdjustmentTickRate,
			ReadFile:           readFile,
		})...)
	}
	return result
}

func init() {
	Command.AddCommand(validateCmd)
}

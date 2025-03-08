package cmd

import (
	"fmt"
	"os"

	"github.com/pterm/pterm/putils"

	"github.com/markusressel/fan2go/cmd/config"
	"github.com/markusressel/fan2go/cmd/curve"
	"github.com/markusressel/fan2go/cmd/fan"
	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/cmd/sensor"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "fan2go",
	Short: "A daemon to control the fans of a computer.",
	Long: `fan2go is a simple daemon that controls the fans
on your computer based on temperature sensors.`,
	// this is the default command to run when no subcommand is specified
	Run: func(cmd *cobra.Command, args []string) {
		printHeader()

		configPath := configuration.DetectAndReadConfigFile()
		ui.Info("Using configuration file at: %s", configPath)
		configuration.LoadConfig()
		err := configuration.Validate(configPath)
		if err != nil {
			ui.ErrorAndNotify("Config Validation Error: %v", "%v", err)
			return
		}

		internal.RunDaemon()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&global.CfgFile, "config", "c", "", "config file (default is $HOME/.fan2go.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&global.NoColor, "no-color", "", false, "Disable all terminal output coloration")
	rootCmd.PersistentFlags().BoolVarP(&global.NoStyle, "no-style", "", false, "Disable all terminal output styling")
	rootCmd.PersistentFlags().BoolVarP(&global.Verbose, "verbose", "v", false, "More verbose output")

	rootCmd.AddCommand(config.Command)

	rootCmd.AddCommand(fan.Command)
	rootCmd.AddCommand(curve.Command)
	rootCmd.AddCommand(sensor.Command)
}

func setupUi() {
	ui.SetDebugEnabled(global.Verbose)
	if global.Verbose {
		pterm.Info.Println("Verbose output enabled")
	}

	if global.NoColor {
		pterm.DisableColor()
		pterm.Info.Println("Color output disabled")
	}
	if global.NoStyle {
		pterm.DisableStyling()
		pterm.Info.Println("Styled output disabled")
	}
}

// Print a large text with the LetterStyle from the standard theme.
func printHeader() {
	err := pterm.DefaultBigText.WithLetters(
		putils.LettersFromStringWithStyle("fan", pterm.NewStyle(pterm.FgLightBlue)),
		putils.LettersFromStringWithStyle("2", pterm.NewStyle(pterm.FgWhite)),
		putils.LettersFromStringWithStyle("go", pterm.NewStyle(pterm.FgLightBlue)),
	).Render()
	if err != nil {
		fmt.Println("fan2go")
	}
	ui.Info("Version: %s", global.Version)
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.OnInitialize(func() {
		configuration.InitConfig(global.CfgFile)
		setupUi()
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

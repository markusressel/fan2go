package cmd

import (
	"fmt"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"os"
)

var (
	cfgFile string
	noColor bool
	noStyle bool
	verbose bool
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "fan2go",
	Short: "A daemon to control the fans of a computer.",
	Long: `fan2go is a simple daemon that controls the fans
on your computer based on temperature sensors.`,
	// this is the default command to run when no subcommand is specified
	Run: func(cmd *cobra.Command, args []string) {
		setupUi()
		printHeader()

		configuration.ReadConfigFile()
		internal.RunDaemon()
	},
}

func setupUi() {
	ui.SetDebugEnabled(verbose)

	if noColor {
		pterm.DisableColor()
	}
	if noStyle {
		pterm.DisableStyling()
	}
}

// Print a large text with the LetterStyle from the standard theme.
func printHeader() {
	err := pterm.DefaultBigText.WithLetters(
		pterm.NewLettersFromStringWithStyle("fan", pterm.NewStyle(pterm.FgLightBlue)),
		pterm.NewLettersFromStringWithStyle("2", pterm.NewStyle(pterm.FgWhite)),
		pterm.NewLettersFromStringWithStyle("go", pterm.NewStyle(pterm.FgLightBlue)),
	).Render()
	if err != nil {
		fmt.Println("fan2go")
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.OnInitialize(func() {
		configuration.InitConfig(cfgFile)
	})

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default is $HOME/.fan2go.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&noColor, "no-color", "", false, "Disable all terminal output coloration")
	rootCmd.PersistentFlags().BoolVarP(&noStyle, "no-style", "", false, "Disable all terminal output styling")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "More verbose output")

	if err := rootCmd.Execute(); err != nil {
		ui.Error("%v", err)
		os.Exit(1)
	}
}

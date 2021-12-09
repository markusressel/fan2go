package cmd

import (
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of fan2go",
	Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		ui.Printfln("0.2.4")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

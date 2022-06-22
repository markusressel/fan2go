package cmd

import (
	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
)

var long bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of fan2go",
	Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		if global.Verbose {
			ui.Printfln("%s-%s-%s", global.Version, global.Commit, global.Date)
		} else if long {
			ui.Printfln("%s-%s", global.Version, global.Commit)
		} else {
			ui.Printfln("%s", global.Version)
		}
	},
}

func init() {
	versionCmd.Flags().BoolVarP(&long, "long", "l", false, "Show the long version")

	rootCmd.AddCommand(versionCmd)
}

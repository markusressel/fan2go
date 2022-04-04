package cmd

import (
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
)

var long bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of fan2go",
	Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		if verbose {
			ui.Printfln("%s-%s-%s", version, commit, date)
		} else if long {
			ui.Printfln("%s-%s", version, commit)
		} else {
			ui.Printfln("%s", version)
		}
	},
}

func init() {
	versionCmd.Flags().BoolVarP(&long, "long", "l", false, "Show the long version")

	rootCmd.AddCommand(versionCmd)
}

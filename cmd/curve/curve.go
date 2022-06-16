package curve

import (
	"github.com/spf13/cobra"
)

var curveId string

var Command = &cobra.Command{
	Use:              "curve",
	Short:            "Curve related commands",
	TraverseChildren: true,
}

func init() {
	Command.PersistentFlags().StringVarP(
		&curveId,
		"id", "i",
		"",
		"Curve ID as specified in the config",
	)
	_ = Command.MarkPersistentFlagRequired("id")
}

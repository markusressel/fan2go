package fan

import (
	"github.com/spf13/cobra"
)

var fanId string

var Command = &cobra.Command{
	Use:              "fan",
	Short:            "Fan related commands",
	Long:             ``,
	TraverseChildren: true,
}

func init() {
	Command.PersistentFlags().StringVarP(
		&fanId,
		"id", "i",
		"",
		"Fan ID as specified in the config",
	)
	_ = Command.MarkPersistentFlagRequired("id")
}

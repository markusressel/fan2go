package config

import "github.com/spf13/cobra"

var Command = &cobra.Command{
	Use:              "config",
	Short:            "Configuration related commands",
	Long:             ``,
	TraverseChildren: true,
}

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var manCmd = &cobra.Command{
	Use:    "man [output directory]",
	Short:  "Generate man pages for fan2go",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		outDir := args[0]
		if err := os.MkdirAll(outDir, 0755); err != nil {
			ui.Fatal("Failed to create directory: %v", err)
		}

		// Enrich rootCmd.Long with README content and example config
		originalLong := rootCmd.Long
		defer func() { rootCmd.Long = originalLong }()

		readme, err := os.ReadFile("README.md")
		if err == nil {
			// Strip HTML and badges (everything before the first markdown header)
			re := regexp.MustCompile(`(?s)^.*?\n# `)
			cleanReadme := re.ReplaceAllString(string(readme), "# ")
			rootCmd.Long += "\n\n# DESCRIPTION\n\n" + cleanReadme
		}

		exampleConfig, err := os.ReadFile("fan2go.yaml")
		if err == nil {
			rootCmd.Long += "\n\n# EXAMPLE CONFIGURATION\n\n```yaml\n" + string(exampleConfig) + "\n```"
		}

		header := &doc.GenManHeader{
			Title:   "FAN2GO",
			Section: "1",
			Source:  fmt.Sprintf("fan2go %s", global.Version),
			Manual:  "fan2go Manual",
		}

		err = doc.GenManTree(rootCmd, header, outDir)
		if err != nil {
			ui.Fatal("Failed to generate man pages: %v", err)
		}

		// Fix generated filenames to use '-' instead of '_' to match common man page conventions
		// cobra/doc generates fan2go_config.1, we might prefer fan2go-config.1
		files, _ := filepath.Glob(filepath.Join(outDir, "*.1"))
		for _, f := range files {
			base := filepath.Base(f)
			if strings.Contains(base, "_") {
				newPath := filepath.Join(outDir, strings.ReplaceAll(base, "_", "-"))
				_ = os.Rename(f, newPath)
			}
		}

		ui.Info("Man pages generated in: %s", outDir)
	},
}

func init() {
	rootCmd.AddCommand(manCmd)
}

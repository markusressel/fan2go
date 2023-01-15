package curve

import (
	"errors"
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
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
}

func getCurveConfig(id string, curves []configuration.CurveConfig) (*configuration.CurveConfig, error) {
	availableCurveIds := []string{}
	for _, curveConf := range curves {
		availableCurveIds = append(availableCurveIds, curveConf.ID)
		if id == curveConf.ID {
			return &curveConf, nil
		}
	}

	return nil, errors.New(fmt.Sprintf("No curve with id found: %s, options: %s", id, availableCurveIds))
}

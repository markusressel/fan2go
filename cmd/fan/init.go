package fan

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var assumePwmMapIdentity bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Runs the initialization sequence for a fan",
	Long: `Measures the fan again (setPwmToGetPwmMap, pwmMap and RPM curve) and replaces its saved data if the
measurement succeeds. Stop the fan2go daemon first: the command refuses to run while it uses the same database.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		//pterm.DisableOutput()

		fan, err := getFan(fanId)
		if err != nil {
			return err
		}

		dbPath := configuration.CurrentConfig.DbPath
		ui.Info("Using persistence at: %s", dbPath)

		p, release, err := openPersistenceExclusively(dbPath)
		if err != nil {
			return err
		}
		defer release()

		_, reg, err := internal.InitializeObjects()
		if err != nil {
			return err
		}
		curve, _ := reg.GetCurve(fan.GetCurveId())
		fanController := controller.NewFanController(
			p,
			fan,
			curve,
			control_loop.NewDirectControlLoop(nil),
			configuration.CurrentConfig.FanController.AdjustmentTickRate,
			assumePwmMapIdentity,
		)

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		// replaces the saved data only if it succeeds, and hands the fan back afterwards (also on errors and
		// panics): the analysis leaves it in manual mode
		_, err = fanController.RunInitializationAndRestore(ctx)
		if err != nil {
			return err
		}
		ui.Success("Done!")
		// print measured fan curve
		return curveCmd.RunE(curveCmd, []string{})
	},
}

func init() {
	initCmd.Flags().StringP("fan-response-delay", "e", "2", "Delay to wait before checking that a fan has responded to a control change: a number of seconds (e.g. 2) or a duration (e.g. 500ms)")
	_ = viper.BindPFlag("FanResponseDelay", initCmd.Flags().Lookup("fan-response-delay"))
	initCmd.Flags().BoolVar(&assumePwmMapIdentity, "assume-pwm-map-identity", false, "Skip automatic detection of setPwmToGetPwmMap; assume a 1:1 mapping instead")
	Command.AddCommand(initCmd)
}

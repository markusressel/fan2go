package config

type (
	FanCurve struct {
		Sensors   []string
		PlotItems []PlotItem
	}

	PlotItem struct {
		Temp       int
		Percentage int
	}
)

package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/warnings"
)

func registerCurveEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/curve")

	group.GET("/", func(c *echo.Context) error {
		return getCurves(c, reg)
	})
	group.GET("/:"+urlParamId+"/", func(c *echo.Context) error {
		return getCurve(c, reg)
	})
}

func getCurves(c *echo.Context, reg *registry.Registry) error {
	data, err := allWithWarnings(reg.SnapshotCurves(), reg.Warnings(), warnings.KindCurve)
	if err != nil {
		return err
	}
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getCurve(c *echo.Context, reg *registry.Registry) error {
	id := c.Param(urlParamId)
	curve, exists := reg.GetCurve(id)
	if !exists {
		return returnNotFound(c, id)
	}
	data, err := withWarnings(curve, reg.Warnings(), warnings.KindCurve, id)
	if err != nil {
		return err
	}
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

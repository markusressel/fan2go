package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/warnings"
)

func registerSensorEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/sensor")

	group.GET("/", func(c *echo.Context) error {
		return getSensors(c, reg)
	})
	group.GET("/:"+urlParamId+"/", func(c *echo.Context) error {
		return getSensor(c, reg)
	})
}

func getSensors(c *echo.Context, reg *registry.Registry) error {
	data, err := allWithWarnings(reg.SnapshotSensors(), reg.Warnings(), warnings.KindSensor)
	if err != nil {
		return err
	}
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getSensor(c *echo.Context, reg *registry.Registry) error {
	id := c.Param(urlParamId)

	sensor, exists := reg.GetSensor(id)
	if !exists {
		return returnNotFound(c, id)
	}
	data, err := withWarnings(sensor, reg.Warnings(), warnings.KindSensor, id)
	if err != nil {
		return err
	}
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

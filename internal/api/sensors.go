package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/registry"
)

func registerSensorEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/sensor")

	group.GET("/", func(c echo.Context) error {
		return getSensors(c, reg)
	})
	group.GET("/:"+urlParamId+"/", func(c echo.Context) error {
		return getSensor(c, reg)
	})
	group.POST("/", createSensor)
	group.DELETE("/:"+urlParamId+"/", deleteSensor)
}

func getSensors(c echo.Context, reg *registry.Registry) error {
	data := reg.SnapshotSensors()
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getSensor(c echo.Context, reg *registry.Registry) error {
	id := c.Param(urlParamId)

	data, exists := reg.GetSensor(id)
	if !exists {
		return returnNotFound(c, id)
	} else {
		return c.JSONPretty(http.StatusOK, data, indentationChar)
	}
}

func createSensor(c echo.Context) error {
	return returnError(c, errors.New("not yet supported"))
}

func deleteSensor(c echo.Context) error {
	return returnError(c, errors.New("not yet supported"))
}

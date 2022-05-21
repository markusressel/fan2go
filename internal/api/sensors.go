package api

import (
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/sensors"
	"net/http"
)

func registerSensorEndpoints(rest *echo.Echo) {
	group := rest.Group("/sensor")

	group.GET("/", getSensors)
	group.GET("/:"+urlParamId+"/", getSensor)
	group.POST("/", createSensor)
	group.DELETE("/:"+urlParamId+"/", deleteSensor)
}

func getSensors(c echo.Context) error {
	data := sensors.SensorMap
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getSensor(c echo.Context) error {
	id := c.Param(urlParamId)

	data, exists := sensors.SensorMap[id]
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

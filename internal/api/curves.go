package api

import (
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/curves"
	"net/http"
)

func registerCurveEndpoints(rest *echo.Echo) {
	group := rest.Group("/curve")

	group.GET("/", getCurves)
	group.GET("/:"+urlParamId+"/", getCurve)
	group.POST("/", createCurve)
	group.DELETE("/:"+urlParamId+"/", deleteCurve)
}

func getCurves(c echo.Context) error {
	data := curves.SnapshotSpeedCurveMap()
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getCurve(c echo.Context) error {
	id := c.Param(urlParamId)
	data, exists := curves.GetSpeedCurve(id)
	if !exists {
		return returnNotFound(c, id)
	} else {
		return c.JSONPretty(http.StatusOK, data, indentationChar)
	}
}

func deleteCurve(c echo.Context) error {
	return returnError(c, errors.New("not yet supported"))
}

func createCurve(c echo.Context) error {
	return returnError(c, errors.New("not yet supported"))
}

package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/registry"
)

func registerCurveEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/curve")

	group.GET("/", func(c echo.Context) error {
		return getCurves(c, reg)
	})
	group.GET("/:"+urlParamId+"/", func(c echo.Context) error {
		return getCurve(c, reg)
	})
	group.POST("/", createCurve)
	group.DELETE("/:"+urlParamId+"/", deleteCurve)
}

func getCurves(c echo.Context, reg *registry.Registry) error {
	data := reg.SnapshotCurves()
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getCurve(c echo.Context, reg *registry.Registry) error {
	id := c.Param(urlParamId)
	data, exists := reg.GetCurve(id)
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

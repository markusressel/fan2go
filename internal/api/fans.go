package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/qdm12/reprint"
)

func registerFanEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/fan")

	group.GET("/", func(c echo.Context) error {
		return getFans(c, reg)
	})
	group.GET("/:"+urlParamId+"/", func(c echo.Context) error {
		return getFan(c, reg)
	})
	group.POST("/", createFan)
	group.DELETE("/:"+urlParamId+"/", deleteFan)
}

// returns a list of all currently configured fans
func getFans(c echo.Context, reg *registry.Registry) error {
	data := reprint.This(reg.SnapshotFans())
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getFan(c echo.Context, reg *registry.Registry) error {
	id := c.Param(urlParamId)
	data, exists := reg.GetFan(id)
	if !exists {
		return returnNotFound(c, id)
	} else {
		return c.JSONPretty(http.StatusOK, data, indentationChar)
	}
}

func deleteFan(c echo.Context) error {
	return returnError(c, errors.New("not yet supported"))
}

func createFan(c echo.Context) error {
	return returnError(c, errors.New("not yet supported"))
}

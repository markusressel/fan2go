package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/warnings"
)

func registerFanEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/fan")

	group.GET("/", func(c *echo.Context) error {
		return getFans(c, reg)
	})
	group.GET("/:"+urlParamId+"/", func(c *echo.Context) error {
		return getFan(c, reg)
	})
}

// returns a list of all currently configured fans
func getFans(c *echo.Context, reg *registry.Registry) error {
	data, err := allWithWarnings(reg.SnapshotFans(), reg.Warnings(), warnings.KindFan)
	if err != nil {
		return err
	}
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getFan(c *echo.Context, reg *registry.Registry) error {
	id := c.Param(urlParamId)
	fan, exists := reg.GetFan(id)
	if !exists {
		return returnNotFound(c, id)
	}
	data, err := withWarnings(fan, reg.Warnings(), warnings.KindFan, id)
	if err != nil {
		return err
	}
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

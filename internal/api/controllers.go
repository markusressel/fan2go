package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/registry"
)

func registerControllerEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/controller")

	group.GET("/", func(c *echo.Context) error {
		return getControllers(c, reg)
	})
	group.GET("/:"+urlParamId+"/", func(c *echo.Context) error {
		return getController(c, reg)
	})
}

// returns the status of all fan controllers, by the id of the fan they control
func getControllers(c *echo.Context, reg *registry.Registry) error {
	data := map[string]status.Status{}
	for fanId, fanController := range reg.SnapshotFanControllers() {
		data[fanId] = fanController.GetStatus()
	}
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

// returns the status of the controller of the fan with the given id
func getController(c *echo.Context, reg *registry.Registry) error {
	id := c.Param(urlParamId)
	fanController, exists := reg.GetFanController(id)
	if !exists {
		return returnNotFound(c, id)
	}
	return c.JSONPretty(http.StatusOK, fanController.GetStatus(), indentationChar)
}

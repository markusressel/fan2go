package api

import (
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/fans"
	"net/http"
)

func registerFanEndpoints(rest *echo.Echo) {
	group := rest.Group("/fan")

	group.GET("/", getFans)
	group.GET("/:"+urlParamId+"/", getFan)
	group.POST("/", createFan)
	group.DELETE("/:"+urlParamId+"/", deleteFan)
}

// returns a list of all currently configured fans
func getFans(c echo.Context) error {
	data := fans.FanMap
	return c.JSONPretty(http.StatusOK, data, indentationChar)
}

func getFan(c echo.Context) error {
	id := c.Param(urlParamId)
	data, exists := fans.FanMap[id]
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

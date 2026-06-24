package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/markusressel/fan2go/internal/registry"
)

const (
	urlParamId      = "id"
	indentationChar = "  "
)

type (
	Result struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
)

func CreateRestService(reg *registry.Registry) *echo.Echo {
	echoRest := CreateWebserver()

	echoRest.GET("/alive/", isAlive)

	// Authentication
	// Group level middleware
	registerFanEndpoints(echoRest, reg)
	registerSensorEndpoints(echoRest, reg)
	registerCurveEndpoints(echoRest, reg)
	//registerWebsocketEndpoint(echoRest)

	return echoRest
}

// returns an empty "ok" answer
func isAlive(c echo.Context) error {
	return c.NoContent(http.StatusOK)
}

// return a "not found" message
func returnNotFound(c echo.Context, id string) (err error) {
	return c.JSONPretty(http.StatusNotFound, &Result{
		Name:    "Not found",
		Message: "No item with id '" + id + "' found",
	}, indentationChar)
}

// return the error message of an error
func returnError(c echo.Context, e error) (err error) {
	return c.JSONPretty(http.StatusInternalServerError, &Result{
		Name:    "Unknown Error",
		Message: e.Error(),
	}, indentationChar)
}

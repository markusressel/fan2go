package api

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"net/http"
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

func CreateRestService() *echo.Echo {
	echoRest := echo.New()
	echoRest.HideBanner = true

	// Root level middleware
	echoRest.Pre(middleware.AddTrailingSlash())

	echoRest.Use(middleware.Secure())

	echoRest.Use(middleware.Logger())
	echoRest.Use(middleware.Recover())

	//var allowedOrigins = config.CurrentConfig.Server.CORS.AllowedOrigins
	//var allowedMethods = config.CurrentConfig.Server.CORS.AllowedMethods
	//if len(allowedOrigins) <= 0 {
	//	echoRest.Use(middleware.CORS())
	//} else {
	//	echoRest.Use(middleware.CORSWithConfig(middleware.CORSConfig{
	//		AllowOrigins: allowedOrigins,
	//		AllowMethods: allowedMethods,
	//	}))
	//}

	// global auth
	//var authConf = config.CurrentConfig.Server.BasicAuth
	//if authConf.User != "" && authConf.Password != "" {
	//		basicAuthConfig := middleware.BasicAuthConfig{
	//			Skipper: func(context echo.Context) bool {
	//				return context.Path() == EndpointPathAlive
	//			},
	//			Validator: func(username string, password string, context echo.Context) (b bool, err error) {
	//				if username == authConf.User && password == authConf.Password {
	//					return true, nil
	//			}
	//				return false, nil
	//			},
	//			Realm: "Restricted",
	//		}
	//		echoRest.Use(middleware.BasicAuthWithConfig(basicAuthConfig))
	//}

	echoRest.GET("/alive/", isAlive)

	// Authentication
	// Group level middleware
	registerFanEndpoints(echoRest)
	registerSensorEndpoints(echoRest)
	registerCurveEndpoints(echoRest)
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

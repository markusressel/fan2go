package api

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func CreateWebserver() *echo.Echo {
	webserver := echo.New()
	webserver.HideBanner = true

	// Root level middleware
	webserver.Pre(middleware.AddTrailingSlash())

	//webserver.Use(middleware.Logger())
	webserver.Use(middleware.Secure())

	//webserver.Use(middleware.Logger())
	webserver.Use(middleware.Recover())

	return webserver
}

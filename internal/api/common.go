package api

import (
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

func CreateWebserver() *echo.Echo {
	webserver := echo.New()

	// Root level middleware
	webserver.Pre(middleware.AddTrailingSlash())

	//webserver.Use(middleware.Logger())
	webserver.Use(middleware.Secure())

	//webserver.Use(middleware.Logger())
	webserver.Use(middleware.Recover())

	return webserver
}

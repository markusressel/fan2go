package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/warnings"
)

func registerWarningEndpoints(rest *echo.Echo, reg *registry.Registry) {
	group := rest.Group("/warnings")

	group.GET("/", func(c *echo.Context) error {
		return getWarnings(c, reg)
	})
}

// returns all current warnings of fans, sensors and curves, each with the kind and id of its item
func getWarnings(c *echo.Context, reg *registry.Registry) error {
	return c.JSONPretty(http.StatusOK, reg.Warnings().All(), indentationChar)
}

// withWarnings returns the JSON object of the given item with an additional "warnings" field that holds the
// current warnings of the item (an empty list if there are none).
func withWarnings(item any, store *warnings.Store, kind warnings.Kind, id string) (json.RawMessage, error) {
	itemJson, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	warningsJson, err := json.Marshal(store.For(kind, id))
	if err != nil {
		return nil, err
	}
	if len(itemJson) < 2 || itemJson[0] != '{' || itemJson[len(itemJson)-1] != '}' {
		return nil, fmt.Errorf("%s %s: JSON is not an object", kind, id)
	}

	var result bytes.Buffer
	result.Write(itemJson[:len(itemJson)-1])
	if len(bytes.TrimSpace(itemJson[1:len(itemJson)-1])) > 0 {
		result.WriteByte(',')
	}
	result.WriteString(`"warnings":`)
	result.Write(warningsJson)
	result.WriteByte('}')
	return result.Bytes(), nil
}

// allWithWarnings returns the JSON objects of the given items (by id) with their warnings, see withWarnings.
func allWithWarnings[T any](items map[string]T, store *warnings.Store, kind warnings.Kind) (map[string]json.RawMessage, error) {
	result := make(map[string]json.RawMessage, len(items))
	for id, item := range items {
		itemJson, err := withWarnings(item, store, kind, id)
		if err != nil {
			return nil, err
		}
		result[id] = itemJson
	}
	return result, nil
}

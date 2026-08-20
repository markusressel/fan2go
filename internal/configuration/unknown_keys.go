package configuration

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/markusressel/fan2go/internal/ui"
)

// WarnUnknownKeys checks unused configuration keys, determines if they are sections,
// finds suggestions for typos, and prints warnings.
func WarnUnknownKeys(cfg interface{}, unusedKeys []string, getValue func(string) interface{}) {
	re := regexp.MustCompile(`\[(\d+)\]`)
	for _, unused := range unusedKeys {
		key := strings.ToLower(unused)
		viperKey := re.ReplaceAllString(key, ".$1")
		val := getValue(viperKey)

		isSection := false
		if val != nil {
			kind := reflect.TypeOf(val).Kind()
			if kind == reflect.Map || kind == reflect.Slice {
				isSection = true
			}
		}

		suggestion := ""
		pathParts := strings.Split(viperKey, ".")
		if len(pathParts) > 0 {
			unknownPart := pathParts[len(pathParts)-1]
			parentPath := pathParts[:len(pathParts)-1]

			parentType := getParentType(parentPath, reflect.TypeOf(cfg))
			validKeys := getValidKeys(parentType)

			closest := getClosestMatch(unknownPart, validKeys)
			if closest != "" {
				suggestion = fmt.Sprintf(" - did you mean '%s'?", closest)
			}
		}

		if isSection {
			ui.Warning("Unknown configuration section (and its contents): %s%s", key, suggestion)
		} else {
			ui.Warning("Unknown configuration key: %s%s", key, suggestion)
		}
	}
}

func levenshtein(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for j := 1; j <= len(b); j++ {
		for i := 1; i <= len(a); i++ {
			if a[i-1] == b[j-1] {
				d[i][j] = d[i-1][j-1]
			} else {
				min := d[i-1][j] + 1
				if d[i][j-1]+1 < min {
					min = d[i][j-1] + 1
				}
				if d[i-1][j-1]+1 < min {
					min = d[i-1][j-1] + 1
				}
				d[i][j] = min
			}
		}
	}
	return d[len(a)][len(b)]
}

func getClosestMatch(target string, options []string) string {
	closest := ""
	minDist := -1
	for _, opt := range options {
		dist := levenshtein(target, opt)
		if minDist == -1 || dist < minDist {
			minDist = dist
			closest = opt
		}
	}
	if minDist != -1 && minDist <= 3 && len(target) > 2 {
		return closest
	}
	return ""
}

func getValidKeys(parentType reflect.Type) []string {
	var keys []string
	if parentType == nil {
		return keys
	}
	for parentType.Kind() == reflect.Ptr {
		parentType = parentType.Elem()
	}
	if parentType.Kind() != reflect.Struct {
		return keys
	}
	for i := 0; i < parentType.NumField(); i++ {
		field := parentType.Field(i)
		if field.PkgPath != "" { // unexported
			continue
		}

		name := ""
		for _, tagKey := range []string{"mapstructure", "json"} {
			tag := field.Tag.Get(tagKey)
			if tag != "" && tag != "-" {
				name = strings.Split(tag, ",")[0]
				break
			}
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		keys = append(keys, name)
	}
	return keys
}

func getParentType(path []string, currentType reflect.Type) reflect.Type {
	for currentType.Kind() == reflect.Ptr {
		currentType = currentType.Elem()
	}
	if len(path) == 0 {
		return currentType
	}
	part := path[0]

	switch currentType.Kind() {
	case reflect.Struct:
		for i := 0; i < currentType.NumField(); i++ {
			field := currentType.Field(i)
			name := strings.ToLower(field.Name)
			for _, tagKey := range []string{"mapstructure", "json"} {
				tag := field.Tag.Get(tagKey)
				if tag != "" && tag != "-" {
					tagOpts := strings.Split(tag, ",")
					if strings.ToLower(tagOpts[0]) == part {
						name = strings.ToLower(tagOpts[0])
						break
					}
				}
			}
			if name == part {
				return getParentType(path[1:], field.Type)
			}
		}
		return nil
	case reflect.Slice, reflect.Array, reflect.Map:
		return getParentType(path[1:], currentType.Elem())
	}
	return nil
}

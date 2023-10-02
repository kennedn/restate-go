package common

import "net/http"

type Route struct {
	Path    string
	Handler func(http.ResponseWriter, *http.Request)
}

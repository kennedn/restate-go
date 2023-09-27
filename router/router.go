package router

import (
	"restate-go/device/tvcom"

	"github.com/gorilla/mux"
)

func NewRouter() *mux.Router {

	routes, err := tvcom.Routes(1000, "ws://192.168.1.161/")
	if err != nil {
		return nil
	}

	r := mux.NewRouter()
	for _, route := range routes {
		r.HandleFunc(route.Path, route.Handler)
	}

	return r
}

package router

import (
	"restate-go/internal/device/meross"
	"restate-go/internal/device/tvcom"
	router "restate-go/internal/router/common"

	"github.com/gorilla/mux"
)

func NewRouter() *mux.Router {

	var routes, tmpRoutes []router.Route
	var err error
	tmpRoutes, err = tvcom.Routes(1000, "ws://192.168.1.161/", "")
	if err != nil {
		return nil
	}
	routes = append(routes, tmpRoutes...)

	tmpRoutes, err = meross.Routes(1000, "192.168.1.140", "office", "bulb")
	if err != nil {
		return nil
	}
	routes = append(routes, tmpRoutes...)

	tmpRoutes, err = meross.Routes(1000, "192.168.1.142", "hall_up", "bulb")
	if err != nil {
		return nil
	}
	routes = append(routes, tmpRoutes...)

	tmpRoutes, err = meross.Routes(1000, "192.168.1.150", "thermostat", "socket")
	if err != nil {
		return nil
	}
	routes = append(routes, tmpRoutes...)

	r := mux.NewRouter()
	for _, route := range routes {
		r.HandleFunc(route.Path, route.Handler)
	}

	return r
}

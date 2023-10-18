package router

import (
	"log"
	"os"
	device "restate-go/internal/device/common"
	"restate-go/internal/device/meross"
	"restate-go/internal/device/snowdon"
	"restate-go/internal/device/tvcom"
	"restate-go/internal/device/wol"
	router "restate-go/internal/router/common"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

func NewRouter() *mux.Router {

	var routes, tmpRoutes []router.Route
	var err error
	tmpRoutes, err = tvcom.Routes(1000, "ws://192.168.1.161/", "")
	if err != nil {
		log.Fatalf("Could not read meross input")
	}
	routes = append(routes, tmpRoutes...)

	merossConfigFile, err := os.ReadFile("./internal/device/input.yaml")
	if err != nil {
		log.Fatalf("Could not read meross input")
	}

	deviceConfig := device.Config{}

	if err := yaml.Unmarshal(merossConfigFile, &deviceConfig); err != nil {
		log.Fatalf("Could not read meross input")
	}

	tmpRoutes, err = meross.Routes(&deviceConfig, "")
	if err != nil {
		log.Fatalf("Could not read meross input")
	}
	routes = append(routes, tmpRoutes...)

	tmpRoutes, err = wol.Routes(&deviceConfig)
	if err != nil {
		log.Fatalf("Could not read wol input")
	}
	routes = append(routes, tmpRoutes...)

	tmpRoutes, err = snowdon.Routes(&deviceConfig)
	if err != nil {
		log.Fatalf("Could not read wol input")
	}
	routes = append(routes, tmpRoutes...)

	r := mux.NewRouter()
	for _, route := range routes {
		r.HandleFunc("/"+deviceConfig.ApiVersion+route.Path, route.Handler)
	}

	return r
}

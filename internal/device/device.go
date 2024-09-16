package device

import (
	"errors"
	"net/http"
	"strings"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	"github.com/kennedn/restate-go/internal/device/alert"
	"github.com/kennedn/restate-go/internal/device/common"
	"github.com/kennedn/restate-go/internal/device/hikvision"
	"github.com/kennedn/restate-go/internal/device/meross"
	"github.com/kennedn/restate-go/internal/device/snowdon"
	"github.com/kennedn/restate-go/internal/device/tvcom"
	"github.com/kennedn/restate-go/internal/device/wol"
	router "github.com/kennedn/restate-go/internal/router/common"
)

type Device interface {
	Routes(config *config.Config) ([]router.Route, error)
}

type Devices struct {
	routes []router.Route
}

var (
	devices = []Device{
		&alert.Device{},
		&meross.Device{},
		&snowdon.Device{},
		&tvcom.Device{},
		&wol.Device{},
		&hikvision.Device{},
	}
)

func (d *Devices) Routes(config *config.Config) ([]router.Route, error) {

	for _, device := range devices {
		tmpRoutes, _ := device.Routes(config)

		// Prepend API version to route paths
		for i, r := range tmpRoutes {
			tmpRoutes[i].Path = "/" + config.ApiVersion + r.Path
		}

		d.routes = append(d.routes, tmpRoutes...)
	}

	if len(d.routes) == 0 {
		logging.Log(logging.Error, "No routes returned from parsed config")
		return []router.Route{}, errors.New("no routes returned from parsed config")
	}

	d.routes = append(d.routes, router.Route{
		Path:    "/" + config.ApiVersion,
		Handler: d.handler,
	})

	d.routes = append(d.routes, router.Route{
		Path:    "/" + config.ApiVersion + "/",
		Handler: d.handler,
	})

	return d.routes, nil
}

// Use the number of '/' characters present in the route Paths to extract top level path names
func (d *Devices) getTopLevelRouteNames() []string {
	topLevelNames := []string{}
	for _, r := range d.routes {
		parts := strings.Split(r.Path, "/")

		if len(parts) == 3 && parts[2] != "" {
			topLevelNames = append(topLevelNames, parts[2])
		}
	}
	return topLevelNames
}

func (d *Devices) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() {
		common.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method != http.MethodGet {
		httpCode, jsonResponse = common.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	httpCode, jsonResponse = common.SetJSONResponse(http.StatusOK, "OK", d.getTopLevelRouteNames())
}

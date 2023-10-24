package device

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/kennedn/restate-go/internal/common/logging"
	"github.com/kennedn/restate-go/internal/device/alert"
	common "github.com/kennedn/restate-go/internal/device/common"
	"github.com/kennedn/restate-go/internal/device/meross"
	"github.com/kennedn/restate-go/internal/device/snowdon"
	"github.com/kennedn/restate-go/internal/device/tvcom"
	"github.com/kennedn/restate-go/internal/device/wol"
	router "github.com/kennedn/restate-go/internal/router/common"

	"gopkg.in/yaml.v3"
)

type Device interface {
	Routes(config *common.Config) ([]router.Route, error)
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
	}
)

func (d *Devices) Routes() ([]router.Route, error) {
	envConfigPath := os.Getenv("RESTATECONFIG")

	config, err := os.ReadFile(envConfigPath)
	if err != nil {
		logging.Log(logging.Error, "Could not read config path (RESTATECONFIG=%s)", envConfigPath)
		return []router.Route{}, err
	}

	deviceConfig := common.Config{}

	if err := yaml.Unmarshal(config, &deviceConfig); err != nil {
		logging.Log(logging.Error, "Could not parse config path (RESTATECONFIG=%s)", envConfigPath)
		return []router.Route{}, err
	}

	for _, device := range devices {
		tmpRoutes, _ := device.Routes(&deviceConfig)

		// Prepend API version to route paths
		for i, r := range tmpRoutes {
			tmpRoutes[i].Path = "/" + deviceConfig.ApiVersion + r.Path
		}

		d.routes = append(d.routes, tmpRoutes...)
	}

	if len(d.routes) == 0 {
		logging.Log(logging.Error, "No routes returned from parsed config")
		return []router.Route{}, errors.New("no routes returned from parsed config")
	}

	d.routes = append(d.routes, router.Route{
		Path:    "/" + deviceConfig.ApiVersion,
		Handler: d.handler,
	})

	d.routes = append(d.routes, router.Route{
		Path:    "/" + deviceConfig.ApiVersion + "/",
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

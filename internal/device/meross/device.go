package meross

import (
	"errors"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/device/meross/bulb"
	"github.com/kennedn/restate-go/internal/device/meross/radiator"
	"github.com/kennedn/restate-go/internal/device/meross/thermostat"
	router "github.com/kennedn/restate-go/internal/router/common"
)

type Device struct{}

var errNoRoutes = errors.New("no routes found in config")

// Routes aggregates routes from all Meross device variants.
func (d *Device) Routes(cfg *config.Config) ([]router.Route, error) {
	var routes []router.Route
	builders := []func(*config.Config) ([]router.Route, error){
		bulb.Routes,
		thermostat.Routes,
		radiator.Routes,
	}

	for _, build := range builders {
		variantRoutes, err := build(cfg)
		if err != nil {
			return nil, err
		}
		routes = append(routes, variantRoutes...)
	}

	if len(routes) == 0 {
		return nil, errNoRoutes
	}

	return routes, nil
}

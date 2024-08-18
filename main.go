package main

import (
	"net/http"
	"os"

	config "github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	"github.com/kennedn/restate-go/internal/device"
	"github.com/kennedn/restate-go/internal/mqtt/frigate"
	"github.com/kennedn/restate-go/internal/router"
	"gopkg.in/yaml.v3"
)

func main() {
	envConfigPath := os.Getenv("RESTATECONFIG")

	configBytes, err := os.ReadFile(envConfigPath)
	if err != nil {
		logging.Log(logging.Error, "Could not read config path (RESTATECONFIG=%s)", envConfigPath)
		os.Exit(1)
	}

	configMap := config.Config{}

	if err := yaml.Unmarshal(configBytes, &configMap); err != nil {
		logging.Log(logging.Error, "Could not parse config path (RESTATECONFIG=%s)", envConfigPath)
		os.Exit(1)
	}

	devices := &device.Devices{}

	routes, err := devices.Routes(&configMap)
	if err != nil {
		logging.Log(logging.Error, "Failed to start server: %v", err)
		os.Exit(1)
	}

	r := router.NewRouter(routes)
	if r == nil {
		logging.Log(logging.Error, "Failed to create router")
		os.Exit(1)
	}

	frigate := &frigate.Device{}
	listeners, err := frigate.Listeners(&configMap)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		os.Exit(1)
	}
	for _, listener := range listeners {
		listener.Listen()
	}

	logging.Log(logging.Info, "Server listening on :8080")
	logging.Log(logging.Error, http.ListenAndServe(":8080", r).Error())
}

package main

import (
	"log"
	"net/http"
	"os"

	"github.com/kennedn/restate-go/internal/common/logging"
	"github.com/kennedn/restate-go/internal/device"
	"github.com/kennedn/restate-go/internal/router"
)

func main() {

	devices := &device.Devices{}

	routes, err := devices.Routes()
	if err != nil {
		logging.Log(logging.Error, "Failed to start server: %v", err)
		os.Exit(1)
	}

	r := router.NewRouter(routes)
	if r == nil {
		log.Fatal("failed to create router")
	}
	logging.Log(logging.Info, "Server listening on :8080")
	logging.Log(logging.Error, http.ListenAndServe(":8080", r).Error())
}

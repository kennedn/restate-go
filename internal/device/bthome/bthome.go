// Package bthome provides an abstraction for making HTTP calls to control bthome branded smart bulbs and sockets.
package bthome

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	router "github.com/kennedn/restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

// namedStatus associates a devices name with its status.
type namedStatus struct {
	Name   string         `json:"name"`
	Status StatusResponse `json:"status"`
}

// status is a representation of the state of a bthome device
type StatusResponse struct {
	Packet      string `json:"packet"`
	Battery     string `json:"battery,omitempty"`
	Temperature string `json:"temperature,omitempty"`
	Humidity    string `json:"humidity,omitempty"`
	Voltage     string `json:"voltage,omitempty"`
}

// bthome represents a bthome device configuration with name, host, device type, timeout, and base configuration.
type bthome struct {
	Name       string          `yaml:"name"`
	MacAddress string          `yaml:"macAddress"`
	Host       string          `yaml:"host"`
	Timeout    uint            `yaml:"timeout"`
	Status     *StatusResponse `yaml:"statusResponse"`
	Base       base            `yaml:"base"`
}

// base represents a list of bthome devices, endpoints and common configuration
type base struct {
	Devices []*bthome `yaml:"devices"`
}

type Device struct{}

func parseBTHomeData(data []byte) (*StatusResponse, error) {
	var status StatusResponse
	i := 0

	for i < len(data) {
		if len(data)-i < 2 {
			return nil, errors.New("incomplete measurement data")
		}

		objectID := data[i]
		i++

		var value any
		var length int

		switch objectID {
		case 0x00: // packet id
			if len(data)-i < 1 {
				return nil, errors.New("incomplete data")
			}
			value = uint8(data[i])
			status.Packet = fmt.Sprintf("%d", value)
			length = 1
		case 0x01: // battery
			if len(data)-i < 1 {
				return nil, errors.New("incomplete data")
			}
			value = uint8(data[i])
			status.Battery = fmt.Sprintf("%d", value)
			length = 1
		case 0x02: // Temperature
			if len(data)-i < 2 {
				return nil, errors.New("incomplete data")
			}
			value = int16(data[i]) | int16(data[i+1])<<8
			status.Temperature = fmt.Sprintf("%.2f", float64(value.(int16))*0.01)
			length = 2
		case 0x03: // Humidity
			if len(data)-i < 2 {
				return nil, errors.New("incomplete data")
			}
			value = uint16(data[i]) | uint16(data[i+1])<<8
			status.Humidity = fmt.Sprintf("%.2f", float64(value.(uint16))*0.01)
			length = 2
		case 0x0C: // voltage
			if len(data)-i < 2 {
				return nil, errors.New("incomplete data")
			}
			value = uint16(data[i]) | uint16(data[i+1])<<8
			status.Voltage = fmt.Sprintf("%.2f", float64(value.(uint16))*0.001)
			length = 2
		default:
			return nil, fmt.Errorf("unsupported object ID: 0x%X", objectID)
		}
		i += length
	}

	return &status, nil
}

// websocketWriteWithResponse connected to a websocket and sorts filters received data based on current devices mac
// It returns the response or an error if the response is not received within the specified timeout.
func (m *bthome) websocketConnectWithResponse() (*StatusResponse, error) {
	status, err := m.Base.websocketConnectWithResponses([]*bthome{m})
	if err != nil {
		return nil, err

	}
	return &status[0].Status, nil
}

// websocketWriteWithResponses connected to a websocket and filters received data based on a list of bthome devices
// It returns an array of namedStatus or an error if the response is not received within the specified timeout.
func (b *base) websocketConnectWithResponses(devices []*bthome) ([]*namedStatus, error) {
	macNames := map[string]string{}
	deviceStrings := map[string]string{}
	statusResponses := []*namedStatus{}

	for _, device := range devices {
		macNames[device.MacAddress] = device.Name
	}

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+devices[0].Host, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(time.Duration(devices[0].Timeout) * time.Millisecond))

	for {
		if len(deviceStrings) == len(devices) {
			break
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			continue
		}

		hexString := string(message)
		if deviceName, exists := macNames[hexString[:12]]; exists {
			// Skip MAC + D2 FC 40 in bthome packet
			deviceStrings[deviceName] = hexString[18:]
		}
	}

	for name, hexString := range deviceStrings {
		bthomeData, err := hex.DecodeString(hexString)
		if err != nil {
			log.Printf("Invalid hex string: %v", err)
			continue
		}
		status, err := parseBTHomeData(bthomeData)
		if err != nil {
			log.Printf("Invalid bthome data: %v", err)
			continue
		}

		statusResponses = append(statusResponses, &namedStatus{
			Name:   name,
			Status: *status,
		})
	}

	if len(statusResponses) == 0 {
		return nil, errors.New("no devices could be parsed")
	}
	return statusResponses, nil
}

// Routes generates routes for bthome device control based on a provided configuration.
func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config)
	return routes, err
}

// generateRoutesFromConfig generates routes and base configuration from a provided configuration and internal config file.
func routes(config *config.Config) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	for _, d := range config.Devices {
		if d.Type != "bthome" {
			continue
		}
		bthome := bthome{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &bthome); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if bthome.Name == "" || bthome.MacAddress == "" || bthome.Host == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		// Strip colons out of mac address for easier comparisons later on
		bthome.MacAddress = strings.ReplaceAll(bthome.MacAddress, ":", "")

		routes = append(routes, router.Route{
			Path:    "/" + bthome.Name,
			Handler: bthome.handler,
		})

		base.Devices = append(base.Devices, &bthome)

		logging.Log(logging.Info, "Found device \"%s\"", bthome.Name)
	}

	if len(routes) == 0 {
		return nil, []router.Route{}, errors.New("no routes found in config")
	} else if len(routes) == 1 {
		return &base, routes, nil
	}

	for i, r := range routes {
		routes[i].Path = "/bthome" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/bthome",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/bthome/",
		Handler: base.handler,
	})
	return &base, routes, nil
}

// Handler is the HTTP handler for bthome device control.
func (m *bthome) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var status *StatusResponse
	var err error

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", []string{"status"})
		return
	}

	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	request := device.Request{}

	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed Or Empty JSON Body", nil)
			return
		}
	} else {
		if err := schema.NewDecoder().Decode(&request, r.URL.Query()); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed or empty query string", nil)
			return
		}
	}

	if request.Code != "status" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}

	status, err = m.websocketConnectWithResponse()
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", *status)

}

// getDeviceNames returns the names of all Meross devices in the base configuration.
func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

// getDevice retrieves a bthome device by its name.
func (b *base) getDevice(name string) *bthome {
	for _, d := range b.Devices {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// Handler is the HTTP handler for handling requests to base route
func (b *base) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", b.getDeviceNames())
		return
	}

	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	request := device.Request{}

	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed Or Empty JSON Body", nil)
			return
		}
	} else {
		if err := schema.NewDecoder().Decode(&request, r.URL.Query()); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed or empty query string", nil)
			return
		}
	}

	if request.Hosts == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: hosts", nil)
		return
	}

	hosts := strings.Split(strings.ReplaceAll(request.Hosts, " ", ""), ",")

	var devices []*bthome
DUPLICATE_DEVICE:
	for _, h := range hosts {
		m := b.getDevice(h)
		if m == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter: hosts (Device '%s' does not exist)", h), nil)
			return
		}
		for _, device := range devices {
			if m == device {
				continue DUPLICATE_DEVICE
			}
		}
		devices = append(devices, m)
	}

	status, err := b.websocketConnectWithResponses(devices)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}
	sort.SliceStable(status, func(i int, j int) bool {
		return status[i].Name < status[j].Name
	})

	responseStruct := struct {
		Devices []*namedStatus `json:"devices,omitempty"`
	}{}

	responseStruct.Devices = status

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", responseStruct)

}

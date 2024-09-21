// Package hikvision provides an abstraction for making HTTP calls to control Hikvision branded smart bulbs and sockets.
package hikvision

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	router "github.com/kennedn/restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"gopkg.in/yaml.v3"
)

type request struct {
	Code  string `json:"code"`
	Value string `json:"value,omitempty"`
	Hosts string `json:"hosts,omitempty"`
}

// namedStatus associates a devices name with its status.
type namedStatus struct {
	Name   string `json:"name"`
	Status any    `json:"status"`
}

// Struct for the main SupplementLight element
type supplementLightResponseGet struct {
	XMLName                         xml.Name                 `xml:"SupplementLight"`
	Version                         string                   `xml:"version,attr"`
	XMLNS                           string                   `xml:"xmlns,attr"`
	SupplementLightMode             string                   `xml:"supplementLightMode"`
	MixedLightBrightnessRegulatMode string                   `xml:"mixedLightBrightnessRegulatMode"`
	WhiteLightBrightness            int                      `xml:"whiteLightBrightness"`
	IrLightBrightness               int                      `xml:"irLightBrightness"`
	EventIntelligenceModeCfg        eventIntelligenceModeCfg `xml:"EventIntelligenceModeCfg"`
}

// Struct for the nested EventIntelligenceModeCfg element
type eventIntelligenceModeCfg struct {
	BrightnessRegulatMode string `xml:"brightnessRegulatMode"`
	WhiteLightBrightness  int    `xml:"whiteLightBrightness"`
	IrLightBrightness     int    `xml:"irLightBrightness"`
}

// hikvision represents a Hikvision device configuration with name, host, device type, timeout, and base configuration.
type hikvision struct {
	Name        string `yaml:"name"`
	Host        string `yaml:"host"`
	Timeout     uint   `yaml:"timeoutMs"`
	DefaultMode string `yaml:"defaultMode"`
	User        string `yaml:"user"`
	Password    string `yaml:"password"`
	Base        base
}

type deviceValues struct {
	Device *hikvision
	Value  string
}

// base represents a list of Hikvision devices, endpoints and common configuration
type base struct {
	SupplementLightTemplate string `yaml:"supplementLightTemplate"`
	IrcutTemplate           string `yaml:"IrcutTemplate"`
	Devices                 []*hikvision
}

type statusResponse struct {
	OnOff               string `json:"onoff"`
	SupplementLightMode string `json:"supplementlightmode"`
}

type Device struct{}

// Routes generates routes for Hikvision device control based on a provided configuration.
func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config)
	return routes, err
}

// generateRoutesFromConfig generates routes and base configuration from a provided configuration and internal config file.
func routes(config *config.Config) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{
		SupplementLightTemplate: "<SupplementLight><supplementLightMode>%s</supplementLightMode></SupplementLight>",
		IrcutTemplate:           "<IrcutFilter><IrcutFilterType>%s</IrcutFilterType></IrcutFilter>",
	}

	for _, d := range config.Devices {
		if d.Type != "hikvision" {
			continue
		}
		hikvision := hikvision{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &hikvision); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if hikvision.Name == "" || hikvision.Host == "" || hikvision.User == "" || hikvision.Password == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		if hikvision.DefaultMode != "irLight" && hikvision.DefaultMode != "eventIntelligence" {
			logging.Log(logging.Info, "Unable to load device: DefaultMode must be either 'irLight' or 'eventIntelligence'")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + hikvision.Name,
			Handler: hikvision.handler,
		})

		base.Devices = append(base.Devices, &hikvision)

		logging.Log(logging.Info, "Found device \"%s\"", hikvision.Name)
	}

	if len(routes) == 0 {
		return nil, []router.Route{}, errors.New("no routes found in config")
	} else if len(routes) == 1 {
		return &base, routes, nil
	}

	for i, r := range routes {
		routes[i].Path = "/hikvision" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/hikvision",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/hikvision/",
		Handler: base.handler,
	})
	return &base, routes, nil
}

// getCodes returns a list of control codes for a Hikvision device.
func getCodes() []string {
	return []string{"toggle", "status"}
}

// check if passed code is valid
func validCode(code string) bool {
	return slices.Contains([]string{"toggle", "status"}, code)
}

// check if value is valid
func validValue(value string) bool {
	return slices.Contains([]string{"irLight", "eventIntelligence", "colorVuWhiteLight"}, value)
}

// get constructs and sends a GET request to a Hikvision device and will return a flattened status when the method is equal to GET.
func (m *hikvision) get() (*supplementLightResponseGet, error) {
	client := &http.Client{
		Timeout: time.Duration(m.Timeout) * time.Millisecond,
	}

	req, err := http.NewRequest("GET", "http://"+m.Host+"/ISAPI/Image/channels/1/supplementLight", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/xml")
	req.SetBasicAuth(m.User, m.Password)

	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	response := supplementLightResponseGet{}

	if err := xml.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	return &response, err
}

// put constructs and sends a PUT request to a Hikvision device
func (m *hikvision) put(value string) error {
	client := &http.Client{
		Timeout: time.Duration(m.Timeout) * time.Millisecond,
	}

	if value == "" {
		return errors.New("value is required")
	}

	payload := []byte(fmt.Sprintf(m.Base.SupplementLightTemplate, value))
	req, err := http.NewRequest("PUT", "http://"+m.Host+"/ISAPI/Image/channels/1/supplementLight", bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/xml")
	req.SetBasicAuth(m.User, m.Password)

	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return err
	}

	return nil
}

func (m *hikvision) ircutPut(filterType string) error {
	if (filterType != "auto" && filterType != "night" && filterType != "day") || filterType == "" {
		return errors.New("filterType must be auto, night or day")
	}

	client := &http.Client{
		Timeout: time.Duration(m.Timeout) * time.Millisecond,
	}

	payload := []byte(fmt.Sprintf(m.Base.IrcutTemplate, filterType))
	req, err := http.NewRequest("PUT", "http://"+m.Host+"/ISAPI/Image/channels/1/ircutFilter", bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/xml")
	req.SetBasicAuth(m.User, m.Password)

	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return err
	}

	return nil

}

func (m *hikvision) supplementLightModeIsDefault(supplementLightMode string) bool {
	return supplementLightMode == m.DefaultMode
}

// Handler is the HTTP handler for Hikvision device control.
func (m *hikvision) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var status *supplementLightResponseGet
	var err error

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", getCodes())
		return
	}

	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	request := request{}

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

	if !validCode(request.Code) {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}

	switch request.Code {
	case "status":
		status, err = m.get()
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}
		statusResp := statusResponse{
			SupplementLightMode: status.SupplementLightMode,
		}
		if m.supplementLightModeIsDefault(status.SupplementLightMode) {
			statusResp.OnOff = "off"
		} else {
			statusResp.OnOff = "on"
		}
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", statusResp)
		return
	case "toggle":
		if request.Value != "" && !validValue(request.Value) {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: value", nil)
			return
		}
		if request.Value == "" {
			status, err = m.get()
			if err != nil {
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
				return
			}
			if m.supplementLightModeIsDefault(status.SupplementLightMode) {
				request.Value = "colorVuWhiteLight"
			} else {
				request.Value = m.DefaultMode
			}
		}
		irCutFilterType := "auto"
		if request.Value == "colorVuWhiteLight" {
			irCutFilterType = "night"
		}
		err = m.ircutPut(irCutFilterType)
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		err = m.put(request.Value)
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
}

// getDeviceNames returns the names of all Hikvision devices in the base configuration.
func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

// getDevice retrieves a Hikvision device by its name.
func (b *base) getDevice(name string) *hikvision {
	for _, d := range b.Devices {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// multiHTTP performs HTTP requests to control multiple Hikvision devices in parallel and returns their statuses.
func (b *base) multiHTTP(devices []*deviceValues, method string) chan *namedStatus {
	if method != "GET" && method != "PUT" {
		return nil
	}

	wg := sync.WaitGroup{}
	responses := make(chan *namedStatus, len(devices))

	for _, d := range devices {
		wg.Add(1)
		go func(d *deviceValues, method string) {
			defer wg.Done()
			response := namedStatus{
				Name:   d.Device.Name,
				Status: nil,
			}

			var status *supplementLightResponseGet
			var err error
			if method == "GET" {
				status, err = d.Device.get()
			} else if method == "PUT" {
				irCutFilterType := "auto"
				if d.Value == "colorVuWhiteLight" {
					irCutFilterType = "night"
				}
				err = d.Device.ircutPut(irCutFilterType)
				if err == nil {
					err = d.Device.put(d.Value)
				}
			}
			if err != nil {
				responses <- &response
				return
			}
			if status == nil {
				response.Status = "OK"
			} else {
				statusResp := statusResponse{
					SupplementLightMode: status.SupplementLightMode,
				}
				if d.Device.supplementLightModeIsDefault(status.SupplementLightMode) {
					statusResp.OnOff = "off"
				} else {
					statusResp.OnOff = "on"
				}
				response.Status = statusResp
			}
			responses <- &response
		}(d, method)
	}

	go func() {
		wg.Wait()
		close(responses)
	}()

	return responses
}

// Handler is the HTTP handler for handling requests to control multiple Hikvision devices.
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

	request := request{}

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

	if !validCode(request.Code) {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}

	if request.Hosts == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: hosts", nil)
		return
	}

	hosts := strings.Split(strings.ReplaceAll(request.Hosts, " ", ""), ",")

	var devices []*deviceValues
	for _, h := range hosts {
		d := b.getDevice(h)
		if d == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter: hosts (Device '%s' does not exist)", h), nil)
			return
		}
		devices = append(devices, &deviceValues{
			Device: d,
			Value:  d.DefaultMode,
		})
	}

	if request.Value != "" && !validValue(request.Value) {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: value", nil)
		return
	}

	switch request.Code {
	case "status":
		responses := b.multiHTTP(devices, "GET")

		responseStruct := struct {
			Devices []*namedStatus `json:"devices,omitempty"`
			Errors  []string       `json:"errors,omitempty"`
		}{}

		for r := range responses {
			if r.Status == nil {
				responseStruct.Errors = append(responseStruct.Errors, r.Name)
				continue
			}
			responseStruct.Devices = append(responseStruct.Devices, r)
		}

		sort.SliceStable(responseStruct.Devices, func(i int, j int) bool {
			return responseStruct.Devices[i].Name < responseStruct.Devices[j].Name
		})

		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", responseStruct)
	case "toggle":
		valueTally := int64(0)
		ledOnDevices := []*deviceValues{}

		if request.Value == "" {
			responses := b.multiHTTP(devices, "GET")

			for r := range responses {
				if r.Status == nil {
					continue
				}
				d := b.getDevice(r.Name)

				statusBytes, err := json.Marshal(r.Status)
				if err != nil {
					httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
					return
				}

				response := statusResponse{}
				if err := json.Unmarshal(statusBytes, &response); err != nil {
					httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
					return
				}

				if d.supplementLightModeIsDefault(response.SupplementLightMode) {
					valueTally++
				}

				ledOnDevices = append(ledOnDevices, &deviceValues{
					Device: d,
					Value:  "colorVuWhiteLight",
				})
			}

			// Each device votes for next state, if most devices are in default state (off), all device LEDs will be toggled on and vice versa
			if len(devices) == 0 {
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
				return
			}
		} else {
			for i := range devices {
				devices[i].Value = request.Value
			}
		}

		var responses chan *namedStatus
		if valueTally <= int64(len(devices))/2 {
			responses = b.multiHTTP(devices, "PUT")
		} else {
			responses = b.multiHTTP(ledOnDevices, "PUT")
		}

		hik := []*hikvision{}
		for r := range responses {
			if r.Status == nil {
				continue
			}
			hik = append(hik, b.getDevice(r.Name))
		}

		if len(hik) == 0 {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		} else {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
		}
	}
}

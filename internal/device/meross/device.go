package meross

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	device "restate-go/internal/device/common"
	router "restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"gopkg.in/yaml.v3"
)

type status struct {
	Onoff       int64 `json:"onoff"`
	RGB         int64 `json:"rgb,omitempty"`
	Temperature int64 `json:"temperature,omitempty"`
	Luminance   int64 `json:"luminance,omitempty"`
}

type namedStatus struct {
	Name   string `json:"name"`
	Status any    `json:"status"`
}

type rawStatus struct {
	Payload struct {
		All struct {
			Digest struct {
				Togglex []struct {
					Onoff int64 `json:"onoff"`
				} `json:"togglex"`
				Light struct {
					RGB         int64 `json:"rgb"`
					Temperature int64 `json:"temperature"`
					Luminance   int64 `json:"luminance"`
				} `json:"light"`
			} `json:"digest"`
		} `json:"all"`
	} `json:"payload"`
}

type endpoint struct {
	Code             string   `yaml:"code"`
	SupportedDevices []string `yaml:"supportedDevices"`
	MinValue         int64    `yaml:"minValue,omitempty"`
	MaxValue         int64    `yaml:"maxValue,omitempty"`
	Namespace        string   `yaml:"namespace"`
	Template         string   `yaml:"template"`
}

type meross struct {
	Name       string `yaml:"name"`
	Host       string `yaml:"host"`
	DeviceType string `yaml:"deviceType"`
	Timeout    uint   `yaml:"timeoutMs"`
	Base       base
}

type base struct {
	BaseTemplate string      `yaml:"baseTemplate"`
	Endpoints    []*endpoint `yaml:"endpoints"`
	Devices      []*meross
}

func Routes(config *device.Config, internalConfigPath string) ([]router.Route, error) {
	base, routes, err := generateRoutesFromConfig(config, internalConfigPath)
	if err != nil || len(routes) == 0 {
		return []router.Route{}, err
	}

	routes = append(routes, router.Route{
		Path:    "/meross",
		Handler: base.Handler,
	})

	routes = append(routes, router.Route{
		Path:    "/meross/",
		Handler: base.Handler,
	})

	return routes, nil
}

func toJsonNumber(value any) json.Number {
	return json.Number(fmt.Sprintf("%d", value))
}

func generateRoutesFromConfig(config *device.Config, internalConfigPath string) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	if internalConfigPath == "" {
		internalConfigPath = "./internal/device/meross/device.yaml"
	}

	internalConfigFile, err := os.ReadFile(internalConfigPath)
	if err != nil {
		return nil, []router.Route{}, err
	}

	if err := yaml.Unmarshal(internalConfigFile, &base); err != nil {
		return nil, []router.Route{}, err
	}
	if len(base.Endpoints) == 0 || base.BaseTemplate == "" {
		return nil, []router.Route{}, fmt.Errorf("Unable to load internalConfigPath \"%s\"", internalConfigPath)
	}

	for _, d := range config.Devices {
		if d.Type != "meross" {
			continue
		}
		meross := meross{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			return nil, []router.Route{}, err
		}

		if err := yaml.Unmarshal(yamlConfig, &meross); err != nil {
			return nil, []router.Route{}, err
		}

		if meross.Name == "" || meross.Host == "" || meross.DeviceType == "" {
			return nil, []router.Route{}, fmt.Errorf("Unable to load device due to missing parameters")
		}

		routes = append(routes, router.Route{
			Path:    "/meross/" + meross.Name,
			Handler: meross.Handler,
		})

		base.Devices = append(base.Devices, &meross)
	}

	return &base, routes, nil
}

func (m *meross) getCodes() []string {
	var codes []string
	for _, e := range m.Base.Endpoints {
		codes = append(codes, e.Code)
	}
	return codes
}

func (m *meross) getEndpoint(code string) *endpoint {
	for _, e := range m.Base.Endpoints {
		if code == e.Code && slices.Contains(e.SupportedDevices, m.DeviceType) {
			return e
		}
	}
	return nil
}

func (m *meross) post(method string, endpoint endpoint, value json.Number) (*status, error) {
	client := &http.Client{
		Timeout: time.Duration(m.Timeout) * time.Millisecond,
	}
	var payload string

	if value != "" {
		payload = fmt.Sprintf(endpoint.Template, value.String())
	} else {
		payload = endpoint.Template
	}

	jsonPayload := []byte(fmt.Sprintf(m.Base.BaseTemplate, method, endpoint.Namespace, payload))

	req, err := http.NewRequest("POST", "http://"+m.Host+"/config", bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, err
	}

	if method == "SET" {
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	rawResponse := rawStatus{}

	if err := json.Unmarshal(body, &rawResponse); err != nil {
		return nil, err
	}

	response := status{
		Onoff:       rawResponse.Payload.All.Digest.Togglex[0].Onoff,
		RGB:         rawResponse.Payload.All.Digest.Light.RGB,
		Temperature: rawResponse.Payload.All.Digest.Light.Temperature,
		Luminance:   rawResponse.Payload.All.Digest.Light.Luminance,
	}

	return &response, err
}

func (m *meross) Handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var status *status
	var err error

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", m.getCodes())
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

	endpoint := m.getEndpoint(request.Code)
	if endpoint == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}

	if request.Value != "" && endpoint.MaxValue != 0 {
		valueInt64, err := request.Value.Int64()
		if err != nil || valueInt64 > endpoint.MaxValue || valueInt64 < endpoint.MinValue || valueInt64 < 0 {
			errorMessage := fmt.Sprintf("Invalid Parameter: value (Min: %d, Max: %d)", endpoint.MinValue, endpoint.MaxValue)
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, errorMessage, nil)
			return
		}

	}

	switch endpoint.Code {
	case "status":
		status, err = m.post("GET", *m.getEndpoint("status"), "")
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", status)
		return
	case "toggle":
		if request.Value == "" {
			status, err = m.post("GET", *m.getEndpoint("status"), "")
			if err != nil {
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
				return
			}

			request.Value = toJsonNumber(1 - status.Onoff)
		}

		_, err = m.post("SET", *endpoint, request.Value)
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

	case "fade":
		_, err = m.post("SET", *m.getEndpoint("toggle"), toJsonNumber(0))
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}
		_, err = m.post("SET", *endpoint, toJsonNumber(-1))
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

	default:
		if request.Value == "" {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: value", nil)
			return
		}
		_, err = m.post("SET", *endpoint, request.Value)
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
	return
}

func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

func (b *base) getDevice(name string) *meross {
	for _, d := range b.Devices {
		if d.Name == name {
			return d
		}
	}
	return nil
}

func (b *base) multiPost(devices []*meross, method string, endpoint string, value json.Number) chan *namedStatus {
	wg := sync.WaitGroup{}
	responses := make(chan *namedStatus, len(devices))

	for _, m := range devices {
		wg.Add(1)
		go func(m *meross, method string, endpoint string, value json.Number) {
			defer wg.Done()
			response := namedStatus{
				Name:   m.Name,
				Status: nil,
			}

			status, err := m.post(method, *m.getEndpoint(endpoint), value)
			if err != nil {
				responses <- &response
				return
			}
			if status == nil {
				response.Status = "OK"
			} else {
				response.Status = status
			}
			responses <- &response
		}(m, method, endpoint, value)
	}

	go func() {
		wg.Wait()
		close(responses)
	}()

	return responses
}

func (b *base) Handler(w http.ResponseWriter, r *http.Request) {
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
	}

	hosts := strings.Split(strings.ReplaceAll(request.Hosts, " ", ""), ",")

	var devices []*meross
	var endpoint *endpoint
	for _, h := range hosts {
		m := b.getDevice(h)
		if m == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter: host (Device '%s' does not exist)", h), nil)
			return
		}

		endpoint = m.getEndpoint(request.Code)
		if endpoint == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter for device '%s': code", m.Name), nil)
			return
		}

		devices = append(devices, m)
	}

	if request.Value != "" && endpoint.MaxValue != 0 {
		valueInt64, err := request.Value.Int64()
		if err != nil || valueInt64 > endpoint.MaxValue || valueInt64 < endpoint.MinValue || valueInt64 < 0 {
			errorMessage := fmt.Sprintf("Invalid Parameter: value (Min: %d, Max: %d)", endpoint.MinValue, endpoint.MaxValue)
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, errorMessage, nil)
			return
		}

	}

	switch endpoint.Code {
	case "status":
		responses := b.multiPost(devices, "GET", "status", "")

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

		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", responseStruct)
	case "toggle":
		valueTally := int64(0)

		if request.Value == "" {
			request.Value = toJsonNumber(0)

			responses := b.multiPost(devices, "GET", "status", "")
			devices = nil

			for r := range responses {
				if r.Status == nil {
					continue
				}
				// Capture non-errored devices
				devices = append(devices, b.getDevice(r.Name))

				var status *status
				yamlConfig, err := yaml.Marshal(r.Status)
				if err != nil {
					httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
					return
				}

				if err := yaml.Unmarshal(yamlConfig, &status); err != nil {
					httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
					return
				}

				valueTally += status.Onoff
			}

			// Each device votes for next state, if most devices are on, all devices will be toggled off and vice versa
			if len(devices) == 0 {
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
				return
			} else if valueTally <= int64(len(devices))/2 {
				request.Value = toJsonNumber(1)
			}
		}

		responses := b.multiPost(devices, "SET", "toggle", request.Value)

		devices = nil
		for r := range responses {
			if r.Status == nil {
				continue
			}
			devices = append(devices, b.getDevice(r.Name))
		}

		if len(devices) == 0 {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		} else {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
		}
	case "fade":
		responses := b.multiPost(devices, "SET", "toggle", toJsonNumber(0))

		devices = nil
		for r := range responses {
			if r.Status == nil {
				continue
			}
			devices = append(devices, b.getDevice(r.Name))
		}

		if len(devices) == 0 {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		responses = b.multiPost(devices, "SET", "fade", toJsonNumber(-1))

		devices = nil
		for r := range responses {
			if r.Status == nil {
				continue
			}
			devices = append(devices, b.getDevice(r.Name))
		}

		if len(devices) == 0 {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		} else {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
		}

	default:
		if request.Value == "" {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: value", nil)
			return
		}

		responses := b.multiPost(devices, "SET", request.Code, request.Value)

		devices = nil
		for r := range responses {
			if r.Status == nil {
				continue
			}
			devices = append(devices, b.getDevice(r.Name))
		}

		if len(devices) == 0 {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		} else {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
		}
	}
}

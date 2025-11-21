// Package meross provides an abstraction for making HTTP calls to control Meross branded smart bulbs and sockets.
package meross_radiator

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	"github.com/kennedn/restate-go/internal/device/meross_radiator/radiator"
	router "github.com/kennedn/restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"gopkg.in/yaml.v3"
)

type meross struct {
	Name       string
	Ids        []string
	Host       string
	DeviceType string
	Timeout    uint
	Key        string
	Base       base
	Endpoints  map[string]*radiator.Endpoint
}

type base struct {
	BaseTemplate string
	Devices      []*meross
}

type deviceDefinition struct {
	BaseTemplate string
	Endpoints    map[string]*radiator.Endpoint
}

type Device struct{}

// Routes generates routes for Meross device control based on a provided configuration.
func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config, "")
	return routes, err
}

func getDeviceDefinition(definitions map[string]radiator.DeviceDefinition, deviceType string) (*deviceDefinition, bool) {
	definition, ok := definitions[deviceType]
	if !ok {
		return nil, false
	}

	return &deviceDefinition{
		BaseTemplate: definition.BaseTemplate,
		Endpoints:    definition.Endpoints,
	}, true
}

// generateRoutesFromConfig generates routes and base configuration from a provided configuration and internal config file.
func routes(config *config.Config, internalConfigPath string) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	definitions := radiator.Definitions()
	ids := map[string]meross{}
	for _, d := range config.Devices {
		meross := meross{}

		if d.Type != "meross_radiator" {
			continue
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &meross); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		definition, ok := getDeviceDefinition(definitions, meross.DeviceType)
		if !ok {
			logging.Log(logging.Info, "Unsupported device type \"%s\"", meross.DeviceType)
			continue
		}

		if base.BaseTemplate == "" {
			base.BaseTemplate = definition.BaseTemplate
		}

		meross.Base = base
		meross.Base.BaseTemplate = definition.BaseTemplate
		meross.Endpoints = definition.Endpoints

		if meross.Name == "" || meross.Host == "" || meross.DeviceType == "" || len(meross.Ids) == 0 {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + meross.Name,
			Handler: meross.handler,
		})

		base.Devices = append(base.Devices, &meross)

		// Add ids and assosiated meross device to a map
		for _, id := range meross.Ids {
			if _, ok := ids[id]; ok {
				continue
			}
			ids[id] = meross
		}

		logging.Log(logging.Info, "Found device \"%s\"", meross.Name)

	}

	// Iterate over collected ids to create pseudo devices that contain a single id
	for id, meross := range ids {
		m := meross
		m.Name = id
		m.Ids = []string{id}

		routes = append(routes, router.Route{
			Path:    "/" + m.Name,
			Handler: m.handler,
		})

		base.Devices = append(base.Devices, &m)

		logging.Log(logging.Info, "Found device \"%s\"", m.Name)
	}

	if len(routes) == 0 {
		return nil, []router.Route{}, errors.New("no routes found in config")
	} else if len(routes) == 1 {
		return &base, routes, nil
	}

	for i, r := range routes {
		routes[i].Path = "/radiator" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/radiator",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/radiator/",
		Handler: base.handler,
	})
	return &base, routes, nil
}

// getCodes returns a list of control codes for a Meross device.
func (m *meross) getCodes() []string {
	var codes []string
	for code := range m.Endpoints {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// getEndpoint retrieves an endpoint configuration by its code.
func (m *meross) getEndpoint(code string) *radiator.Endpoint {
	return m.Endpoints[code]
}

func randomHex(n int) string {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return hex.EncodeToString(bytes)
}

func md5SumString(s string) string {
	// Calculate the MD5 hash
	hasher := md5.New()
	hasher.Write([]byte(s))
	hashBytes := hasher.Sum(nil)

	// Convert the hash bytes to a hexadecimal string
	return hex.EncodeToString(hashBytes)

}

// post constructs and sends a POST request to a Meross device and will return a flattened status when the method is equal to GET.
func (b *base) post(host string, method string, namespace string, payload string, key string, timeout uint) (*radiator.RawStatus, error) {
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Millisecond,
	}
	// Newer firmware (6.2.5) requires a unique nonce for messageId
	messageId := randomHex(16)
	sign := md5SumString(fmt.Sprintf("%s%s%d", messageId, key, 0))

	payloadName := strings.Split(namespace, ".")
	wrappedPayload := fmt.Sprintf("{\"%s\":[%s]}", payloadName[len(payloadName)-1], payload)
	jsonPayload := []byte(fmt.Sprintf(b.BaseTemplate, messageId, method, namespace, sign, wrappedPayload))

	req, err := http.NewRequest("POST", "http://"+host+"/config", bytes.NewReader(jsonPayload))
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

	rawResponse := radiator.RawStatus{}

	if err := json.Unmarshal(body, &rawResponse); err != nil {
		return nil, err
	}

	if rawResponse.Payload.Error.Code != 0 {
		return nil, errors.New(rawResponse.Payload.Error.Detail)
	}

	return &rawResponse, nil

}

// post constructs and sends a POST request to a Meross device and will return a flattened status when the method is equal to GET.
func (m *meross) post(method string, namespace string, payload string) (*radiator.RawStatus, error) {
	return m.Base.post(m.Host, method, namespace, payload, m.Key, m.Timeout)
}

// Handler is the HTTP handler for Meross device control.
func (m *meross) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

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

	if request.Value != "" {
		if err := endpoint.Validate(request.Value); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, err.Error(), nil)
			return
		}
	}

	ctx := radiator.EndpointContext{
		HasValue:       request.Value != "",
		RequestedValue: request.Value,
	}

	if request.Code == "toggle" {
		ctx.FetchStatus = func() (*radiator.RawStatus, error) {
			statusEndpoint := m.getEndpoint("status")
			if statusEndpoint == nil {
				return nil, errors.New("status endpoint not configured")
			}

			payload := statusEndpoint.BuildPayload(m.Ids, radiator.ToJSONNumber(0))
			return m.post("GET", statusEndpoint.Namespace, payload)
		}
	}

	method, value, err := endpoint.Prepare(ctx)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	payload := endpoint.BuildPayload(m.Ids, value)
	rawStatus, err := m.post(method, endpoint.Namespace, payload)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	if method == "SET" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
		return
	}

	status, err := endpoint.Parse(rawStatus, nil)
	if err != nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusNotImplemented, "Not Implemented", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", status)
}

// getDeviceNames returns the names of all Meross devices in the base configuration.
func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

// getDevice retrieves a Meross device by its name.
func (b *base) getDevice(name string) *meross {
	for _, d := range b.Devices {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// getDevice retrieves a Meross device by its ID.
func (b *base) getDeviceById(id string) *meross {
	for _, d := range b.Devices {
		for _, deviceId := range d.Ids {
			if deviceId == id {
				return d
			}
		}
	}
	return nil
}

func collectIDs(devices []*meross) []string {
	var ids []string
	for _, d := range devices {
		ids = append(ids, d.Ids...)
	}
	return ids
}

// Handler is the HTTP handler for Meross device control.
func (b *base) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

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

	var devices []*meross
DUPLICATE_DEVICE:
	for _, h := range hosts {
		m := b.getDevice(h)
		if m == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter: hosts (Device '%s' does not exist)", h), nil)
			return
		}

		endpoint := m.getEndpoint(request.Code)
		if endpoint == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter for device '%s': code", m.Name), nil)
			return
		}

		for _, device := range devices {
			if m == device {
				continue DUPLICATE_DEVICE
			}
		}

		devices = append(devices, m)
	}

	if len(devices) == 0 {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	endpoint := devices[0].getEndpoint(request.Code)
	if request.Value != "" {
		if err := endpoint.Validate(request.Value); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, err.Error(), nil)
			return
		}
	}

	m := devices[0]
	ctx := radiator.EndpointContext{
		HasValue:       request.Value != "",
		RequestedValue: request.Value,
	}

	if request.Code == "toggle" {
		ctx.FetchStatus = func() (*radiator.RawStatus, error) {
			statusEndpoint := m.getEndpoint("status")
			if statusEndpoint == nil {
				return nil, errors.New("status endpoint not configured")
			}

			payload := statusEndpoint.BuildPayload(collectIDs(devices), radiator.ToJSONNumber(0))
			return b.post(m.Host, "GET", statusEndpoint.Namespace, payload, m.Key, m.Timeout)
		}
	}

	method, value, err := endpoint.Prepare(ctx)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	aggregatedPayload := endpoint.BuildPayload(collectIDs(devices), value)
	rawStatus, err := b.post(m.Host, method, endpoint.Namespace, aggregatedPayload, m.Key, m.Timeout)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	if method == "SET" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
		return
	}

	lookup := func(id string) string {
		if device := b.getDeviceById(id); device != nil {
			return device.Name
		}
		return ""
	}

	status, err := endpoint.Parse(rawStatus, lookup)
	if err != nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusNotImplemented, "Not Implemented", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", status)
}

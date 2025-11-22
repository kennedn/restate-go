// Package radiator provides control logic for Meross radiator valves.
package radiator

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
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	router "github.com/kennedn/restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"gopkg.in/yaml.v3"
)

// meross represents a Meross device configuration with name, host, device type, timeout, and base configuration.

// meross represents a Meross device configuration with name, host, device type, timeout, and base configuration.
type meross struct {
	Name       string   `yaml:"name"`
	Ids        []string `yaml:"ids"`
	Host       string   `yaml:"host"`
	DeviceType string   `yaml:"deviceType"`
	Timeout    uint     `yaml:"timeoutMs"`
	Key        string   `yaml:"key,omitempty"`
	Base       base
}

// base represents a list of Meross devices, endpoints and common configuration
type base struct {
	BaseTemplate string      `yaml:"baseTemplate"`
	Endpoints    []*endpoint `yaml:"endpoints"`
	Devices      []*meross
}

type Device struct{}

// Routes returns radiator routes for the given configuration.
func Routes(config *config.Config) ([]router.Route, error) {
	return (&Device{}).Routes(config)
}

// Routes generates routes for Meross device control based on a provided configuration.
func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config, "")
	return routes, err
}

// Routes returns the router routes for this meross device.
func (m *meross) Routes() []router.Route {
	return []router.Route{{Path: "/" + m.Name, Handler: m.handler}}
}

// toJsonNumber converts a numeric value to a JSON number.
func toJsonNumber(value any) json.Number {
	return json.Number(fmt.Sprintf("%d", value))
}

// generateRoutesFromConfig generates routes and base configuration from a provided configuration and internal config file.
func routes(config *config.Config, internalConfigPath string) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	if internalConfigPath == "" {
		// Use embedded defaultBase (no runtime file dependency)
		base = defaultBase
	} else {
		internalConfigFile, err := os.ReadFile(internalConfigPath)
		if err != nil {
			return nil, []router.Route{}, err
		}
		if err := yaml.Unmarshal(internalConfigFile, &base); err != nil {
			return nil, []router.Route{}, err
		}
	}
	if len(base.Endpoints) == 0 || base.BaseTemplate == "" {
		return nil, []router.Route{}, fmt.Errorf("unable to load internal config")
	}

	ids := map[string]meross{}
	for _, d := range config.Devices {
		meross := meross{
			Base: base,
		}

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

		if meross.Name == "" || meross.Host == "" || meross.DeviceType == "" || len(meross.Ids) == 0 {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		// delegate route creation to the device implementation
		routes = append(routes, meross.Routes()...)

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

		// delegate route creation to the device implementation
		routes = append(routes, m.Routes()...)

		base.Devices = append(base.Devices, &m)

		logging.Log(logging.Info, "Found device \"%s\"", m.Name)
	}

	if len(routes) == 0 {
		return nil, []router.Route{}, nil
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
	for _, e := range m.Base.Endpoints {
		codes = append(codes, e.Code)
	}
	return codes
}

// getEndpoint retrieves an endpoint configuration by its code.
func (m *meross) getEndpoint(code string) EndpointIF {
	for _, e := range m.Base.Endpoints {
		if code == e.Code && slices.Contains(e.SupportedDevices, m.DeviceType) {
			return getConcreteEndpoint(e)
		}
	}
	return nil
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
func (b *base) post(host string, method string, namespace string, payload string, key string, timeout uint) (*rawStatus, error) {
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

	rawResponse := rawStatus{}

	if err := json.Unmarshal(body, &rawResponse); err != nil {
		return nil, err
	}

	if rawResponse.Payload.Error.Code != 0 {
		return nil, errors.New(rawResponse.Payload.Error.Detail)
	}

	return &rawResponse, nil

}

// post constructs and sends a POST request to a Meross device and will return a flattened status when the method is equal to GET.
func (m *meross) post(method string, namespace string, payload string) (*rawStatus, error) {
	return m.Base.post(m.Host, method, namespace, payload, m.Key, m.Timeout)
}

// Build a payload for the IDs contained in a device
func (m *meross) buildPayload(template string, value json.Number) string {
	var payload strings.Builder
	for i, id := range m.Ids {
		payload.WriteString(fmt.Sprintf(template, id, string(value)))
		if i < len(m.Ids)-1 {
			payload.WriteString(",")
		}
	}
	return payload.String()
}

// Handler is the HTTP handler for Meross device control.
func (m *meross) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var endpoint EndpointIF
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

	endpoint = m.getEndpoint(request.Code)
	if endpoint == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}

	if err := endpoint.ValidateValue(request.Value); err != nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter: value (%s)", err.Error()), nil)
		return
	}

	// delegate all request processing to the endpoint implementation
	statusCode, payload, message, err := endpoint.HandleMeross(m, request)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(statusCode, message, payload)
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

// Handler is the HTTP handler for Meross device control.
func (b *base) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var endpoint EndpointIF
	var err error

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

		endpoint = m.getEndpoint(request.Code)
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

	if err := endpoint.ValidateValue(request.Value); err != nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter: value (%s)", err.Error()), nil)
		return
	}

	// delegate full handling to the endpoint implementation (no switches)
	statusCode, payloadData, message, err := endpoint.HandleBase(b, devices, request)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(statusCode, message, payloadData)
}

// Package meross provides an abstraction for making HTTP calls to control Meross branded smart bulbs and sockets.
package mts200b

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
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

//go:embed device.yaml
var defaultInternalConfig []byte

// ---------------------------
// Models / DTOs
// ---------------------------

// status is a flattened representation of the state of a Meross device, including on/off state, color, temperature, and luminance.
type status struct {
	Onoff       *int64       `json:"onoff"`
	Mode        *int64       `json:"mode,omitempty"`
	Temperature *temperature `json:"temperature,omitempty"`
}

type temperature struct {
	Current    *int64 `json:"current"`
	Target     *int64 `json:"target"`
	Heating    *bool  `json:"heating"`
	OpenWindow *bool  `json:"openWindow"`
}

// namedStatus associates a devices name with its status.
type namedStatus struct {
	Name   string `json:"name"`
	Status any    `json:"status"`
}

// rawStatus represents the raw status response from a Meross device.
type rawStatus struct {
	Payload struct {
		Mode []struct {
			Channel     int64 `json:"channel"`
			Onoff       int64 `json:"onoff"`
			Mode        int64 `json:"mode"`
			State       int64 `json:"state"`
			CurrentTemp int64 `json:"currentTemp"`
			HeatTemp    int64 `json:"heatTemp"`
			CoolTemp    int64 `json:"coolTemp"`
			EcoTemp     int64 `json:"ecoTemp"`
			ManualTemp  int64 `json:"manualTemp"`
			Warning     int64 `json:"warning"`
			TargetTemp  int64 `json:"targetTemp"`
			Min         int64 `json:"min"`
			Max         int64 `json:"max"`
			LmTime      int64 `json:"lmTime"`
		} `json:"mode,omitempty"`
		Error struct {
			Code   int64  `json:"code,omitempty"`
			Detail string `json:"detail,omitempty"`
		} `json:"error,omitempty"`
		All struct {
			Digest struct {
				Togglex []struct {
					Onoff int64 `json:"onoff"`
				} `json:"togglex,omitempty"`
				Light struct {
					RGB         int64 `json:"rgb,omitempty"`
					Temperature int64 `json:"temperature,omitempty"`
					Luminance   int64 `json:"luminance,omitempty"`
				} `json:"light,omitempty"`
				Thermostat struct {
					Mode []struct {
						Channel     int64 `json:"channel"`
						Onoff       int64 `json:"onoff"`
						Mode        int64 `json:"mode"`
						State       int64 `json:"state"`
						CurrentTemp int64 `json:"currentTemp"`
						HeatTemp    int64 `json:"heatTemp"`
						CoolTemp    int64 `json:"coolTemp"`
						EcoTemp     int64 `json:"ecoTemp"`
						ManualTemp  int64 `json:"manualTemp"`
						Warning     int64 `json:"warning"`
						TargetTemp  int64 `json:"targetTemp"`
						Min         int64 `json:"min"`
						Max         int64 `json:"max"`
						LmTime      int64 `json:"lmTime"`
					} `json:"mode,omitempty"`
					SummerMode []struct {
						Channel int64 `json:"channel"`
						Mode    int64 `json:"mode"`
					} `json:"summerMode,omitempty"`
					WindowOpened []struct {
						Channel int64 `json:"channel"`
						Status  int64 `json:"status"`
						Detect  int64 `json:"detect"`
						LmTime  int64 `json:"lmTime"`
					} `json:"windowOpened,omitempty"`
				} `json:"thermostat,omitempty"`
			} `json:"digest"`
		} `json:"all"`
	} `json:"payload"`
}

// Handler defines per-endpoint behavior for single-device and multi-device requests.
// Handlers receive the raw *http.Request so they can decode bespoke request shapes and validate independently.
type Handler interface {
	HandleSingle(m *meross, r *http.Request) (any, error)
	HandleMulti(b *base, devices []*meross, r *http.Request) (any, error)
}

// endpoint describes a Meross device control endpoint with code, supported devices, and other properties.
// MinValue/MaxValue are no longer part of the manifest; validation is done inside handlers.
type endpoint struct {
	Code             string   `yaml:"code"`
	SupportedDevices []string `yaml:"supportedDevices"`
	Namespace        string   `yaml:"namespace"`
	Template         string   `yaml:"template"`

	Handler Handler `yaml:"-"`
}

// meross represents a Meross device configuration with name, host, device type, timeout, and base configuration.
type meross struct {
	Name       string `yaml:"name"`
	Host       string `yaml:"host"`
	DeviceType string `yaml:"deviceType"`
	Timeout    uint   `yaml:"timeoutMs"`
	Key        string `yaml:"key,omitempty"`
	Base       base
}

// base represents a list of Meross devices, endpoints and common configuration
type base struct {
	BaseTemplate string      `yaml:"baseTemplate"`
	Endpoints    []*endpoint `yaml:"endpoints"`
	Devices      []*meross
}

type Device struct{}

// Routes generates routes for Meross device control based on a provided configuration.
func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config, nil)
	return routes, err
}

// ---------------------------
// Helpers
// ---------------------------

// toJsonNumber converts a numeric value to a JSON number.
func toJsonNumber(value any) json.Number {
	return json.Number(fmt.Sprintf("%d", value))
}

// decodeRequest decodes request payload into out. Handlers may call this with bespoke structs.
func decodeRequest(r *http.Request, out any) error {
	if r.Header.Get("Content-Type") == "application/json" {
		return json.NewDecoder(r.Body).Decode(out)
	}
	return schema.NewDecoder().Decode(out, r.URL.Query())
}

// generateRoutesFromConfig generates routes and base configuration from a provided configuration and internal config file.
func routes(config *config.Config, internalConfigOverride *[]byte) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}
	internalConfig := defaultInternalConfig
	if internalConfigOverride != nil {
		internalConfig = *internalConfigOverride
	}

	if err := yaml.Unmarshal(internalConfig, &base); err != nil {
		return nil, []router.Route{}, err
	}
	if len(base.Endpoints) == 0 || base.BaseTemplate == "" {
		return nil, []router.Route{}, fmt.Errorf("unable to load internalConfig")
	}

	// Bind endpoint handlers based on Code.
	base.wireHandlers()

	for _, d := range config.Devices {
		if d.Type != "meross" {
			continue
		}
		meross := meross{
			Base: base,
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

		if meross.DeviceType != "thermostat" {
			continue
		}

		if meross.Name == "" || meross.Host == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + meross.Name,
			Handler: meross.handler,
		})

		base.Devices = append(base.Devices, &meross)

		logging.Log(logging.Info, "Found device \"%s\"", meross.Name)
	}

	if len(routes) == 0 {
		return nil, []router.Route{}, errors.New("no routes found in config")
	} else if len(routes) == 1 {
		return &base, routes, nil
	}

	for i, r := range routes {
		routes[i].Path = "/meross" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/meross",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/meross/",
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
func (m *meross) getEndpoint(code string) *endpoint {
	for _, e := range m.Base.Endpoints {
		if code == e.Code && slices.Contains(e.SupportedDevices, m.DeviceType) {
			return e
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
	hasher := md5.New()
	hasher.Write([]byte(s))
	hashBytes := hasher.Sum(nil)
	return hex.EncodeToString(hashBytes)
}

// post constructs and sends a POST request to a Meross device and will return a flattened status when the method is equal to GET.
func (m *meross) post(method string, endpoint endpoint, value json.Number) (*rawStatus, error) {
	client := &http.Client{
		Timeout: time.Duration(m.Timeout) * time.Millisecond,
	}
	var payload string

	if value != "" {
		payload = fmt.Sprintf(endpoint.Template, value.String())
	} else {
		payload = endpoint.Template
	}

	// Newer firmware (6.2.5) requires a unique nonce for messageId
	messageId := randomHex(16)
	sign := md5SumString(fmt.Sprintf("%s%s%d", messageId, m.Key, 0))

	jsonPayload := []byte(fmt.Sprintf(m.Base.BaseTemplate, messageId, method, endpoint.Namespace, sign, payload))

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

	if rawResponse.Payload.Error.Code != 0 {
		return nil, errors.New(rawResponse.Payload.Error.Detail)
	}

	return &rawResponse, err
}

// ---------------------------
// Single-device HTTP handler
// ---------------------------

// handler is the HTTP handler for a single Meross device.
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

	// Buffer request body so we can decode twice (once for routing, once inside handler).
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Decode only to route (device.Request no longer contains Value).
	baseReq := device.Request{}
	if err := decodeRequest(r, &baseReq); err != nil {
		msg := "Malformed Or Empty JSON Body"
		if r.Header.Get("Content-Type") != "application/json" {
			msg = "Malformed or empty query string"
		}
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, msg, nil)
		return
	}

	endpoint := m.getEndpoint(baseReq.Code)
	if endpoint == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}

	if endpoint.Handler == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "No handler bound", nil)
		return
	}

	// Restore body for bespoke handler decode.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	result, err := endpoint.Handler.HandleSingle(m, r)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, err.Error(), nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", result)
}

// ---------------------------
// Base / multi-device logic
// ---------------------------

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

// multiPost performs multiple POST requests to control multiple Meross devices in parallel and returns their statuses.
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

			st, err := m.post(method, *m.getEndpoint(endpoint), value)
			if err != nil {
				responses <- &response
				return
			}
			if st == nil {
				response.Status = "OK"
			} else {
				response.Status = st
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

// handler is the HTTP handler for handling requests to control multiple Meross devices.
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

	// Buffer for dual decode.
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Decode only to route (device.Request no longer contains Value).
	baseReq := device.Request{}
	if err := decodeRequest(r, &baseReq); err != nil {
		msg := "Malformed Or Empty JSON Body"
		if r.Header.Get("Content-Type") != "application/json" {
			msg = "Malformed or empty query string"
		}
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, msg, nil)
		return
	}

	if baseReq.Hosts == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: hosts", nil)
		return
	}

	hosts := strings.Split(strings.ReplaceAll(baseReq.Hosts, " ", ""), ",")

	var devices []*meross
	var endpoint *endpoint
DUPLICATE_DEVICE:
	for _, h := range hosts {
		m := b.getDevice(h)
		if m == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter: hosts (Device '%s' does not exist)", h), nil)
			return
		}

		endpoint = m.getEndpoint(baseReq.Code)
		if endpoint == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, fmt.Sprintf("Invalid Parameter for device '%s': code", m.Name), nil)
			return
		}

		for _, d := range devices {
			if m == d {
				continue DUPLICATE_DEVICE
			}
		}

		devices = append(devices, m)
	}

	if endpoint.Handler == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "No handler bound", nil)
		return
	}

	// Restore body for bespoke handler decode.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	result, err := endpoint.Handler.HandleMulti(b, devices, r)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", result)
}

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

// status is a cut down rpresentation of the state of a Meross device, must be pointers to distinguish between unset and 0 value with omitempty
type statusGet struct {
	Id          *string      `json:"id"`
	Onoff       *int64       `json:"onoff,omitempty"`
	Mode        *int64       `json:"mode,omitempty"`
	Online      *int64       `json:"online,omitempty"`
	Temperature *temperature `json:"temperature,omitempty"`
}

type singleGet struct {
	Id    *string `json:"id"`
	Value *int64  `json:"value,omitempty"`
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
		Error struct {
			Code   int64  `json:"code,omitempty"`
			Detail string `json:"detail,omitempty"`
		} `json:"error,omitempty"`
		All []struct {
			ID            string `json:"id"`
			ScheduleBMode int64  `json:"scheduleBMode"`
			Online        struct {
				Status         int64 `json:"status"`
				LastActiveTime int64 `json:"lastActiveTime"`
			} `json:"online"`
			Togglex struct {
				Onoff int64 `json:"onoff"`
			} `json:"togglex"`
			TimeSync struct {
				State int64 `json:"state"`
			} `json:"timeSync"`
			Mode struct {
				State int64 `json:"state"`
			} `json:"mode"`
			Temperature struct {
				Room       int64 `json:"room"`
				CurrentSet int64 `json:"currentSet"`
				Heating    int64 `json:"heating"`
				OpenWindow int64 `json:"openWindow"`
			} `json:"temperature"`
		} `json:"all"`
		Battery []struct {
			ID    string `json:"id"`
			Value int64  `json:"value"`
		} `json:"battery"`
		Mode []struct {
			ID    string `json:"id"`
			State int64  `json:"state"`
		} `json:"mode"`
		Adjust []struct {
			ID          string `json:"id"`
			Temperature int64  `json:"temperature"`
		} `json:"adjust"`
	} `json:"payload"`
}

// endpoint describes a Meross device control endpoint with code, supported devices, and other properties.
type endpoint struct {
	Code             string   `yaml:"code"`
	SupportedDevices []string `yaml:"supportedDevices"`
	MinValue         int64    `yaml:"minValue,omitempty"`
	MaxValue         int64    `yaml:"maxValue,omitempty"`
	Namespace        string   `yaml:"namespace"`
	Template         string   `yaml:"template"`
}

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

// Routes generates routes for Meross device control based on a provided configuration.
func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config, "")
	return routes, err
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
		internalConfigPath = "./internal/device/meross_radiator/device.yaml"
	}

	internalConfigFile, err := os.ReadFile(internalConfigPath)
	if err != nil {
		return nil, []router.Route{}, err
	}

	if err := yaml.Unmarshal(internalConfigFile, &base); err != nil {
		return nil, []router.Route{}, err
	}
	if len(base.Endpoints) == 0 || base.BaseTemplate == "" {
		return nil, []router.Route{}, fmt.Errorf("unable to load internalConfigPath \"%s\"", internalConfigPath)
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
	var rawStatus *rawStatus
	var payload string
	var status []any
	var endpoint *endpoint
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

	if request.Value != "" && endpoint.MaxValue != 0 {
		valueInt64, err := request.Value.Int64()
		if err != nil || valueInt64 > endpoint.MaxValue || valueInt64 < endpoint.MinValue {
			errorMessage := fmt.Sprintf("Invalid Parameter: value (Min: %d, Max: %d)", endpoint.MinValue, endpoint.MaxValue)
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, errorMessage, nil)
			return
		}

	}

	switch endpoint.Code {
	case "toggle":
		if request.Value == "" {
			endpoint = m.getEndpoint("status")
			payload = m.buildPayload(endpoint.Template, toJsonNumber(0))
			rawStatus, err = m.post("GET", endpoint.Namespace, payload)
			if err != nil {
				logging.Log(logging.Error, err.Error())
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
				return
			}

			request.Value = toJsonNumber(1 - rawStatus.Payload.All[0].Togglex.Onoff)
		}

		endpoint = m.getEndpoint("toggle")
		payload = m.buildPayload(endpoint.Template, request.Value)
		_, err = m.post("SET", endpoint.Namespace, payload)
		if err != nil {
			logging.Log(logging.Error, err.Error())
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}
	default:
		method := "SET"
		if request.Value == "" {
			method = "GET"
			// Hacky way to keep templates consistant with two placeholders
			request.Value = toJsonNumber(0)
		}
		payload = m.buildPayload(endpoint.Template, request.Value)
		rawStatus, err = m.post(method, endpoint.Namespace, payload)
		if err != nil {
			logging.Log(logging.Error, err.Error())
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		if method == "SET" {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
			return
		}

		switch endpoint.Code {
		case "status":
			deviceStates := rawStatus.Payload.All
			for i := range deviceStates {
				heating := deviceStates[i].Temperature.CurrentSet-deviceStates[i].Temperature.Room > 0
				openWindow := deviceStates[i].Temperature.OpenWindow != 0
				status = append(status, &statusGet{
					Id:     &deviceStates[i].ID,
					Onoff:  &deviceStates[i].Togglex.Onoff,
					Mode:   &deviceStates[i].Mode.State,
					Online: &deviceStates[i].Online.Status,
					Temperature: &temperature{
						Current:    &deviceStates[i].Temperature.Room,
						Target:     &deviceStates[i].Temperature.CurrentSet,
						Heating:    &heating,
						OpenWindow: &openWindow,
					},
				})
			}
		case "battery":
			deviceStates := rawStatus.Payload.Battery
			for i := range deviceStates {
				status = append(status, &singleGet{
					Id:    &deviceStates[i].ID,
					Value: &deviceStates[i].Value,
				})
			}
		case "mode":
			deviceStates := rawStatus.Payload.Mode
			for i := range deviceStates {
				status = append(status, &singleGet{
					Id:    &deviceStates[i].ID,
					Value: &deviceStates[i].State,
				})
			}
		case "adjust":
			deviceStates := rawStatus.Payload.Adjust
			for i := range deviceStates {
				status = append(status, &singleGet{
					Id:    &deviceStates[i].ID,
					Value: &deviceStates[i].Temperature,
				})
			}
		default:
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusNotImplemented, "Not Implemented", nil)
			return
		}

		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", status)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
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
	var rawStatus *rawStatus
	var payload strings.Builder
	var status []*namedStatus
	var endpoint *endpoint
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

	if request.Value != "" && endpoint.MaxValue != 0 {
		valueInt64, err := request.Value.Int64()
		if err != nil || valueInt64 > endpoint.MaxValue || valueInt64 < endpoint.MinValue {
			errorMessage := fmt.Sprintf("Invalid Parameter: value (Min: %d, Max: %d)", endpoint.MinValue, endpoint.MaxValue)
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, errorMessage, nil)
			return
		}

	}

	m := devices[0]

	switch endpoint.Code {
	case "toggle":
		valueTally := int64(0)
		if request.Value == "" {
			request.Value = toJsonNumber(0)
			endpoint = m.getEndpoint("status")
			// Build array of devices to send to hub as a single post
			for i, m := range devices {
				payload.WriteString(m.buildPayload(endpoint.Template, toJsonNumber(0)))
				if i < len(devices)-1 {
					payload.WriteString(",")
				}
			}
			rawStatus, err = b.post(m.Host, "GET", endpoint.Namespace, payload.String(), m.Key, m.Timeout)
			if err != nil {
				logging.Log(logging.Error, err.Error())
				httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
				return
			}

			for _, s := range rawStatus.Payload.All {
				valueTally += s.Togglex.Onoff
			}

			if valueTally <= int64(len(devices))/2 {
				request.Value = toJsonNumber(1)
			}
		}

		endpoint = devices[0].getEndpoint("toggle")
		for i, m := range devices {
			payload.WriteString(m.buildPayload(endpoint.Template, request.Value))
			if i < len(devices)-1 {
				payload.WriteString(",")
			}
		}
		_, err = b.post(m.Host, "SET", endpoint.Namespace, payload.String(), m.Key, m.Timeout)
		if err != nil {
			logging.Log(logging.Error, err.Error())
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}
	default:
		method := "SET"
		if request.Value == "" {
			method = "GET"
			// Hacky way to keep templates consistant with two placeholders
			request.Value = toJsonNumber(0)
		}
		for i, m := range devices {
			payload.WriteString(m.buildPayload(endpoint.Template, request.Value))
			if i < len(devices)-1 {
				payload.WriteString(",")
			}
		}
		rawStatus, err = b.post(m.Host, method, endpoint.Namespace, payload.String(), m.Key, m.Timeout)
		if err != nil {
			logging.Log(logging.Error, err.Error())
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		if method == "SET" {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
			return
		}

		switch endpoint.Code {
		case "status":
			deviceStates := rawStatus.Payload.All
			for i := range deviceStates {
				heating := deviceStates[i].Temperature.CurrentSet-deviceStates[i].Temperature.Room > 0
				openWindow := deviceStates[i].Temperature.OpenWindow != 0
				status = append(status, &namedStatus{
					Name: b.getDeviceById(deviceStates[i].ID).Name,
					Status: &statusGet{
						Id:     &deviceStates[i].ID,
						Onoff:  &deviceStates[i].Togglex.Onoff,
						Mode:   &deviceStates[i].Mode.State,
						Online: &deviceStates[i].Online.Status,
						Temperature: &temperature{
							Current:    &deviceStates[i].Temperature.Room,
							Target:     &deviceStates[i].Temperature.CurrentSet,
							Heating:    &heating,
							OpenWindow: &openWindow,
						},
					},
				})
			}
		case "battery":
			deviceStates := rawStatus.Payload.Battery
			for i := range deviceStates {
				status = append(status, &namedStatus{
					Name: b.getDeviceById(deviceStates[i].ID).Name,
					Status: &singleGet{
						Id:    &deviceStates[i].ID,
						Value: &deviceStates[i].Value,
					},
				})
			}
		case "mode":
			deviceStates := rawStatus.Payload.Mode
			for i := range deviceStates {
				status = append(status, &namedStatus{
					Name: b.getDeviceById(deviceStates[i].ID).Name,
					Status: singleGet{
						Id:    &deviceStates[i].ID,
						Value: &deviceStates[i].State,
					},
				})
			}
		case "adjust":
			deviceStates := rawStatus.Payload.Adjust
			for i := range deviceStates {
				status = append(status, &namedStatus{
					Name: b.getDeviceById(deviceStates[i].ID).Name,
					Status: singleGet{
						Id:    &deviceStates[i].ID,
						Value: &deviceStates[i].Temperature,
					},
				})
			}
		default:
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusNotImplemented, "Not Implemented", nil)
			return
		}

		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", status)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
}

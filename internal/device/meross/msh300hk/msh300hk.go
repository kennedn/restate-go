package msh300hk

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
	"reflect"
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

//go:embed device.yaml
var defaultInternalConfig []byte

// ---------------------------
// Models / DTOs
// ---------------------------

type StringNumber string

func (v *StringNumber) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*v = StringNumber(s)
		return nil
	}

	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*v = StringNumber(n.String())
		return nil
	}

	return fmt.Errorf("expected string or number, got %s", string(b))
}

func (v *StringNumber) UnmarshalText(b []byte) error {
	*v = StringNumber(string(b))
	return nil
}

func (v StringNumber) String() string {
	return string(v)
}

func (v StringNumber) Number() json.Number {
	return json.Number(v)
}

func (v StringNumber) Int64() (int64, error) {
	return json.Number(v).Int64()
}

func (v StringNumber) Float64() (float64, error) {
	return json.Number(v).Float64()
}

// CodeValueRequest is the generic request shape for endpoints that accept an optional value.
type CodeValueRequest struct {
	Code  string       `json:"code" schema:"code"`
	Value StringNumber `json:"value,omitempty" schema:"value"`
	Hosts string       `json:"hosts,omitempty" schema:"hosts"`
}

// statusGet is a cut down representation of the state of a Meross device.
// Must be pointers to distinguish between unset and 0 value with omitempty.
type statusGet struct {
	Id          *string      `json:"id"`
	Onoff       *int64       `json:"onoff,omitempty"`
	Mode        *int64       `json:"mode,omitempty"`
	Online      *int64       `json:"online,omitempty"`
	Temperature *temperature `json:"temperature,omitempty"`
	Schedule    *string      `json:"schedule,omitempty"`
}

type singleGet struct {
	Id    *string `json:"id"`
	Value *int64  `json:"value,omitempty"`
}

type temperature struct {
	Current    *int64  `json:"current"`
	Target     *int64  `json:"target"`
	Heating    *bool   `json:"heating"`
	OpenWindow *bool   `json:"openWindow"`
	Boost      *string `json:"boost,omitempty"`
}

// namedStatus associates a device name with its status.
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

		Schedule []map[string]any `json:"schedule,omitempty"`
	} `json:"payload"`
}

// For schedule GET responses we expect entries under payload.schedule
type rawScheduleEntry map[string]any

// Handler defines per-endpoint behavior for single-device and multi-device requests.
// Handlers receive the raw *http.Request so they can decode bespoke request shapes and validate independently.
type Handler interface {
	HandleSingle(m *meross, r *http.Request) (any, error)
	HandleMulti(b *base, devices []*meross, r *http.Request) (any, error)
}

// endpoint describes a Meross device control endpoint.
// MinValue/MaxValue are no longer in YAML; any validation is done in handlers.
type endpoint struct {
	Code             string   `yaml:"code"`
	SupportedDevices []string `yaml:"supportedDevices"`
	Namespace        string   `yaml:"namespace"`
	Template         string   `yaml:"template"`
	PayloadName      string   `yaml:"payloadName,omitempty"`

	Handler Handler `yaml:"-"`
}

// meross represents a Meross device configuration.
type meross struct {
	Name       string           `yaml:"name"`
	Ids        []string         `yaml:"ids"`
	Host       string           `yaml:"host"`
	DeviceType string           `yaml:"deviceType"`
	Timeout    uint             `yaml:"timeoutMs"`
	Key        string           `yaml:"key,omitempty"`
	Schedules  []schedulePreset `yaml:"schedules,omitempty"`
	Base       base
}

// schedulePreset represents a named schedule selection configured per radiator.
type schedulePreset struct {
	ID   string    `yaml:"-" json:"id,omitempty"`
	Name string    `yaml:"name" json:"name,omitempty"`
	Mon  [][]int64 `yaml:"mon" json:"mon,omitempty"`
	Tue  [][]int64 `yaml:"tue" json:"tue,omitempty"`
	Wed  [][]int64 `yaml:"wed" json:"wed,omitempty"`
	Thu  [][]int64 `yaml:"thu" json:"thu,omitempty"`
	Fri  [][]int64 `yaml:"fri" json:"fri,omitempty"`
	Sat  [][]int64 `yaml:"sat" json:"sat,omitempty"`
	Sun  [][]int64 `yaml:"sun" json:"sun,omitempty"`
}

// base represents a list of Meross devices, endpoints and common configuration.
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

// ---------------------------
// Loading / routing
// ---------------------------

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

	ids := map[string]meross{}
	for _, d := range config.Devices {
		meross := meross{Base: base}

		if d.Type != "meross" {
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

		if meross.DeviceType != "radiator" {
			continue
		}

		if meross.Name == "" || meross.Host == "" || len(meross.Ids) == 0 {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + meross.Name,
			Handler: meross.handler,
		})

		base.Devices = append(base.Devices, &meross)

		// Add ids and associated meross device to a map
		for _, id := range meross.Ids {
			if _, ok := ids[id]; ok {
				continue
			}
			ids[id] = meross
		}

		logging.Log(logging.Info, "Found device \"%s\"", meross.Name)
	}

	// Iterate over collected ids to create pseudo devices that contain a single id
	for id, mer := range ids {
		m := mer
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

// ---------------------------
// HTTP POST helpers
// ---------------------------

// base.post constructs and sends a POST request to a Meross hub and returns rawStatus for GET.
// If payloadName is empty, it is derived from the last segment of namespace converted to lowercase.
func (b *base) post(host string, method string, namespace string, payload string, key string, timeout uint, payloadName string) (*rawStatus, error) {
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Millisecond,
	}
	// Newer firmware (6.2.5) requires a unique nonce for messageId
	messageId := randomHex(16)
	sign := md5SumString(fmt.Sprintf("%s%s%d", messageId, key, 0))

	if payloadName == "" {
		parts := strings.Split(namespace, ".")
		if len(parts) > 0 {
			payloadName = strings.ToLower(parts[len(parts)-1])
		}
	}
	wrappedPayload := fmt.Sprintf("{\"%s\":[%s]}", payloadName, payload)
	jsonPayload := []byte(fmt.Sprintf(b.BaseTemplate, messageId, method, namespace, sign, wrappedPayload))

	req, err := http.NewRequest("POST", "http://"+host+"/config", bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

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

// meross.post calls through to base.post with optional payloadName override.
func (m *meross) post(method string, namespace string, payload string, payloadName string) (*rawStatus, error) {
	return m.Base.post(m.Host, method, namespace, payload, m.Key, m.Timeout, payloadName)
}

// buildPayload builds a payload for the IDs contained in a device using a two-placeholder template (id,value).
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

// buildIdsPayload builds a payload of id-only objects for schedule GET requests.
func (m meross) buildIdsPayload() string {
	var payload strings.Builder
	for i, id := range m.Ids {
		payload.WriteString(fmt.Sprintf("{\"id\":\"%s\"}", id))
		if i < len(m.Ids)-1 {
			payload.WriteString(",")
		}
	}
	return payload.String()
}

// matchPreset compares a decoded schedule map (without id) to a list of presets and
// returns the matching preset name or empty string if none match.
func matchPreset(copyMap map[string]any, presets []schedulePreset) string {
	bytes, _ := json.Marshal(copyMap)
	var actualMap map[string]any
	_ = json.Unmarshal(bytes, &actualMap)
	for _, preset := range presets {
		var expectedMap map[string]any
		_ = json.Unmarshal([]byte("{"+string(preset.toPayload())+"}"), &expectedMap)
		if reflect.DeepEqual(actualMap, expectedMap) {
			return preset.Name
		}
	}
	return ""
}

func (m *meross) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", m.getCodes())
		return
	}
	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	// Buffer body so main handler can route and endpoint handler can decode bespoke struct.
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Decode only to route (device.Request now only includes Code/Hosts).
	baseReq := CodeValueRequest{}
	if err := decodeRequest(r, &baseReq); err != nil {
		msg := "Malformed Or Empty JSON Body"
		if r.Header.Get("Content-Type") != "application/json" {
			msg = "Malformed or empty query string"
		}
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, msg, nil)
		return
	}

	ep := m.getEndpoint(baseReq.Code)
	if ep == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}
	if ep.Handler == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "No handler bound", nil)
		return
	}

	// Restore body for bespoke handler decode.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	result, err := ep.Handler.HandleSingle(m, r)
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

// getDeviceById retrieves a Meross device by its ID.
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

// handler is the HTTP handler for multiple radiator devices.
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

	// Buffer body for dual decode.
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Decode only to route.
	baseReq := CodeValueRequest{}
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
	var ep *endpoint
DUPLICATE_DEVICE:
	for _, h := range hosts {
		m := b.getDevice(h)
		if m == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest,
				fmt.Sprintf("Invalid Parameter: hosts (Device '%s' does not exist)", h), nil)
			return
		}

		ep = m.getEndpoint(baseReq.Code)
		if ep == nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest,
				fmt.Sprintf("Invalid Parameter for device '%s': code", m.Name), nil)
			return
		}

		for _, d := range devices {
			if m == d {
				continue DUPLICATE_DEVICE
			}
		}
		devices = append(devices, m)
	}

	if len(devices) == 0 {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}
	if ep.Handler == nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "No handler bound", nil)
		return
	}

	// Restore body for bespoke handler decode.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	result, err := ep.Handler.HandleMulti(b, devices, r)
	if err != nil {
		logging.Log(logging.Error, err.Error())
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, err.Error(), nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", result)
}

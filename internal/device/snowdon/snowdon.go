package snowdon

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	router "github.com/kennedn/restate-go/internal/router/common"

	_ "embed"

	"github.com/gorilla/schema"
	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

//go:embed device.yaml
var defaultInternalConfig []byte

type internalConfig struct {
	Commands map[string]string `yaml:"commands"`
	Status   map[string]string `yaml:"status"`
}

type rawResponse struct {
	Message string `json:"message,omitempty"`
	Data    string `json:"data,omitempty"`
}

type deviceResponse struct {
	Prefix string
	Result string
	Code   string
	Raw    string
}

type snowdon struct {
	Name     string `yaml:"name"`
	Host     string `yaml:"host"`
	Timeout  uint   `yaml:"timeoutMs"`
	Base     base
	Commands map[string]string
	Status   map[string]string
}

type statusData struct {
	OnOff string `json:"onoff"`
	Input string `json:"input"`
}

type base struct {
	Devices []*snowdon
}

type Device struct{}

func (s *snowdon) getCommandNames() []string {
	var names []string
	for name := range s.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config, nil)
	return routes, err
}

func routes(config *config.Config, internalConfigOverride *[]byte) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	internal := defaultInternalConfig
	if internalConfigOverride != nil {
		internal = *internalConfigOverride
	}

	cfg := internalConfig{}
	if err := yaml.Unmarshal(internal, &cfg); err != nil {
		return nil, []router.Route{}, err
	}

	if len(cfg.Commands) == 0 {
		return nil, []router.Route{}, errors.New("no commands present in config")
	}

	for _, d := range config.Devices {
		if d.Type != "snowdon" {
			continue
		}

		snowdon := snowdon{
			Base:     base,
			Commands: cfg.Commands,
			Status:   cfg.Status,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &snowdon); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if snowdon.Name == "" || snowdon.Host == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + snowdon.Name,
			Handler: snowdon.handler,
		})

		routes = append(routes, router.Route{
			Path:    "/" + snowdon.Name + "/",
			Handler: snowdon.handler,
		})

		base.Devices = append(base.Devices, &snowdon)

		logging.Log(logging.Info, "Found device \"%s\"", snowdon.Name)
	}

	if len(base.Devices) == 0 {
		return nil, []router.Route{}, errors.New("no devices found in config")
	} else if len(base.Devices) == 1 {
		return &base, routes, nil
	}

	for i, r := range routes {
		routes[i].Path = "/snowdon" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/snowdon",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/snowdon/",
		Handler: base.handler,
	})

	return &base, routes, nil
}

func (s *snowdon) getCommandCode(name string) string {
	return s.Commands[name]
}

func (s *snowdon) getStatusName(code string) string {
	return s.Status[code]
}

func (s *snowdon) websocketWriteWithResponse(data string) ([]byte, error) {
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+s.Host, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if len(data) != 8 {
		return nil, errors.New("constructed message did not have the expected size")
	}

	err = conn.WriteMessage(websocket.TextMessage, []byte(data))
	if err != nil {
		return nil, err
	}

	conn.SetReadDeadline(time.Now().Add(time.Duration(s.Timeout) * time.Millisecond))
	_, response, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	return response, nil
}

func parseDeviceResponse(response []byte) (*deviceResponse, error) {
	if len(response) != 8 {
		return nil, errors.New("unexpected response length")
	}

	raw := string(response)
	result := raw[4:6]

	if result != "OK" && result != "NG" {
		return nil, errors.New("unexpected response status")
	}

	return &deviceResponse{
		Prefix: raw[0:4],
		Result: result,
		Code:   raw[6:8],
		Raw:    raw,
	}, nil
}

func (s *snowdon) call(code string) (*rawResponse, int, error) {
	command := s.getCommandCode(code)
	if command == "" {
		return nil, http.StatusBadRequest, errors.New("invalid parameter: code")
	}

	response, err := s.websocketWriteWithResponse(command)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	parsed, err := parseDeviceResponse(response)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	if parsed.Result == "NG" {
		message := "Device Error"
		return nil, http.StatusBadGateway, errors.New(message)
	}

	if code == "status" {
		value := s.getStatusName(parsed.Code)
		if value == "" {
			return nil, http.StatusInternalServerError, errors.New("unknown status response")
		}
		return &rawResponse{
			Message: "OK",
			Data:    value,
		}, http.StatusOK, nil
	}

	return &rawResponse{
		Message: "OK",
	}, http.StatusOK, nil
}

func (s *snowdon) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", s.getCommandNames())
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

	response, responseCode, err := s.call(request.Code)
	if err != nil {
		switch responseCode {
		case http.StatusBadRequest:
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		default:
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		}
		return
	}

	if request.Code == "status" {
		onoff := "on"
		if response.Data == "off" {
			onoff = "off"
		}
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, response.Message, statusData{
			OnOff: onoff,
			Input: response.Data,
		})
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, response.Message, nil)
}

func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

func (b *base) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", b.getDeviceNames())
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
}

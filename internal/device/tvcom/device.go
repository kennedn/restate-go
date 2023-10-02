package tvcom

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sort"
	"time"

	device "restate-go/internal/device/common"
	router "restate-go/internal/router/common"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

type configuration struct {
	Keys map[string]string `yaml:"keys"`
	Data map[string]string `yaml:"data"`
}

// LightBulb represents a light bulb with an IP address and endpoint URL
type opcode struct {
	Tvcom    *tvcom
	Code     string
	Name     string
	Data     map[string]string
	DataKeys []string
}

type tvcom struct {
	Timeout      time.Duration
	WebSocketURL string
	Opcodes      []opcode
	OpcodeNames  []string
}

func (t *tvcom) getNames() []string {
	return t.OpcodeNames
}

func (o *opcode) getDataNames() []string {
	var names []string

	for _, code := range o.DataKeys {
		name := o.Data[code]
		names = append(names, name)
	}
	return names
}

func (o *opcode) getDataName(code string) string {
	return o.Data[code]
}

func (o *opcode) getDataCode(name string) string {
	for k, v := range o.Data {
		if v == name {
			return k
		}
	}
	return ""
}

func (o *opcode) websocketWriteWithResponse(data string) ([]byte, error) {
	conn, _, err := websocket.DefaultDialer.Dial(o.Tvcom.WebSocketURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Send data to the external device
	err = conn.WriteMessage(websocket.TextMessage, []byte(o.Code+" 00 "+data+"\r"))
	if err != nil {
		return nil, err
	}

	// Receive data from the external device
	conn.SetReadDeadline(time.Now().Add(o.Tvcom.Timeout * time.Millisecond))
	_, response, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	if len(response) == 10 && string(response[5:7]) == "OK" {
		return response, nil
	} else {
		return []byte(""), errors.New("unexpected response")
	}
}

func generateRoutesFromConfig(timeout time.Duration, websocketURL string, configPath string) (*tvcom, []router.Route, error) {
	routes := []router.Route{}
	if configPath == "" {
		configPath = "./internal/device/tvcom/device.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, []router.Route{}, err
	}

	tvcom := tvcom{Timeout: timeout, WebSocketURL: websocketURL}
	configuration := []configuration{}

	if err := yaml.Unmarshal(data, &configuration); err != nil {
		return nil, []router.Route{}, err
	}

	var opcodeNames []string

	for _, c := range configuration {
		for code, name := range c.Keys {
			// Store a sorted list of Datakeys so that ordered iteration of data can occur later
			var dataKeys []string
			for k := range c.Data {
				dataKeys = append(dataKeys, k)
			}
			sort.Strings(dataKeys)

			opcodeNames = append(opcodeNames, name)

			op := opcode{
				Tvcom:    &tvcom,
				Code:     code,
				Name:     name,
				Data:     c.Data,
				DataKeys: dataKeys,
			}
			tvcom.Opcodes = append(tvcom.Opcodes, op)
			routes = append(routes, router.Route{
				Path:    "/tvcom/" + op.Name,
				Handler: op.Handler,
			})
		}
	}

	sort.Strings(opcodeNames)
	tvcom.OpcodeNames = opcodeNames

	return &tvcom, routes, nil
}

func Routes(timeout time.Duration, websocketURL string, configPath string) ([]router.Route, error) {
	tvcom, routes, err := generateRoutesFromConfig(timeout, websocketURL, configPath)
	if err != nil {
		return []router.Route{}, err
	}

	if len(routes) == 0 {
		return nil, errors.New("No routes could be retrieved from config at path " + configPath)
	}

	routes = append(routes, router.Route{
		Path:    "/tvcom/",
		Handler: tvcom.Handler,
	})

	routes = append(routes, router.Route{
		Path:    "/tvcom",
		Handler: tvcom.Handler,
	})

	return routes, nil
}

func (t *tvcom) Handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method != http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", t.getNames())
}

func (o *opcode) Handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var err error
	var httpCode int

	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", o.getDataNames())
		return
	}

	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	var dataName string
	if r.Header.Get("Content-Type") == "application/json" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed Or Empty JSON Body", nil)
			return
		}

		request := device.Request{}

		if err := json.Unmarshal(body, &request); err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Malformed Or Empty JSON Body", nil)
			return
		}

		dataName = request.Code
	} else {
		dataName = r.URL.Query().Get("code")
	}

	data := o.getDataCode(dataName)
	if data == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Variable", nil)
		return
	}

	response, err := o.websocketWriteWithResponse(data)
	if err != nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	responseValue := o.getDataName(string(response[7:9]))
	if responseValue == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", responseValue)
}

package tvcom

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	router "github.com/kennedn/restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

type configuration struct {
	Keys map[string]string `yaml:"keys"`
	Data map[string]string `yaml:"data"`
}

type opcode struct {
	Tvcom    *tvcom
	Code     string
	Name     string
	Data     map[string]string
	DataKeys []string
}

type tvcom struct {
	Name        string `yaml:"name"`
	Timeout     uint   `yaml:"timeoutMs"`
	Host        string `yaml:"host"`
	Base        base
	Opcodes     []opcode
	OpcodeNames []string
}

type base struct {
	Devices []*tvcom
}

type Device struct{}

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
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+o.Tvcom.Host, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	message := []byte(o.Code + " 00 " + data + "\r")
	if len(message) != 9 {
		return nil, errors.New("constructed message did not have the expected size")
	}

	err = conn.WriteMessage(websocket.TextMessage, message)
	if err != nil {
		return nil, err
	}

	// Receive data from the external device
	conn.SetReadDeadline(time.Now().Add(time.Duration(o.Tvcom.Timeout) * time.Millisecond))
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

func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config, "")
	return routes, err
}

func routes(config *config.Config, internalConfigPath string) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	if internalConfigPath == "" {
		internalConfigPath = "./internal/device/tvcom/device.yaml"
	}

	internalConfigFile, err := os.ReadFile(internalConfigPath)
	if err != nil {
		return nil, []router.Route{}, err
	}

	configuration := []configuration{}

	if err := yaml.Unmarshal(internalConfigFile, &configuration); err != nil {
		return nil, []router.Route{}, err
	}

	var opcodeNames []string
	var opCodes []opcode
	for _, c := range configuration {

		// Store a sorted list of Datakeys so that ordered iteration of data can occur later
		var dataKeys []string
		for k := range c.Data {
			dataKeys = append(dataKeys, k)
		}
		sort.Strings(dataKeys)

		for code, name := range c.Keys {

			opcodeNames = append(opcodeNames, name)

			op := opcode{
				Code:     code,
				Name:     name,
				Data:     c.Data,
				DataKeys: dataKeys,
			}
			opCodes = append(opCodes, op)
		}
	}
	sort.Strings(opcodeNames)

	if len(opCodes) == 0 {
		return nil, []router.Route{}, errors.New("no opcodes present in config")
	}

	for _, d := range config.Devices {
		if d.Type != "tvcom" {
			continue
		}

		tvcom := tvcom{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &tvcom); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if tvcom.Name == "" || tvcom.Host == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		for _, o := range opCodes {
			tmpOpcode := o
			tmpOpcode.Tvcom = &tvcom
			tvcom.Opcodes = append(tvcom.Opcodes, tmpOpcode)

			routes = append(routes, router.Route{
				Path:    "/" + tvcom.Name + "/" + tmpOpcode.Name,
				Handler: tmpOpcode.handler,
			})
		}
		tvcom.OpcodeNames = opcodeNames

		routes = append(routes, router.Route{
			Path:    "/" + tvcom.Name,
			Handler: tvcom.handler,
		})

		routes = append(routes, router.Route{
			Path:    "/" + tvcom.Name + "/",
			Handler: tvcom.handler,
		})

		base.Devices = append(base.Devices, &tvcom)

		logging.Log(logging.Info, "Found device \"%s\"", tvcom.Name)
	}

	if len(base.Devices) == 0 {
		return nil, []router.Route{}, errors.New("no devices found in config")
	} else if len(base.Devices) == 1 {
		return &base, routes, nil
	}

	for i, r := range routes {
		routes[i].Path = "/tvcom" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/tvcom",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/tvcom/",
		Handler: base.handler,
	})

	return &base, routes, nil
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

func (t *tvcom) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method != http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", t.getNames())
}

func (o *opcode) handler(w http.ResponseWriter, r *http.Request) {
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

	data := o.getDataCode(request.Code)
	if data == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: code", nil)
		return
	}

	response, err := o.websocketWriteWithResponse(data)
	if err != nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}
	if request.Code == "status" {
		responseValue := o.getDataName(string(response[7:9]))
		if responseValue == "" {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", responseValue)
		return
	}
	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
}

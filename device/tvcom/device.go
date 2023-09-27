package tvcom

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sort"
	"time"

	device "restate-go/device/common"
	router "restate-go/router/common"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

type opcodeSignature struct {
	Keys map[string]string `yaml:"keys"`
	Data map[string]string `yaml:"data"`
}

// LightBulb represents a light bulb with an IP address and endpoint URL
type opcode struct {
	Tvcom    *Tvcom
	Code     string
	Name     string
	Data     map[string]string
	DataKeys []string
}

type Tvcom struct {
	Timeout      time.Duration
	WebSocketURL string
	Opcodes      []opcode
}

func Routes(timeout time.Duration, websocketURL string) ([]router.Route, error) {
	routes := []router.Route{}
	data, err := os.ReadFile("device/tvcom/device.yaml")
	if err != nil {
		return nil, err
	}

	tvcom := Tvcom{Timeout: timeout, WebSocketURL: websocketURL}
	signatures := []opcodeSignature{}

	if err := yaml.Unmarshal(data, &signatures); err != nil {
		return nil, err
	}

	for _, s := range signatures {
		for code, name := range s.Keys {
			// Store a stored list of Datakeys so that ordered iteration of data can occur later
			var dataKeys []string
			for k := range s.Data {
				dataKeys = append(dataKeys, k)
			}
			sort.Strings(dataKeys)

			op := opcode{
				Tvcom:    &tvcom,
				Code:     code,
				Name:     name,
				Data:     s.Data,
				DataKeys: dataKeys,
			}
			tvcom.Opcodes = append(tvcom.Opcodes, op)
			routes = append(routes, router.Route{
				Path:    "/tvcom/" + op.Name,
				Handler: op.Handler,
			})
		}
	}
	return routes, nil
}

func (t *Tvcom) GetCode(name string) (string, error) {
	for _, op := range t.Opcodes {
		if op.Name == name {
			return op.Code, nil
		}
	}
	return "", errors.New("opcode not found")
}

func (t *Tvcom) GetName(code string) (string, error) {
	for _, op := range t.Opcodes {
		if op.Code == code {
			return op.Name, nil
		}
	}
	return "", errors.New("opcode not found")
}

func (t *Tvcom) GetNames() []string {
	var names []string
	for _, op := range t.Opcodes {
		names = append(names, op.Name)
	}
	return names
}

func (o *opcode) GetDataName(code string) string {
	return o.Data[code]
}

func (o *opcode) GetDataNames() []string {
	var names []string

	for _, code := range o.DataKeys {
		name := o.Data[code]
		names = append(names, name)
	}
	return names
}

func (o *opcode) GetDataCode(name string) string {
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
		return response, errors.New("unexpected response")
	}
}

func (o *opcode) Handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var err error
	var httpCode int

	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", o.GetDataNames())
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

	data := o.GetDataCode(dataName)
	if data == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Variable", nil)
		return
	}

	response, err := o.websocketWriteWithResponse(data)
	if err != nil {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	responseValue := o.GetDataName(string(response[7:9]))
	if responseValue == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", responseValue)
}

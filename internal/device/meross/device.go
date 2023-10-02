package meross

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	device "restate-go/internal/device/common"
	router "restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"gopkg.in/yaml.v3"
)

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

type status struct {
	Onoff       int64 `json:"onoff"`
	RGB         int64 `json:"rgb,omitempty"`
	Temperature int64 `json:"temperature,omitempty"`
	Luminance   int64 `json:"luminance,omitempty"`
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
	Timeout      time.Duration
	Name         string
	IpAddress    string
	DeviceType   string
	BaseTemplate string     `yaml:"baseTemplate"`
	Endpoints    []endpoint `yaml:"endpoints"`
}

func (m *meross) getCodes() []string {
	var codes []string
	for _, e := range m.Endpoints {
		codes = append(codes, e.Code)
	}
	return codes
}

func (m *meross) getEndpoint(code string) *endpoint {
	for _, e := range m.Endpoints {
		if code == e.Code && slices.Contains(e.SupportedDevices, m.DeviceType) {
			return &e
		}
	}
	return nil
}

func Routes(timeout time.Duration, ipAddress string, name string, deviceType string) ([]router.Route, error) {
	routes := []router.Route{}
	data, err := os.ReadFile("./internal/device/meross/device.yaml")
	if err != nil {
		return nil, err
	}

	meross := meross{
		Timeout:    timeout,
		IpAddress:  ipAddress,
		DeviceType: deviceType,
		Name:       name,
	}

	if err := yaml.Unmarshal(data, &meross); err != nil {
		return nil, err
	}

	routes = append(routes, router.Route{
		Path:    "/meross/" + meross.Name,
		Handler: meross.Handler,
	})

	return routes, nil
}

func (m *meross) toJsonNumber(value any) json.Number {
	return json.Number(fmt.Sprintf("%d", value))
}

func (m *meross) post(method string, endpoint endpoint, value json.Number) (*status, error) {
	client := &http.Client{}
	var payload string

	if value != "" {
		payload = fmt.Sprintf(endpoint.Template, value.String())
	} else {
		payload = endpoint.Template
	}

	jsonPayload := []byte(fmt.Sprintf(m.BaseTemplate, method, endpoint.Namespace, payload))

	req, err := http.NewRequest("POST", "http://"+m.IpAddress+"/config", bytes.NewReader(jsonPayload))
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
		status, err = m.post("GET", *m.getEndpoint("status"), "")
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

		if request.Value == "" {
			request.Value = m.toJsonNumber(1 - status.Onoff)
		}

		_, err = m.post("SET", *endpoint, request.Value)
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

	case "fade":
		_, err = m.post("SET", *m.getEndpoint("toggle"), m.toJsonNumber(0))
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}
		_, err = m.post("SET", *endpoint, m.toJsonNumber(-1))
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		}

	default:
		if request.Value == "" {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Missing value Variable", nil)
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

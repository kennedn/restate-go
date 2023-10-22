package snowdon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"restate-go/internal/common/logging"
	device "restate-go/internal/device/common"
	router "restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

type rawResponse struct {
	Code    []string `json:"code,omitempty"`
	Onoff   string   `json:"onoff,omitempty"`
	Input   string   `json:"input,omitempty"`
	Status  string   `json:"status,omitempty"`
	Message string   `json:"message,omitempty"`
}

type snowdon struct {
	Name    string `yaml:"name"`
	Host    string `yaml:"host"`
	Timeout uint   `yaml:"timeoutMs"`
	Base    base
}

type base struct {
	Devices []*snowdon
}

func Routes(config *device.Config) ([]router.Route, error) {
	_, routes, err := routes(config)
	return routes, err
}

func routes(config *device.Config) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	for _, d := range config.Devices {
		if d.Type != "snowdon" {
			continue
		}
		snowdon := snowdon{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			return nil, []router.Route{}, err
		}

		if err := yaml.Unmarshal(yamlConfig, &snowdon); err != nil {
			return nil, []router.Route{}, err
		}

		if snowdon.Name == "" || snowdon.Host == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + snowdon.Name,
			Handler: snowdon.handler,
		})

		base.Devices = append(base.Devices, &snowdon)
	}

	if len(routes) == 0 {
		logging.Log(logging.Info, "No routes found in config")
		return nil, []router.Route{}, errors.New("No routes found in config")
	} else if len(routes) == 1 {
		logging.Log(logging.Info, "Single device detected")
		return &base, routes, nil
	}

	logging.Log(logging.Info, "Multiple devices detected")
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

func (s *snowdon) call(method string, code string) (*rawResponse, int, error) {
	client := &http.Client{
		Timeout: time.Duration(s.Timeout) * time.Millisecond,
	}

	queryUrl := fmt.Sprintf("http://%s/?code=%s", s.Host, code)

	req, err := http.NewRequest(method, queryUrl, nil)

	if err != nil {
		return nil, 0, err
	}
	// Send the request and get the response
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	rawResponse := rawResponse{}

	if err := json.Unmarshal(body, &rawResponse); err != nil {
		return nil, resp.StatusCode, err
	}

	return &rawResponse, resp.StatusCode, nil
}

func capitalise(str string) string {
	return cases.Title(language.English).String(str)
}

func (s *snowdon) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var err error

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method == http.MethodGet {
		response, responseCode, err := s.call("GET", "")
		if err != nil {
			httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
			return
		} else if responseCode != 200 {
			httpCode, jsonResponse = device.SetJSONResponse(responseCode, capitalise(response.Message), nil)
			return
		}
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", response.Code)
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

	response, responseCode, err := s.call("PUT", request.Code)
	if err != nil || responseCode == 500 {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	} else if responseCode != 200 {
		httpCode, jsonResponse = device.SetJSONResponse(responseCode, capitalise(response.Message), nil)
		return
	}
	if request.Code == "status" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", response)
	} else {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
	}
}

func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

// Handler is the HTTP handler for handling requests to control multiple snowdon devices.
func (b *base) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int

	defer func() { device.JSONResponse(w, httpCode, jsonResponse) }()

	if r.Method == http.MethodGet {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", b.getDeviceNames())
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
	return

}

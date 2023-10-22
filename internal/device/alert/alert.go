package alert

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"restate-go/internal/common/logging"
	device "restate-go/internal/device/common"
	router "restate-go/internal/router/common"

	"github.com/gorilla/schema"
	"gopkg.in/yaml.v3"
)

type Request struct {
	Message  string      `json:"message"`
	Title    string      `json:"title,omitempty"`
	Priority json.Number `json:"priority,omitempty"`
	Token    string      `json:"token,omitempty"`
	User     string      `json:"user,omitempty"`
}

type rawResponse struct {
	Status int      `json:"status"`
	Errors []string `json:"errors,omitempty"`
}

type alert struct {
	Name    string `yaml:"name"`
	Timeout uint   `yaml:"timeoutMs"`
	Token   string `yaml:"token"`
	User    string `yaml:"user"`
	Base    base
}

type base struct {
	Devices []*alert
	URL     string
}

func Routes(config *device.Config) ([]router.Route, error) {
	_, routes, err := routes(config)
	return routes, err
}

func routes(config *device.Config) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{
		URL: "https://api.pushover.net/1/messages.json",
	}

	for _, d := range config.Devices {
		if d.Type != "alert" {
			continue
		}
		alert := alert{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			return nil, []router.Route{}, err
		}

		if err := yaml.Unmarshal(yamlConfig, &alert); err != nil {
			return nil, []router.Route{}, err
		}

		if alert.Name == "" || alert.Token == "" || alert.User == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + alert.Name,
			Handler: alert.handler,
		})

		base.Devices = append(base.Devices, &alert)
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
		routes[i].Path = "/alert" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/alert",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/alert/",
		Handler: base.handler,
	})
	return &base, routes, nil
}

func (a *alert) post(request Request) (*rawResponse, int, error) {
	client := &http.Client{
		Timeout: time.Duration(a.Timeout) * time.Millisecond,
	}

	if request.Title == "" {
		request.Title = "restate"
	}

	if request.Token == "" {
		request.Token = a.Token
	}

	if request.User == "" {
		request.User = a.User
	}

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequest("POST", a.Base.URL, bytes.NewReader(requestBytes))
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")

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

func (a *alert) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var err error

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusMethodNotAllowed, "Method Not Allowed", nil)
		return
	}

	request := Request{}

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

	if request.Message == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: message", nil)
		return
	}

	response, responseCode, err := a.post(request)
	if err != nil || responseCode == 500 {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	} else if responseCode != 200 {
		var errorMessage string
		if len(response.Errors) > 0 {
			errorMessage = response.Errors[0]
		}
		httpCode, jsonResponse = device.SetJSONResponse(responseCode, errorMessage, nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", nil)
}

func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

// Handler is the HTTP handler for handling requests to control multiple alert devicea.
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

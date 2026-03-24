package bins

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	config "github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
	router "github.com/kennedn/restate-go/internal/router/common"

	_ "time/tzdata"

	"github.com/gorilla/schema"
	"gopkg.in/yaml.v3"
)

type Request struct {
	Address string `json:"address" schema:"address"`
}

type Response struct {
	Name string `json:"name"`
	Date string `json:"date"`
}

type rawLookupResponse struct {
	Integration struct {
		Transformed struct {
			SelectData []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"select_data"`
			RowsData map[string]struct {
				Round string `json:"Round"`
				Date  string `json:"Date"`
			} `json:"rows_data"`
		} `json:"transformed"`
	} `json:"integration"`
}

type bins struct {
	Name           string `yaml:"name"`
	Timeout        uint   `yaml:"timeoutMs"`
	DefaultAddress string `yaml:"defaultAddress"`
	ServiceURL     string `yaml:"serviceUrl"`
	UprnURL        string `yaml:"uprnUrl"`
	LookupURL      string `yaml:"lookupUrl"`
	Base           base
}

type base struct {
	Devices []*bins
}

type Device struct{}

// Device interface function for generating routes
func (d *Device) Routes(config *config.Config) ([]router.Route, error) {
	_, routes, err := routes(config)
	return routes, err
}

// Extract devices of type bins from config and return a list of routes
func routes(config *config.Config) (*base, []router.Route, error) {
	routes := []router.Route{}
	base := base{}

	for _, d := range config.Devices {
		if d.Type != "bins" {
			continue
		}

		bins := bins{
			Base: base,
		}

		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		if err := yaml.Unmarshal(yamlConfig, &bins); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if bins.Name == "" || bins.DefaultAddress == "" || bins.ServiceURL == "" || bins.UprnURL == "" || bins.LookupURL == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		routes = append(routes, router.Route{
			Path:    "/" + bins.Name,
			Handler: bins.handler,
		})

		base.Devices = append(base.Devices, &bins)

		logging.Log(logging.Info, "Found device \"%s\"", bins.Name)
	}

	if len(routes) == 0 {
		return nil, []router.Route{}, errors.New("no routes found in config")
	} else if len(routes) == 1 {
		return &base, routes, nil
	}

	for i, r := range routes {
		routes[i].Path = "/bins" + r.Path
	}

	routes = append(routes, router.Route{
		Path:    "/bins",
		Handler: base.handler,
	})

	routes = append(routes, router.Route{
		Path:    "/bins/",
		Handler: base.handler,
	})

	return &base, routes, nil
}

func postcodeFromAddress(address string) string {
	parts := strings.Fields(address)
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[len(parts)-2:], " ")
}

func stripNumbers(input string) string {
	var output strings.Builder
	for _, r := range input {
		if r < '0' || r > '9' {
			output.WriteRune(r)
		}
	}
	return strings.TrimSpace(output.String())
}

func toISOString(input string) string {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		return ""
	}

	t, err := time.ParseInLocation("02/01/2006 15:04:05", input, loc)
	if err != nil {
		return ""
	}

	return t.Format("2006-01-02")
}

func currentDate() string {
	return time.Now().Format("2006-01-02")
}

func (b *bins) newClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Timeout: time.Duration(b.Timeout) * time.Millisecond,
		Jar:     jar,
	}, nil
}

func (b *bins) bootstrap(client *http.Client) error {
	req, err := http.NewRequest("GET", b.ServiceURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if _, err := io.ReadAll(resp.Body); err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return errors.New("failed to bootstrap session")
	}

	return nil
}

func (b *bins) postJSON(client *http.Client, url string, payload interface{}, response interface{}) (int, error) {
	requestBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(requestBytes))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, errors.New("upstream request failed")
	}

	if err := json.Unmarshal(body, response); err != nil {
		return resp.StatusCode, err
	}

	return resp.StatusCode, nil
}

func (b *bins) lookupUPRN(client *http.Client, address string) (string, int, error) {
	postcode := postcodeFromAddress(address)
	if postcode == "" {
		return "", http.StatusBadRequest, errors.New("invalid address")
	}

	payload := map[string]interface{}{
		"formValues": map[string]interface{}{
			"Section 1": map[string]interface{}{
				"postcode": map[string]string{
					"value": postcode,
				},
			},
		},
	}

	rawResponse := rawLookupResponse{}
	responseCode, err := b.postJSON(client, b.UprnURL, payload, &rawResponse)
	if err != nil {
		return "", responseCode, err
	}

	for _, item := range rawResponse.Integration.Transformed.SelectData {
		if item.Label == address {
			return item.Value, http.StatusOK, nil
		}
	}

	return "", http.StatusNotFound, errors.New("address not found")
}

func (b *bins) lookupBins(client *http.Client, uprn string) ([]Response, int, error) {
	payload := map[string]interface{}{
		"formValues": map[string]interface{}{
			"Section 1": map[string]interface{}{
				"uprn": map[string]string{
					"value": uprn,
				},
				"fromDate": map[string]string{
					"value": currentDate(),
				},
			},
		},
	}

	rawResponse := rawLookupResponse{}
	responseCode, err := b.postJSON(client, b.LookupURL, payload, &rawResponse)
	if err != nil {
		return nil, responseCode, err
	}

	responses := []Response{}
	for _, row := range rawResponse.Integration.Transformed.RowsData {
		responses = append(responses, Response{
			Name: stripNumbers(row.Round),
			Date: toISOString(row.Date),
		})
	}

	return responses, http.StatusOK, nil
}

// Sanitise params and proxy upstream bin lookup
func (b *bins) post(request Request) ([]Response, int, error) {
	client, err := b.newClient()
	if err != nil {
		return nil, 0, err
	}

	if err := b.bootstrap(client); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	uprn, responseCode, err := b.lookupUPRN(client, request.Address)
	if err != nil {
		return nil, responseCode, err
	}

	response, responseCode, err := b.lookupBins(client, uprn)
	if err != nil {
		return nil, responseCode, err
	}

	return response, responseCode, nil
}

// Handle bins http request
func (b *bins) handler(w http.ResponseWriter, r *http.Request) {
	var jsonResponse []byte
	var httpCode int
	var err error

	defer func() {
		device.JSONResponse(w, httpCode, jsonResponse)
	}()

	if r.Method != http.MethodPost {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", []string{"address"})
		return
	}

	request := Request{}

	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType == "application/json" {
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

	if request.Address == "" {
		request.Address = b.DefaultAddress
	}

	if request.Address == "" {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Missing Parameter: address", nil)
		return
	}

	response, responseCode, err := b.post(request)
	if responseCode == http.StatusBadRequest {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusBadRequest, "Invalid Parameter: address", nil)
		return
	} else if responseCode == http.StatusNotFound {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusNotFound, "Address Not Found", nil)
		return
	} else if err != nil || responseCode == http.StatusInternalServerError {
		httpCode, jsonResponse = device.SetJSONResponse(http.StatusInternalServerError, "Internal Server Error", nil)
		return
	} else if responseCode != http.StatusOK {
		httpCode, jsonResponse = device.SetJSONResponse(responseCode, "Bad Gateway", nil)
		return
	}

	httpCode, jsonResponse = device.SetJSONResponse(http.StatusOK, "OK", response)
}

func (b *base) getDeviceNames() []string {
	var names []string
	for _, d := range b.Devices {
		names = append(names, d.Name)
	}
	return names
}

// Handles returning list of configured devices
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

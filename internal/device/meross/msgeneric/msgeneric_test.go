package msgeneric

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

//nolint:lll // Test fixtures are long but preserved for readability.
//go:embed testdata/merossConfig/normal_config.yaml
var normalMerossConfig string

//go:embed testdata/merossConfig/unknown_deviceType.yaml
var unknownDeviceTypeMerossConfig string

//go:embed testdata/merossConfig/missing_config.yaml
var missingMerossConfig string

//go:embed testdata/merossConfig/missing_config_parameter.yaml
var missingMerossConfigParameter string

//go:embed testdata/merossConfig/single_device_config.yaml
var singleDeviceMerossConfig string

//go:embed testdata/merossConfig/empty_yaml_config.yaml
var emptyConfig string

//go:embed testdata/baseConfig/non_yaml_config.yaml
var nonYamlConfig string

//go:embed testdata/baseConfig/0_endpoints.yaml
var baseNoEndpoints string

//go:embed testdata/serverConfig/normal_responses.yaml
var normalServerConfig string

//go:embed testdata/baseConfig/empty_yaml_config.yaml
var baseEmptyConfig string

func bytesPtr(b []byte) *[]byte {
	return &b
}

func setupHTTPServer(t *testing.T, serverConfig string) *httptest.Server {
	serverConfigValues := struct {
		Get struct {
			Code int    `yaml:"code"`
			JSON string `yaml:"json"`
		} `yaml:"get"`
		Set struct {
			Code int `yaml:"code"`
		} `yaml:"set"`
	}{}

	if err := yaml.Unmarshal([]byte(serverConfig), &serverConfigValues); err != nil {
		t.Fatalf("Could not parse serverConfigPath")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}

		body := struct {
			Header struct {
				Method string `json:"method"`
			} `json:"header"`
		}{}

		if err := json.Unmarshal(rawBody, &body); err != nil {
			t.Fatalf("Could not parse request body")
		}

		switch body.Header.Method {
		case "GET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(serverConfigValues.Get.Code)
			w.Write([]byte(serverConfigValues.Get.JSON))
		case "SET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(serverConfigValues.Set.Code)
			w.Write([]byte(""))
		}

	}))

	return server
}

func TestRoutes(t *testing.T) {
	logging.SetLogLevel(logging.Error)
	testCases := []struct {
		name           string
		merossConfig   string
		internalConfig *[]byte
		routeCount     int
		expectedError  error
	}{
		{
			name:           "default_config",
			merossConfig:   normalMerossConfig,
			internalConfig: nil,
			routeCount:     5,
			expectedError:  nil,
		},
		{
			name:           "base_0_endpoints",
			merossConfig:   normalMerossConfig,
			internalConfig: bytesPtr([]byte(baseNoEndpoints)),
			routeCount:     0,
			expectedError:  errors.New(""),
		},
		{
			name:           "base_non_yaml_config",
			merossConfig:   normalMerossConfig,
			internalConfig: bytesPtr([]byte(nonYamlConfig)),
			routeCount:     0,
			expectedError:  &yaml.TypeError{},
		},
		{
			name:           "base_empty_yaml_config",
			merossConfig:   normalMerossConfig,
			internalConfig: bytesPtr([]byte(baseEmptyConfig)),
			routeCount:     0,
			expectedError:  errors.New(""),
		},
		{
			name:           "meross_empty_yaml_config",
			merossConfig:   emptyConfig,
			internalConfig: nil,
			routeCount:     0,
			expectedError:  errors.New(""),
		},
		{
			name:           "meross_missing_config",
			merossConfig:   missingMerossConfig,
			internalConfig: nil,
			routeCount:     0,
			expectedError:  errors.New(""),
		},
		{
			name:           "meross_missing_config_parameter",
			merossConfig:   missingMerossConfigParameter,
			internalConfig: nil,
			routeCount:     0,
			expectedError:  errors.New(""),
		},
		{
			name:           "meross_unknown_deviceType",
			merossConfig:   unknownDeviceTypeMerossConfig,
			internalConfig: nil,
			routeCount:     4,
			expectedError:  nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			merossConfig := config.Config{}

			if err := yaml.Unmarshal([]byte(tc.merossConfig), &merossConfig); err != nil {
				t.Fatalf("Could not read meross input")
			}
			_, r, err := routes(&merossConfig, tc.internalConfig)

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(r) != tc.routeCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.routeCount, len(r))
			}

		})
	}
}

func TestHandler(t *testing.T) {
	logging.SetLogLevel(logging.Error)
	testCases := []struct {
		name         string
		method       string
		url          string
		data         []byte
		serverConfig string
		merossConfig string
		expectedCode int
		expectedBody string
	}{
		{
			name:         "status_no_error",
			method:       "POST",
			url:          "/meross/test1?code=status",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":{"onoff":1,"rgb":255,"temperature":1,"luminance":-1}}`,
		},
		{
			name:         "toggle_no_error",
			method:       "POST",
			url:          "/meross/test2",
			data:         []byte(`{"code": "toggle"}`),
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "get_device_request",
			method:       "GET",
			url:          "/meross/test1",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["toggle","status","luminance","temperature","rgb","fade"]}`,
		},
		{
			name:         "get_base_request",
			method:       "GET",
			url:          "/meross/",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["test1","test2","test3"]}`,
		},
		{
			name:         "get_base_request_single_device",
			method:       "GET",
			url:          "/meross/",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: singleDeviceMerossConfig,
			expectedCode: 404,
			expectedBody: "404 page not found\n",
		},
		{
			name:         "unsupported_device_method",
			method:       "DELETE",
			url:          "/meross/test1",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "unsupported_base_method",
			method:       "DELETE",
			url:          "/meross/",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "malformed_json_body",
			method:       "POST",
			url:          "/meross/test1",
			data:         []byte(`not_json`),
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 400,
			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
		},
		{
			name:         "malformed_query_string",
			method:       "POST",
			url:          "/meross/test1?monkeytest",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 400,
			expectedBody: `{"message":"Malformed or empty query string"}`,
		},
		{
			name:         "unsupported_code_variable",
			method:       "POST",
			url:          "/meross/test1?code=monkey",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Parameter: code"}`,
		},
		{
			name:         "value_out_of_range",
			method:       "POST",
			url:          "/meross/test1?code=luminance&value=200",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 500,
			expectedBody: `{"message":"Internal Server Error"}`,
		},
		{
			name:         "luminance_no_error",
			method:       "POST",
			url:          "/meross/test1?code=luminance&value=50",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "fade_no_error",
			method:       "POST",
			url:          "/meross/test1?code=fade",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_status_no_error",
			method:       "POST",
			url:          "/meross/?code=status&hosts=test1,test2",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":{"devices":[{"name":"test1","status":{"onoff":1,"rgb":255,"temperature":1,"luminance":-1}},{"name":"test2","status":{"onoff":1,"rgb":255,"temperature":1,"luminance":-1}}]}}`,
		},
		{
			name:         "multi_toggle_no_error",
			method:       "POST",
			url:          "/meross?code=toggle&hosts=test1,test2",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 500,
			expectedBody: `{"message":"Internal Server Error"}`,
		},
		{
			name:         "multi_fade_no_error",
			method:       "POST",
			url:          "/meross",
			data:         []byte(`{"code":"fade", "hosts": "test1,test3"}`),
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_luminance_no_error",
			method:       "POST",
			url:          "/meross",
			data:         []byte(`{"code":"luminance", "hosts": "test1,test3","value":"10"}`),
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			merossConfig := config.Config{}

			if err := yaml.Unmarshal([]byte(tc.merossConfig), &merossConfig); err != nil {
				t.Fatalf("Could not read meross input")
			}

			base, routes, err := routes(&merossConfig, nil)

			if err != nil {
				t.Fatalf("routes returned an error: %v", err)
			}
			router := mux.NewRouter()
			for _, r := range routes {
				router.HandleFunc(r.Path, r.Handler)
			}

			server := setupHTTPServer(t, tc.serverConfig)
			for i := range base.Devices {
				base.Devices[i].Host = strings.TrimPrefix(server.URL, "http://")
			}
			defer server.Close()
			recorder := httptest.NewRecorder()

			request := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.data))
			if tc.data != nil {
				request.Header.Set("Content-Type", "application/json")
			}

			router.ServeHTTP(recorder, request)

			if recorder.Code != tc.expectedCode {
				t.Errorf("Unexpected HTTP status code. Expected: %d, Got: %d", tc.expectedCode, recorder.Code)
			}

			if recorder.Body.String() != tc.expectedBody {
				t.Errorf("Unexpected response body. Expected: %s, Got: %s", tc.expectedBody, recorder.Body.String())
			}
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("toJsonNumber", func(t *testing.T) {
		n := toJsonNumber(42)
		if n.String() != "42" {
			t.Fatalf("expected json number string of 42, got %s", n.String())
		}
	})

	t.Run("randomHexLength", func(t *testing.T) {
		v := randomHex(8)
		if len(v) != 16 {
			t.Fatalf("expected 16 hex characters, got %d", len(v))
		}
	})

	t.Run("md5SumString", func(t *testing.T) {
		expected := "098f6bcd4621d373cade4e832627b4f6"
		if got := md5SumString("test"); got != expected {
			t.Fatalf("md5 hash mismatch: expected %s, got %s", expected, got)
		}
	})

	t.Run("decodeRequestJSONAndQuery", func(t *testing.T) {
		type sample struct {
			Code  string      `json:"code" schema:"code"`
			Value json.Number `json:"value" schema:"value"`
		}

		jsonReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"code":"toggle","value":5}`))
		jsonReq.Header.Set("Content-Type", "application/json")
		body := sample{}
		if err := decodeRequest(jsonReq, &body); err != nil {
			t.Fatalf("expected json decode to succeed: %v", err)
		}
		if body.Code != "toggle" || body.Value.String() != "5" {
			t.Fatalf("unexpected json decode result: %+v", body)
		}

		queryReq := httptest.NewRequest(http.MethodGet, "/?code=toggle&value=7", nil)
		queryBody := sample{}
		if err := decodeRequest(queryReq, &queryBody); err != nil {
			t.Fatalf("expected query decode to succeed: %v", err)
		}
		if queryBody.Code != "toggle" || queryBody.Value.String() != "7" {
			t.Fatalf("unexpected query decode result: %+v", queryBody)
		}
	})
}

func TestBaseHelpers(t *testing.T) {
	endpoints := []*endpoint{
		{Code: "status", SupportedDevices: []string{"bulb"}},
		{Code: "toggle", SupportedDevices: []string{"socket"}},
	}
	b := base{Endpoints: endpoints}
	m1 := &meross{Name: "one", DeviceType: "bulb", Base: b}
	m2 := &meross{Name: "two", DeviceType: "socket", Base: b}
	b.Devices = []*meross{m1, m2}

	names := b.getDeviceNames()
	assert.ElementsMatch(t, []string{"one", "two"}, names)

	if got := b.getDevice("one"); got != m1 {
		t.Fatalf("expected to retrieve device 'one'")
	}
	if got := b.getDevice("missing"); got != nil {
		t.Fatalf("expected missing device lookup to return nil")
	}

	if ep := m1.getEndpoint("status"); ep == nil {
		t.Fatalf("expected bulb to resolve status endpoint")
	}
	if ep := m1.getEndpoint("toggle"); ep != nil {
		t.Fatalf("expected unsupported endpoint to be nil")
	}
}

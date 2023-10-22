package snowdon

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"restate-go/internal/common/logging"
	device "restate-go/internal/device/common"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/gorilla/schema"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func setupHTTPServer(t *testing.T, serverConfigPath string) *httptest.Server {
	serverConfigFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		t.Fatalf("Could not read serverConfigPath")
	}

	serverConfig := struct {
		Codes []struct {
			Code     string `yaml:"code"`
			Method   string `yaml:"method"`
			HttpCode int    `yaml:"httpCode"`
			Json     string `yaml:"json"`
		}
	}{}

	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
		t.Fatalf("Could not parse serverConfigPath")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := device.Request{}
		if err := schema.NewDecoder().Decode(&request, r.URL.Query()); err != nil {
			t.Fatalf("Could not parse query URL")
		}

		for _, s := range serverConfig.Codes {
			if request.Code == s.Code && r.Method == s.Method {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(s.HttpCode)
				w.Write([]byte(s.Json))
				return
			}
		}

		t.Fatalf("No canned response found for input")
	}))

	return server
}

func TestRoutes(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	testCases := []struct {
		name          string
		configPath    string
		routeCount    int
		expectedError error
	}{
		{
			name:          "default_config",
			configPath:    "testdata/snowdonConfig/normal_config.yaml",
			routeCount:    4,
			expectedError: nil,
		},
		{
			name:          "empty_yaml_config",
			configPath:    "testdata/snowdonConfig/empty_yaml_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config",
			configPath:    "testdata/snowdonConfig/missing_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config_parameter",
			configPath:    "testdata/snowdonConfig/missing_config_parameter.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "single_device_config",
			configPath:    "testdata/snowdonConfig/single_device_config.yaml",
			routeCount:    1,
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			merossConfigFile, err := os.ReadFile(tc.configPath)
			if err != nil {
				t.Fatalf("Could not read config file")
			}

			merossConfig := device.Config{}

			if err := yaml.Unmarshal(merossConfigFile, &merossConfig); err != nil {
				t.Fatalf("Could not read config file")
			}
			r, err := Routes(&merossConfig)

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(r) != tc.routeCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.routeCount, len(r))
			}

		})
	}
}

func TestHandlers(t *testing.T) {
	logging.SetLogLevel(logging.Error)
	testCases := []struct {
		name          string
		method        string
		url           string
		data          []byte
		serverConfig  string
		snowdonConfig string
		expectedCode  int
		expectedBody  string
	}{
		{
			name:          "status_no_error",
			method:        "POST",
			url:           "/snowdon/test1?code=status",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  200,
			expectedBody:  `{"message":"OK","data":{"onoff":"on","input":"aux"}}`,
		},
		{
			name:          "power_no_error",
			method:        "POST",
			url:           "/snowdon/test2",
			data:          []byte(`{"code": "power"}`),
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  200,
			expectedBody:  `{"message":"OK"}`,
		},
		{
			name:          "get_device_request",
			method:        "GET",
			url:           "/snowdon/test1",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  200,
			expectedBody:  `{"message":"OK","data":["status","power","mute","volume_up","volume_down","previous","next","play_pause","input","treble_up","treble_down","bass_up","bass_down","pair","flat","music","dialog","movie"]}`,
		},
		{
			name:          "get_base_request",
			method:        "GET",
			url:           "/snowdon/",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  200,
			expectedBody:  `{"message":"OK","data":["test1","test2"]}`,
		},
		{
			name:          "get_base_request_single_device",
			method:        "GET",
			url:           "/snowdon/",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/single_device_config.yaml",
			expectedCode:  404,
			expectedBody:  "404 page not found\n",
		},
		{
			name:          "unsupported_device_method",
			method:        "DELETE",
			url:           "/snowdon/test1",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  405,
			expectedBody:  `{"message":"Method Not Allowed"}`,
		},
		{
			name:          "unsupported_base_method",
			method:        "POST",
			url:           "/snowdon/",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  405,
			expectedBody:  `{"message":"Method Not Allowed"}`,
		},
		{
			name:          "malformed_json_body",
			method:        "POST",
			url:           "/snowdon/test1",
			data:          []byte(`not_json`),
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  400,
			expectedBody:  `{"message":"Malformed Or Empty JSON Body"}`,
		},
		{
			name:          "malformed_query_string",
			method:        "POST",
			url:           "/snowdon/test1?monkeytest",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  400,
			expectedBody:  `{"message":"Malformed or empty query string"}`,
		},
		{
			name:          "unsupported_code_variable",
			method:        "POST",
			url:           "/snowdon/test1?code=monkey",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  400,
			expectedBody:  `{"message":"Code Not Recognised"}`,
		},
		{
			name:          "snowdon_internal_500_error",
			method:        "POST",
			url:           "/snowdon/test1?code=error",
			data:          nil,
			serverConfig:  "testdata/serverConfig/normal_responses.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  500,
			expectedBody:  `{"message":"Internal Server Error"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			snowdonConfigFile, err := os.ReadFile(tc.snowdonConfig)
			if err != nil {
				t.Fatalf("Could not read snowdon input")
			}

			snowdonConfig := device.Config{}

			if err := yaml.Unmarshal(snowdonConfigFile, &snowdonConfig); err != nil {
				t.Fatalf("Could not read snowdon input")
			}

			base, routes, err := routes(&snowdonConfig)

			if err != nil {
				t.Fatalf("routes returned an error: %v", err)
			}
			router := mux.NewRouter()
			for _, r := range routes {
				router.HandleFunc(r.Path, r.Handler)
			}
			server := setupHTTPServer(t, tc.serverConfig)
			for i, _ := range base.Devices {
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
				t.Fatalf("Unexpected HTTP status code. Expected: %d, Got: %d", tc.expectedCode, recorder.Code)
			}

			if recorder.Body.String() != tc.expectedBody {
				t.Fatalf("Unexpected response body. Expected: %s, Got: %s", tc.expectedBody, recorder.Body.String())
			}
		})
	}
}

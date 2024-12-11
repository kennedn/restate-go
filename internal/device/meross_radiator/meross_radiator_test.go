package meross_radiator

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func setupHTTPServer(t *testing.T, serverConfigPath string) *httptest.Server {
	serverConfigFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		t.Fatalf("Could not read serverConfigPath")
	}

	serverConfig := struct {
		Get struct {
			Code int    `yaml:"code"`
			JSON string `yaml:"json"`
		} `yaml:"get"`
		Set struct {
			Code int `yaml:"code"`
		} `yaml:"set"`
	}{}

	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
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
			w.WriteHeader(serverConfig.Get.Code)
			w.Write([]byte(serverConfig.Get.JSON))
		case "SET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(serverConfig.Set.Code)
			w.Write([]byte(""))
		}

	}))

	return server
}

func TestRoutes(t *testing.T) {
	logging.SetLogLevel(logging.Error)
	testCases := []struct {
		name               string
		configPath         string
		internalConfigPath string
		routeCount         int
		expectedError      error
	}{
		{
			name:               "default_config",
			configPath:         "testdata/merossConfig/normal_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         5,
			expectedError:      nil,
		},
		{
			name:               "base_bad_path",
			configPath:         "testdata/merossConfig/normal_config.yaml",
			internalConfigPath: "non/existant/file",
			routeCount:         0,
			expectedError:      &fs.PathError{},
		},
		{
			name:               "base_0_endpoints",
			configPath:         "testdata/merossConfig/normal_config.yaml",
			internalConfigPath: "testdata/baseConfig/0_endpoints.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "base_non_yaml_config",
			configPath:         "testdata/merossConfig/normal_config.yaml",
			internalConfigPath: "testdata/baseConfig/non_yaml_config.yaml",
			routeCount:         0,
			expectedError:      &yaml.TypeError{},
		},
		{
			name:               "base_empty_yaml_config",
			configPath:         "testdata/merossConfig/normal_config.yaml",
			internalConfigPath: "testdata/baseConfig/empty_yaml_config.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "meross_empty_yaml_config",
			configPath:         "testdata/merossConfig/empty_yaml_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "meross_missing_config",
			configPath:         "testdata/merossConfig/missing_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "meross_missing_config_parameter",
			configPath:         "testdata/merossConfig/missing_config_parameter.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "meross_unknown_deviceType",
			configPath:         "testdata/merossConfig/unknown_deviceType.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         4,
			expectedError:      nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			merossConfigFile, err := os.ReadFile(tc.configPath)
			if err != nil {
				t.Fatalf("Could not read meross input")
			}

			merossConfig := config.Config{}

			if err := yaml.Unmarshal(merossConfigFile, &merossConfig); err != nil {
				t.Fatalf("Could not read meross input")
			}
			_, r, err := routes(&merossConfig, tc.internalConfigPath)

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
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":{"onoff":1,"rgb":255,"temperature":1,"luminance":-1}}`,
		},
		{
			name:         "toggle_no_error",
			method:       "POST",
			url:          "/meross/test2",
			data:         []byte(`{"code": "toggle"}`),
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "get_device_request",
			method:       "GET",
			url:          "/meross/test1",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["toggle","status","luminance","temperature","rgb","fade"]}`,
		},
		{
			name:         "get_base_request",
			method:       "GET",
			url:          "/meross/",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["test1","test2","test3"]}`,
		},
		{
			name:         "get_base_request_single_device",
			method:       "GET",
			url:          "/meross/",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/single_device_config.yaml",
			expectedCode: 404,
			expectedBody: "404 page not found\n",
		},
		{
			name:         "unsupported_device_method",
			method:       "DELETE",
			url:          "/meross/test1",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "unsupported_base_method",
			method:       "DELETE",
			url:          "/meross/",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "malformed_json_body",
			method:       "POST",
			url:          "/meross/test1",
			data:         []byte(`not_json`),
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
		},
		{
			name:         "malformed_query_string",
			method:       "POST",
			url:          "/meross/test1?monkeytest",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Malformed or empty query string"}`,
		},
		{
			name:         "unsupported_code_variable",
			method:       "POST",
			url:          "/meross/test1?code=monkey",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Parameter: code"}`,
		},
		{
			name:         "value_out_of_range",
			method:       "POST",
			url:          "/meross/test1?code=luminance&value=200",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Parameter: value (Min: 0, Max: 100)"}`,
		},
		{
			name:         "luminance_no_error",
			method:       "POST",
			url:          "/meross/test1?code=luminance&value=50",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "fade_no_error",
			method:       "POST",
			url:          "/meross/test1?code=fade",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_status_no_error",
			method:       "POST",
			url:          "/meross/?code=status&hosts=test1,test2",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":{"devices":[{"name":"test1","status":{"onoff":1,"rgb":255,"temperature":1,"luminance":-1}},{"name":"test2","status":{"onoff":1,"rgb":255,"temperature":1,"luminance":-1}}]}}`,
		},
		{
			name:         "multi_toggle_no_error",
			method:       "POST",
			url:          "/meross?code=toggle&hosts=test1,test2",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_fade_no_error",
			method:       "POST",
			url:          "/meross",
			data:         []byte(`{"code":"fade", "hosts": "test1,test3"}`),
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_luminance_no_error",
			method:       "POST",
			url:          "/meross",
			data:         []byte(`{"code":"luminance", "hosts": "test1,test3","value":"10"}`),
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			merossConfig: "testdata/merossConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			merossConfigFile, err := os.ReadFile(tc.merossConfig)
			if err != nil {
				t.Fatalf("Could not read meross input")
			}

			merossConfig := config.Config{}

			if err := yaml.Unmarshal(merossConfigFile, &merossConfig); err != nil {
				t.Fatalf("Could not read meross input")
			}

			base, routes, err := routes(&merossConfig, "device.yaml")

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

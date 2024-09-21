package hikvision

import (
	"bytes"
	"errors"
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
		Put struct {
			Code int    `yaml:"code"`
			JSON string `yaml:"json"`
		} `yaml:"put"`
	}{}

	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
		t.Fatalf("Could not parse serverConfigPath")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		switch r.Method {
		case "GET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(serverConfig.Get.Code)
			w.Write([]byte(serverConfig.Get.JSON))
		case "PUT":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(serverConfig.Put.Code)
			w.Write([]byte(serverConfig.Put.JSON))
		}

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
			name:          "no_error",
			configPath:    "testdata/hikvisionConfig/normal_config.yaml",
			routeCount:    4,
			expectedError: nil,
		},
		{
			name:          "hikvision_empty_yaml_config",
			configPath:    "testdata/hikvisionConfig/empty_yaml_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "hikvision_missing_config",
			configPath:    "testdata/hikvisionConfig/missing_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "hikvision_missing_config_parameter",
			configPath:    "testdata/hikvisionConfig/missing_config_parameter.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hikvisionConfigFile, err := os.ReadFile(tc.configPath)
			if err != nil {
				t.Fatalf("Could not read hikvision input")
			}

			hikvisionConfig := config.Config{}

			if err := yaml.Unmarshal(hikvisionConfigFile, &hikvisionConfig); err != nil {
				t.Fatalf("Could not read hikvision input")
			}
			_, r, err := routes(&hikvisionConfig)

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
		name            string
		method          string
		url             string
		data            []byte
		serverConfig    string
		hikvisionConfig string
		expectedCode    int
		expectedBody    string
	}{
		{
			name:            "status_no_error",
			method:          "POST",
			url:             "/hikvision/front_camera?code=status",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    200,
			expectedBody:    `{"message":"OK","data":{"onoff":"on","supplementlightmode":"irLight"}}`,
		},
		{
			name:            "toggle_no_error",
			method:          "POST",
			url:             "/hikvision/back_camera",
			data:            []byte(`{"code": "toggle"}`),
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    200,
			expectedBody:    `{"message":"OK"}`,
		},
		{
			name:            "get_device_request",
			method:          "GET",
			url:             "/hikvision/front_camera",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    200,
			expectedBody:    `{"message":"OK","data":["toggle","status"]}`,
		},
		{
			name:            "get_base_request",
			method:          "GET",
			url:             "/hikvision/",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    200,
			expectedBody:    `{"message":"OK","data":["front_camera","back_camera"]}`,
		},
		{
			name:            "get_base_request_single_device",
			method:          "GET",
			url:             "/hikvision/",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/single_device_config.yaml",
			expectedCode:    404,
			expectedBody:    "404 page not found\n",
		},
		{
			name:            "unsupported_device_method",
			method:          "DELETE",
			url:             "/hikvision/front_camera",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    405,
			expectedBody:    `{"message":"Method Not Allowed"}`,
		},
		{
			name:            "unsupported_base_method",
			method:          "DELETE",
			url:             "/hikvision/",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    405,
			expectedBody:    `{"message":"Method Not Allowed"}`,
		},
		{
			name:            "malformed_json_body",
			method:          "POST",
			url:             "/hikvision/front_camera",
			data:            []byte(`not_json`),
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    400,
			expectedBody:    `{"message":"Malformed Or Empty JSON Body"}`,
		},
		{
			name:            "malformed_query_string",
			method:          "POST",
			url:             "/hikvision/front_camera?monkeytest",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    400,
			expectedBody:    `{"message":"Malformed or empty query string"}`,
		},
		{
			name:            "unsupported_code_variable",
			method:          "POST",
			url:             "/hikvision/front_camera?code=monkey",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    400,
			expectedBody:    `{"message":"Invalid Parameter: code"}`,
		},
		{
			name:            "malformed_value",
			method:          "POST",
			url:             "/hikvision/front_camera?code=toggle&value=monkey",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    400,
			expectedBody:    `{"message":"Invalid Parameter: value"}`,
		},
		{
			name:            "multi_status_no_error",
			method:          "POST",
			url:             "/hikvision/?code=status&hosts=front_camera,back_camera",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    200,
			expectedBody:    `{"message":"OK","data":{"devices":[{"name":"back_camera","status":{"onoff":"off","supplementlightmode":"irLight"}},{"name":"front_camera","status":{"onoff":"on","supplementlightmode":"irLight"}}]}}`,
		},
		{
			name:            "multi_toggle_no_error",
			method:          "POST",
			url:             "/hikvision?code=toggle&hosts=front_camera,back_camera&value=eventIntelligence",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    200,
			expectedBody:    `{"message":"OK"}`,
		},
		{
			name:            "multi_toggle_no_value",
			method:          "POST",
			url:             "/hikvision?code=toggle&hosts=front_camera,back_camera",
			data:            nil,
			serverConfig:    "testdata/serverConfig/normal_responses.yaml",
			hikvisionConfig: "testdata/hikvisionConfig/normal_config.yaml",
			expectedCode:    200,
			expectedBody:    `{"message":"OK"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hikvisionConfigFile, err := os.ReadFile(tc.hikvisionConfig)
			if err != nil {
				t.Fatalf("Could not read hikvision input")
			}

			hikvisionConfig := config.Config{}

			if err := yaml.Unmarshal(hikvisionConfigFile, &hikvisionConfig); err != nil {
				t.Fatalf("Could not read hikvision input")
			}

			base, routes, err := routes(&hikvisionConfig)

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

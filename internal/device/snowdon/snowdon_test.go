package snowdon

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
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func setupWSServer(t *testing.T, serverConfigPath string) *httptest.Server {
	serverConfigFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		t.Fatalf("Could not read serverConfigPath")
	}

	serverConfig := struct {
		Codes []struct {
			Code     string `yaml:"code"`
			Response string `yaml:"response"`
		} `yaml:"codes"`
	}{}

	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
		t.Fatalf("Could not parse serverConfigPath")
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Could not upgrade websocket connection: %v", err)
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("Could not read websocket message: %v", err)
		}

		requestCode := string(msg)

		for _, s := range serverConfig.Codes {
			if requestCode == s.Code {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(s.Response)); err != nil {
					t.Fatalf("Could not write websocket message: %v", err)
				}
				return
			}
		}

		t.Fatalf("No canned websocket response found for input: %s", requestCode)
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
			routeCount:    6,
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
			routeCount:    2,
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			snowdonConfigFile, err := os.ReadFile(tc.configPath)
			if err != nil {
				t.Fatalf("Could not read config file")
			}

			snowdonConfig := config.Config{}
			if err := yaml.Unmarshal(snowdonConfigFile, &snowdonConfig); err != nil {
				t.Fatalf("Could not parse config file")
			}

			device := &Device{}
			routes, err := device.Routes(&snowdonConfig)

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(routes) != tc.routeCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.routeCount, len(routes))
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
			data:          []byte(`{"code":"power"}`),
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
			expectedBody:  `{"message":"OK","data":["bass_down","bass_up","dialog","flat","input","movie","music","mute","next","pair","play_pause","power","previous","status","treble_down","treble_up","volume_down","volume_up"]}`,
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
			expectedBody:  `{"message":"Invalid Parameter: code"}`,
		},
		{
			name:          "device_returns_invalid_payload",
			method:        "POST",
			url:           "/snowdon/test1?code=power",
			data:          nil,
			serverConfig:  "testdata/serverConfig/invalid_payload_response.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  500,
			expectedBody:  `{"message":"Internal Server Error"}`,
		},
		{
			name:          "device_returns_ng_status",
			method:        "POST",
			url:           "/snowdon/test1?code=power",
			data:          nil,
			serverConfig:  "testdata/serverConfig/ng_response.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  500,
			expectedBody:  `{"message":"Internal Server Error"}`,
		},
		{
			name:          "status_unknown_mapping",
			method:        "POST",
			url:           "/snowdon/test1?code=status",
			data:          nil,
			serverConfig:  "testdata/serverConfig/unknown_status_response.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  500,
			expectedBody:  `{"message":"Internal Server Error"}`,
		},
		{
			name:          "status_off_mapping",
			method:        "POST",
			url:           "/snowdon/test1?code=status",
			data:          nil,
			serverConfig:  "testdata/serverConfig/status_off_response.yaml",
			snowdonConfig: "testdata/snowdonConfig/normal_config.yaml",
			expectedCode:  200,
			expectedBody:  `{"message":"OK","data":{"onoff":"off","input":"off"}}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			snowdonConfigFile, err := os.ReadFile(tc.snowdonConfig)
			if err != nil {
				t.Fatalf("Could not read snowdon input")
			}

			snowdonConfig := config.Config{}
			if err := yaml.Unmarshal(snowdonConfigFile, &snowdonConfig); err != nil {
				t.Fatalf("Could not parse snowdon input")
			}

			base, routes, err := routes(&snowdonConfig, nil)
			if err != nil {
				t.Fatalf("routes returned an error: %v", err)
			}

			router := mux.NewRouter()
			for _, r := range routes {
				router.HandleFunc(r.Path, r.Handler)
			}

			server := setupWSServer(t, tc.serverConfig)
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
				t.Fatalf("Unexpected HTTP status code. Expected: %d, Got: %d", tc.expectedCode, recorder.Code)
			}

			if recorder.Body.String() != tc.expectedBody {
				t.Fatalf("Unexpected response body. Expected: %s, Got: %s", tc.expectedBody, recorder.Body.String())
			}
		})
	}
}

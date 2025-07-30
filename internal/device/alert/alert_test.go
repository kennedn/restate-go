package alert

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	config "github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	common "github.com/kennedn/restate-go/internal/device/alert/common"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

type code struct {
	Name     string `yaml:"name"`
	HttpCode int    `yaml:"httpCode"`
	Json     string `yaml:"json"`
}

type serverConfig struct {
	Codes []code
}

func findCode(name string, serverConfig *serverConfig) *code {
	for _, s := range serverConfig.Codes {
		if s.Name == name {
			return &s
		}
	}
	return nil
}

func setupHTTPServer(t *testing.T, serverConfigPath string) *httptest.Server {
	serverConfigFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		t.Fatalf("Could not read serverConfigPath")
	}

	serverConfig := &serverConfig{}
	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
		t.Fatalf("Could not parse serverConfigPath")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := common.Request{}

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}

		if err := json.Unmarshal(rawBody, &request); err != nil {
			t.Fatalf("Could not parse request body")
		}

		resp := findCode("normal", serverConfig)
		if request.Message == "error" {
			resp = findCode("internal-error", serverConfig)
		} else if request.Token == "error" {
			resp = findCode("bad-token", serverConfig)
		} else if request.User == "error" {
			resp = findCode("bad-user", serverConfig)
		} else if request.Priority.String() != "" {
			priority, err := request.Priority.Int64()
			if err != nil || priority < -2 || priority > 2 {
				resp = findCode("bad-priority", serverConfig)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.HttpCode)
		w.Write([]byte(resp.Json))

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
			configPath:    "testdata/alertConfig/normal_config.yaml",
			routeCount:    4,
			expectedError: nil,
		},
		{
			name:          "empty_yaml_config",
			configPath:    "testdata/alertConfig/empty_yaml_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config",
			configPath:    "testdata/alertConfig/missing_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config_parameter",
			configPath:    "testdata/alertConfig/missing_config_parameter.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "single_device_config",
			configPath:    "testdata/alertConfig/single_device_config.yaml",
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

			merossConfig := config.Config{}

			if err := yaml.Unmarshal(merossConfigFile, &merossConfig); err != nil {
				t.Fatalf("Could not read config file")
			}
			device := &Device{}
			r, err := device.Routes(&merossConfig)

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
		name         string
		method       string
		url          string
		data         []byte
		serverConfig string
		alertConfig  string
		expectedCode int
		expectedBody string
		contentType  string
	}{
		{
			name:         "no_error",
			method:       "POST",
			url:          "/alert/test1?message=test",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
			contentType:  "application/json",
		},
		{
			name:         "no_error_json",
			method:       "POST",
			url:          "/alert/test2",
			data:         []byte(`{"message": "test"}`),
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
			contentType:  "application/json",
		},
		{
			name:         "get_device_request",
			method:       "GET",
			url:          "/alert/test1",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
			contentType:  "application/json",
		},
		{
			name:         "get_base_request",
			method:       "GET",
			url:          "/alert/",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["test1","test2"]}`,
			contentType:  "application/json",
		},
		{
			name:         "get_base_request_single_device",
			method:       "GET",
			url:          "/alert/",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/single_device_config.yaml",
			expectedCode: 404,
			expectedBody: "404 page not found\n",
			contentType:  "application/json",
		},
		{
			name:         "unsupported_base_method",
			method:       "POST",
			url:          "/alert/",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
			contentType:  "application/json",
		},
		{
			name:         "malformed_json_body",
			method:       "POST",
			url:          "/alert/test1",
			data:         []byte(`not_json`),
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
			contentType:  "application/json",
		},
		{
			name:         "malformed_query_string",
			method:       "POST",
			url:          "/alert/test1?monkeytest",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Malformed or empty query string"}`,
			contentType:  "application/json",
		},
		{
			name:         "missing_message_variable",
			method:       "POST",
			url:          "/alert/test1",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Parameter: message"}`,
			contentType:  "application/json",
		},
		{
			name:         "invalid_user_variable",
			method:       "POST",
			url:          "/alert/test1?message=test&user=error",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"user identifier is not a valid user, group, or subscribed user key, see https://pushover.net/api#identifiers"}`,
			contentType:  "application/json",
		},
		{
			name:         "invalid_token_variable",
			method:       "POST",
			url:          "/alert/test1?message=test&token=error",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"application token is invalid, see https://pushover.net/api"}`,
			contentType:  "application/json",
		},
		{
			name:         "invalid_priority_variable",
			method:       "POST",
			url:          "/alert/test1?message=test&priority=3",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"priority is invalid, see https://pushover.net/api#priority"}`,
			contentType:  "application/json",
		},
		{
			name:         "internal_server_error",
			method:       "POST",
			url:          "/alert/test1?message=error",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 500,
			expectedBody: `{"message":"Internal Server Error"}`,
			contentType:  "application/json",
		},
		{
			name:         "utf8 content type",
			method:       "POST",
			url:          "/alert/test2",
			data:         []byte(`{"message": "test"}`),
			serverConfig: "testdata/serverConfig/normal_responses.yaml",
			alertConfig:  "testdata/alertConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
			contentType:  "application/json; charset=utf-8",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alertConfigFile, err := os.ReadFile(tc.alertConfig)
			if err != nil {
				t.Fatalf("Could not read alert input")
			}

			alertConfig := config.Config{}

			if err := yaml.Unmarshal(alertConfigFile, &alertConfig); err != nil {
				t.Fatalf("Could not read alert input")
			}

			base, routes, err := routes(&alertConfig)

			if err != nil {
				t.Fatalf("routes returned an error: %v", err)
			}
			router := mux.NewRouter()
			for _, r := range routes {
				router.HandleFunc(r.Path, r.Handler)
			}

			server := setupHTTPServer(t, tc.serverConfig)
			for _, d := range base.Devices {
				d.Base.URL = server.URL
			}

			defer server.Close()
			recorder := httptest.NewRecorder()

			request := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.data))
			if tc.data != nil {
				request.Header.Set("Content-Type", tc.contentType)
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

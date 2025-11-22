package msgeneric

import (
	"bytes"
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

const (
	normalMerossConfig = `apiVersion: v2
devices:
- type: meross
  config:
    name: test1
    deviceType: bulb
    timeoutMs: 500
    host: "127.0.0.1"
- type: meross
  config:
    name: test2
    deviceType: socket
    timeoutMs: 500
    host: "127.0.0.2"
- type: meross
  config:
    name: test3
    deviceType: bulb
    timeoutMs: 500
    host: "127.0.0.3"
- type: tvcom
  config:`
	unknownDeviceTypeMerossConfig = `apiVersion: v2
devices:
- type: meross
  config:
    name: office
    deviceType: bulb
    timeoutMs: 500
    host: "test.com"
- type: meross
  config:
    name: thermostat
    deviceType: test
    timeoutMs: 500
    host: "test.com"`
	missingMerossConfig = `apiVersion: v2
devices:
- type: meross
  config:
- type: meross
  config:`
	missingMerossConfigParameter = `apiVersion: v2
devices:
- type: meross
  config:
    name: office
    deviceType: bulb
    timeoutMs: 500
- type: meross
  config:
    deviceType: socket
    timeoutMs: 500
    host: "test.com"`
	singleDeviceMerossConfig = `apiVersion: v2
devices:
- type: meross
  config:
    name: test1
    deviceType: bulb
    timeoutMs: 500
    host: "127.0.0.1"`
	emptyConfig     = ``
	nonYamlConfig   = `not_yaml`
	baseNoEndpoints = `baseTemplate: '{"header":{"messageId":"","method":"%s","namespace":"%s","payloadVersion":1,"sign":"cfcd208495d565ef66e7dff9f98764da","timestamp":0},"payload":%s}'
endpoints:`
	normalServerConfig = `get:
  code: 200
  json: '{"header":{"messageId":"","namespace":"Appliance.System.All","method":"GETACK","payloadVersion":1,"from":"/appliance/2102259955984090842748e1e94e0605/publish","timestamp":1696614615,"timestampMs":134,"sign":"457fdad9d35da59ccd1008e6c18fbb4b"},"payload":{"all":{"system":{"hardware":{"type":"msl120d","subType":"eu","version":"2.0.0","chipType":"mt7682","uuid":"2102259955984090842748e1e94e0605","macAddress":"48:e1:e9:4e:06:05"},"firmware":{"version":"2.1.2","compileTime":"2020/04/30 14:45:31 GMT +08:00","wifiMac":"9c:53:22:90:d3:c8","innerIp":"192.168.1.140","server":"pc.int","port":8883,"userId":0},"time":{"timestamp":1696614615,"timezone":"","timeRule":[]},"online":{"status":2}},"digest":{"togglex":[{"channel":0,"onoff":1,"lmTime":1696611561}],"triggerx":[],"timerx":[],"light":{"capacity":6,"channel":0,"rgb":255,"temperature":1,"luminance":-1,"transform":-1}}}}}'
set:
  code: 200`
)

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
			internalConfig: bytesPtr([]byte(emptyConfig)),
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
			name:         "toggle_with_value_no_error",
			method:       "POST",
			url:          "/meross/test2",
			data:         []byte(`{"code": "toggle", "value": "1"}`),
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
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_toggle_with_value_no_error",
			method:       "POST",
			url:          "/meross?code=toggle&hosts=test1,test2&value=1",
			data:         nil,
			serverConfig: normalServerConfig,
			merossConfig: normalMerossConfig,
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
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

package tvcom

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func setupWebsocketServer(t *testing.T, tvcom *tvcom, timeout uint) *httptest.Server {
	// Create a test WebSocket server using httptest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Upgrade the connection to a WebSocket connection
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		// Disable timeouts
		conn.SetReadDeadline(time.Time{})
		conn.SetWriteDeadline(time.Time{})
		if err != nil {
			t.Fatalf("Failed to upgrade connection to WebSocket: %v", err)
		}
		defer conn.Close()

		// Read the message sent by the client
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if timeout == 1 {
			time.Sleep(1 * time.Millisecond)
		}

		command2 := string(message[1])
		data := string(message[6:8])
		var status string
		var o *opcode
		for _, op := range tvcom.Opcodes {
			if string(message[0:2]) == op.Code {
				o = &op
				break
			}
		}

		if _, ok := o.Data[data]; ok {
			status = "OK"
		} else {
			status = "NG"
		}

		if err := conn.WriteMessage(messageType, []byte(command2+" 00 "+status+data+"x")); err != nil {
			t.Fatalf("Failed to write response to WebSocket: %v", err)
		}
	}))

	return server
}

func TestWebsocketWriteWithResponse(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	testCases := []struct {
		name             string
		testCode         string
		testData         string
		expectedResponse []byte
		timeout          uint
		shouldPass       bool
	}{
		{
			name:             "status",
			testCode:         "ka",
			testData:         "ff",
			expectedResponse: []byte("a 00 OKffx"),
			timeout:          500,
			shouldPass:       true,
		},
		{
			name:             "power_change_state",
			testCode:         "kc",
			testData:         "01",
			expectedResponse: []byte("c 00 OK01x"),
			timeout:          500,
			shouldPass:       true,
		},
		{
			name:             "data_out_of_range",
			testCode:         "ka",
			testData:         "02",
			expectedResponse: []byte(""),
			timeout:          500,
			shouldPass:       false,
		},
		{
			name:             "timeout",
			testCode:         "kc",
			testData:         "ff",
			expectedResponse: []byte(""),
			timeout:          1,
			shouldPass:       false,
		},
		{
			name:             "malformed_opcode",
			testCode:         "malformed",
			testData:         "opcode",
			expectedResponse: []byte(""),
			timeout:          500,
			shouldPass:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tvcomConfigFile, err := os.ReadFile("testdata/tvcomConfig/single_device_config.yaml")
			if err != nil {
				t.Fatalf("Could not read tvcom input")
			}

			tvcomConfig := device.Config{}

			if err := yaml.Unmarshal(tvcomConfigFile, &tvcomConfig); err != nil {
				t.Fatalf("Could not read tvcom input")
			}
			base, _, err := routes(&tvcomConfig, "testdata/baseConfig/long_opcodes_device.yaml")
			if err != nil {
				t.Fatalf("routes returned an error: %v", err)
			}

			tvcom := base.Devices[0]

			tvcom.Timeout = tc.timeout

			server := setupWebsocketServer(t, tvcom, tc.timeout)
			defer server.Close()

			tvcom.Host = strings.TrimPrefix(server.URL, "http://")

			var o *opcode = nil
			for _, op := range tvcom.Opcodes {
				if tc.testCode == op.Code {
					o = &op
					break
				}
			}
			if o == nil {
				t.Fatalf("Could not locate opcode using testCode %s", tc.testCode)
			}

			response, err := o.websocketWriteWithResponse(tc.testData)
			if err != nil && tc.shouldPass {
				t.Fatalf("websocketWriteWithResponse returned an error: %v", err)
			}
			if err == nil && !tc.shouldPass {
				t.Fatalf("websocketWriteWithResponse did not error")
			}

			if string(response) != string(tc.expectedResponse) {
				t.Errorf("Unexpected response. Expected: %s, Got: %s", string(tc.expectedResponse), response)
			}
		})
	}
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
			configPath:         "testdata/tvcomConfig/normal_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         50,
			expectedError:      nil,
		},
		{
			name:               "base_bad_path",
			configPath:         "testdata/tvcomConfig/normal_config.yaml",
			internalConfigPath: "non/existant/file",
			routeCount:         0,
			expectedError:      &fs.PathError{},
		},
		{
			name:               "base_0_endpoints",
			configPath:         "testdata/tvcomConfig/normal_config.yaml",
			internalConfigPath: "testdata/baseConfig/0_opcodes_device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "base_non_yaml_config",
			configPath:         "testdata/tvcomConfig/normal_config.yaml",
			internalConfigPath: "testdata/baseConfig/non_yaml_config.yaml",
			routeCount:         0,
			expectedError:      &yaml.TypeError{},
		},
		{
			name:               "base_empty_yaml_config",
			configPath:         "testdata/tvcomConfig/normal_config.yaml",
			internalConfigPath: "testdata/baseConfig/empty_yaml_config.yaml",
			routeCount:         0,
			expectedError:      &fs.PathError{},
		},
		{
			name:               "tvcom_empty_yaml_config",
			configPath:         "testdata/tvcomConfig/empty_yaml_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "tvcom_missing_config",
			configPath:         "testdata/tvcomConfig/missing_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "tvcom_missing_config_parameter",
			configPath:         "testdata/tvcomConfig/missing_config_parameter.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tvcomConfigFile, err := os.ReadFile(tc.configPath)
			if err != nil {
				t.Fatalf("Could not read tvcom input")
			}

			tvcomConfig := device.Config{}

			if err := yaml.Unmarshal(tvcomConfigFile, &tvcomConfig); err != nil {
				t.Fatalf("Could not read tvcom input")
			}

			_, r, err := routes(&tvcomConfig, tc.internalConfigPath)

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
		tvcomConfig  string
		expectedCode int
		expectedBody string
	}{
		{
			name:         "no_error",
			method:       "POST",
			url:          "/tvcom/test1/power?code=on",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "no_error_json",
			method:       "POST",
			url:          "/tvcom/test2/power",
			data:         []byte(`{"code": "status"}`),
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":"status"}`,
		},
		{
			name:         "get_device_request",
			method:       "GET",
			url:          "/tvcom/test1",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["abnormal_state","add_skip","aspect_ratio","auto_configure_vga","backlight_lcd","balance","brightness","colour","colour_temp","contrast","input_select","ir_key","ism_method_plasma","osd_select","power","power_saving_plasma","remote_lock","screen_mute","sharpness","tint","volume","volume_mute"]}`,
		},
		{
			name:         "unsupported_device_method",
			method:       "POST",
			url:          "/tvcom/test1",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "unsupported_opcode_method",
			method:       "DELETE",
			url:          "/tvcom/test2/power",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "get_opcode_request",
			method:       "GET",
			url:          "/tvcom/test2/power",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["off","on","status"]}`,
		},
		{
			name:         "get_base_request",
			method:       "GET",
			url:          "/tvcom/",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["test1","test2"]}`,
		},
		{
			name:         "get_base_request_single_device",
			method:       "GET",
			url:          "/tvcom/",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/single_device_config.yaml",
			expectedCode: 404,
			expectedBody: "404 page not found\n",
		},
		{
			name:         "unsupported_base_method",
			method:       "POST",
			url:          "/tvcom/",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "malformed_json_body",
			method:       "POST",
			url:          "/tvcom/test1/volume",
			data:         []byte(`not_json`),
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
		},
		{
			name:         "malformed_query_string",
			method:       "POST",
			url:          "/tvcom/test2/volume?monkeytest",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Malformed or empty query string"}`,
		},
		{
			name:         "missing_code_variable",
			method:       "POST",
			url:          "/tvcom/test1/input_select",
			data:         nil,
			tvcomConfig:  "testdata/tvcomConfig/normal_config.yaml",
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Parameter: code"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tvcomConfigFile, err := os.ReadFile(tc.tvcomConfig)
			if err != nil {
				t.Fatalf("Could not read tvcom input")
			}

			tvcomConfig := device.Config{}

			if err := yaml.Unmarshal(tvcomConfigFile, &tvcomConfig); err != nil {
				t.Fatalf("Could not read tvcom input")
			}

			base, routes, err := routes(&tvcomConfig, "device.yaml")
			if err != nil {
				t.Fatalf("routes returned an error: %v", err)
			}

			server := setupWebsocketServer(t, base.Devices[0], 500)
			defer server.Close()
			for i := range base.Devices {
				base.Devices[i].Host = strings.TrimPrefix(server.URL, "http://")
			}

			router := mux.NewRouter()
			for _, r := range routes {
				router.HandleFunc(r.Path, r.Handler)
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

package tvcom

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func setupWebsocketServer(t *testing.T, tvcom *tvcom, timeout time.Duration) *httptest.Server {
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
	testCases := []struct {
		name             string
		testCode         string
		testData         string
		expectedResponse []byte
		timeout          time.Duration
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
			tvcom, _, err := generateRoutesFromConfig(tc.timeout, "ws://test.com", "testdata/long_opcodes_device.yaml")
			if err != nil {
				t.Fatalf("generateRoutesFromConfig returned an error: %v", err)
			}

			server := setupWebsocketServer(t, tvcom, tc.timeout)
			defer server.Close()

			tvcom.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http")

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
	testCases := []struct {
		name          string
		timeout       time.Duration
		websocketURL  string
		configPath    string
		routeCount    int
		expectedError error
	}{
		{
			name:          "bad_path",
			timeout:       500,
			websocketURL:  "ws://test.com",
			configPath:    "non/existant/file",
			routeCount:    0,
			expectedError: &fs.PathError{},
		},
		{
			name:          "default_config",
			timeout:       500,
			websocketURL:  "ws://test.com",
			configPath:    "device.yaml",
			routeCount:    24,
			expectedError: nil,
		},
		{
			name:          "7_route_config",
			timeout:       500,
			websocketURL:  "ws://test.com",
			configPath:    "testdata/5_opcodes_device.yaml",
			routeCount:    7,
			expectedError: nil,
		},
		{
			name:          "non_yaml_config",
			timeout:       500,
			websocketURL:  "ws://test.com",
			configPath:    "testdata/malformed_device.yaml",
			routeCount:    0,
			expectedError: &yaml.TypeError{},
		},
		{
			name:          "empty_yaml_config",
			timeout:       500,
			websocketURL:  "ws://test.com",
			configPath:    "testdata/0_opcodes_device.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Routes(tc.timeout, tc.websocketURL, tc.configPath)

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(r) != tc.routeCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.routeCount, len(r))
			}

		})
	}
}

func TestTvcomHandler(t *testing.T) {
	testCases := []struct {
		name         string
		method       string
		url          string
		data         []byte
		expectedCode int
		expectedBody string
	}{
		{
			name:         "GET_/tvcom",
			method:       "GET",
			url:          "/tvcom",
			data:         nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["abnormal_state","add_skip","aspect_ratio","auto_configure_vga","backlight_lcd","balance","brightness","colour","colour_temp","contrast","input_select","ir_key","ism_method_plasma","osd_select","power","power_saving_plasma","remote_lock","screen_mute","sharpness","tint","volume","volume_mute"]}`,
		},
		{
			name:         "GET_/tvcom/",
			method:       "GET",
			url:          "/tvcom/",
			data:         nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["abnormal_state","add_skip","aspect_ratio","auto_configure_vga","backlight_lcd","balance","brightness","colour","colour_temp","contrast","input_select","ir_key","ism_method_plasma","osd_select","power","power_saving_plasma","remote_lock","screen_mute","sharpness","tint","volume","volume_mute"]}`,
		},
		{
			name:         "POST_/tvcom",
			method:       "POST",
			url:          "/tvcom",
			data:         nil,
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
	}

	routes, err := Routes(500, "ws://test.com", "device.yaml")
	if err != nil {
		t.Fatalf("Routes returned an error: %v", err)
	}
	router := mux.NewRouter()
	for _, r := range routes {
		router.HandleFunc(r.Path, r.Handler)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			request := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.data))

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

func TestOpcodeHandler(t *testing.T) {
	testCases := []struct {
		method       string
		url          string
		data         []byte
		expectedCode int
		expectedBody string
	}{
		{
			method:       "POST",
			url:          "/tvcom/power?code=status",
			data:         nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":"status"}`,
		},
		{
			method:       "GET",
			url:          "/tvcom/power",
			data:         nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["off","on","status"]}`,
		},
		{
			method:       "DELETE",
			url:          "/tvcom/power",
			data:         nil,
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			method:       "POST",
			url:          "/tvcom/power",
			data:         []byte(`{"code": "status"}`),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":"status"}`,
		},
		{
			method:       "POST",
			url:          "/tvcom/power",
			data:         []byte(`malformed_json`),
			expectedCode: 400,
			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
		},
		{
			method:       "POST",
			url:          "/tvcom/input_select?code=hdmi_3",
			data:         nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":"hdmi_3"}`,
		},
		{
			method:       "POST",
			url:          "/tvcom/input_select?code=non_existant_code",
			data:         nil,
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Variable"}`,
		},
	}

	tvcom, routes, err := generateRoutesFromConfig(500, "ws://test.com", "device.yaml")
	if err != nil {
		t.Fatalf("generateRoutesFromConfig returned an error: %v", err)
	}
	router := mux.NewRouter()
	for _, r := range routes {
		router.HandleFunc(r.Path, r.Handler)
	}

	server := setupWebsocketServer(t, tvcom, 500)
	defer server.Close()

	tvcom.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http")

	for _, tc := range testCases {
		t.Run(tc.method+tc.url, func(t *testing.T) {
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

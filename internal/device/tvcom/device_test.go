package tvcom

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
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
			t.Fatalf("Failed to read message from WebSocket: %v", err)
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

func setupWebsocketServerAndOpcode(t *testing.T, testData []byte, expectedResponse []byte, timeout time.Duration) (*opcode, *httptest.Server) {
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
		messageType, _, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("Failed to read message from WebSocket: %v", err)
		}

		if timeout == 1 {
			time.Sleep(1 * time.Millisecond)
		}

		if err := conn.WriteMessage(messageType, expectedResponse); err != nil {
			t.Fatalf("Failed to write response to WebSocket: %v", err)
		}
	}))

	// Create an instance of your 'opcode' struct and set 'Tvcom.WebSocketURL' to the test server's URL
	o := &opcode{
		Tvcom: &tvcom{
			WebSocketURL: "ws" + strings.TrimPrefix(server.URL, "http"),
			Timeout:      timeout, // Set your desired timeout here
		},
		Code: "test_code",
	}
	return o, server
}

func TestWebsocketWriteWithResponse(t *testing.T) {
	testCases := []struct {
		name             string
		testData         []byte
		expectedResponse []byte
		timeout          time.Duration
		shouldPass       bool
	}{
		{
			"status",
			[]byte("ka 00 ff\r"), []byte("a 00 OK01x"), 500, true,
		},
		{
			"power_change_state",
			[]byte("kc 00 01\r"), []byte("c 00 OK01x"), 500, true,
		},
		{
			"data_out_of_range",
			[]byte("ka 00 02\r"), []byte("a 00 NG00x"), 500, false,
		},
		{
			"timeout",
			[]byte("kc 00 ff\r"), []byte("a 00 OK00x"), 1, false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o, server := setupWebsocketServerAndOpcode(t, tc.testData, tc.expectedResponse, tc.timeout)
			defer server.Close()

			// Call your function with test data
			response, err := o.websocketWriteWithResponse(string(tc.testData))
			if err != nil && tc.shouldPass {
				t.Fatalf("websocketWriteWithResponse returned an error: %v", err)
			}
			if err == nil && !tc.shouldPass {
				t.Fatalf("websocketWriteWithResponse did not error")
			}

			// Add assertions to check the response
			if string(response) != string(tc.expectedResponse) && tc.shouldPass {
				t.Errorf("Unexpected response. Expected: %s, Got: %s", string(tc.expectedResponse), response)
			}
			// Add assertions to check the response
			if string(response) == string(tc.expectedResponse) && !tc.shouldPass {
				t.Errorf("Response should not match expected. Expected: %s, Got: %s", string(tc.expectedResponse), response)
			}
		})
	}
}

func TestRoutes(t *testing.T) {
	testCases := []struct {
		name         string
		timeout      time.Duration
		websocketURL string
		configPath   string
		routeCount   int
		errorMessage string
	}{
		{
			name:         "bad_path",
			timeout:      500,
			websocketURL: "ws://test.com",
			configPath:   "non/existant/file",
			routeCount:   0,
			errorMessage: "no such file or directory",
		},
		{
			name:         "default_config",
			timeout:      500,
			websocketURL: "ws://test.com",
			configPath:   "device.yaml",
			routeCount:   24,
			errorMessage: "",
		},
		{
			name:         "7_route_config",
			timeout:      500,
			websocketURL: "ws://test.com",
			configPath:   "testdata/device_5_opcodes.yaml",
			routeCount:   7,
			errorMessage: "",
		},
		{
			name:         "non_yaml_config",
			timeout:      500,
			websocketURL: "ws://test.com",
			configPath:   "testdata/device_malformed.yaml",
			routeCount:   0,
			errorMessage: "",
		},
		{
			name:         "empty_yaml_config",
			timeout:      500,
			websocketURL: "ws://test.com",
			configPath:   "testdata/device_0_opcodes.yaml",
			routeCount:   0,
			errorMessage: "No routes could be retrieved from config at path",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Routes(tc.timeout, tc.websocketURL, tc.configPath)
			if err != nil {
				assert.Containsf(t, err.Error(), tc.errorMessage, "Error should contain \"%v\", got \"%v\"", tc.errorMessage, err)
			} else {
				assert.Equal(t, tc.errorMessage, "", "Expected an error that contains message \"%v\", but got nil", tc.errorMessage)
			}

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
				headers := make(http.Header)
				headers.Add("Content-Type", "application/json")
				request.Header = headers
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

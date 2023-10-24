package wol

import (
	"bytes"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	device "github.com/kennedn/restate-go/internal/device/common"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"gopkg.in/yaml.v3"
)

type mockPacketConn struct {
	readFunc    func(b []byte) (int, net.Addr, error)
	writeToFunc func(b []byte, addr net.Addr) (int, error)
}

func (m *mockPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	return m.readFunc(b)
}

func (m *mockPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	return m.writeToFunc(b, addr)
}

func (m *mockPacketConn) Close() error {
	return nil
}

func (m *mockPacketConn) LocalAddr() net.Addr {
	return nil
}

func (m *mockPacketConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockPacketConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockPacketConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type TimeoutError struct {
	error
}

func (e *TimeoutError) Timeout() bool {
	return true
}

func (e *TimeoutError) Temporary() bool {
	return true
}

func (e *TimeoutError) Error() string {
	return ""
}

func TestWakeOnLan(t *testing.T) {

	testCases := []struct {
		name            string
		macAddress      string
		expectedError   error
		expectedPayload []byte
	}{
		{
			name:          "valid_mac_address",
			macAddress:    "00:11:22:33:44:55",
			expectedError: nil,
			expectedPayload: []byte{
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
			},
		},
		{
			name:            "long_mac_address",
			macAddress:      "00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01",
			expectedError:   errors.New(""),
			expectedPayload: nil,
		},
		{
			name:            "invalid_mac_address",
			macAddress:      "not a mac address",
			expectedError:   &net.AddrError{},
			expectedPayload: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var payload []byte

			mockConn := &mockPacketConn{
				writeToFunc: func(b []byte, addr net.Addr) (int, error) {
					payload = b
					return 0, nil
				},
				readFunc: func(b []byte) (int, net.Addr, error) {
					return 0, nil, nil
				},
			}

			w := &wol{
				Name:       tc.name,
				Timeout:    100,
				MacAddress: tc.macAddress,
				base: base{
					udpAddr: &net.UDPAddr{
						IP:   net.ParseIP("127.0.0.255"),
						Port: 9,
					},
				},
				conn: mockConn,
			}

			err := w.wakeOnLan()

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if slices.Compare(payload, tc.expectedPayload) != 0 {
				t.Fatalf("Payload does not match the expected payload")
			}
		})
	}
}

func TestPing(t *testing.T) {

	testCases := []struct {
		name               string
		host               string
		expectTimeoutError bool
		expectedMessage    *icmp.Message
		readError          bool
		writeError         bool
	}{
		{
			name: "valid_icmp_message",
			host: "127.0.0.1",
			expectedMessage: &icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{
					ID:  os.Getpid() & 0xffff,
					Seq: 1,
				},
			},
			expectTimeoutError: false,
			readError:          false,
			writeError:         false,
		},
		{
			name: "read_error",
			host: "127.0.0.1",
			expectedMessage: &icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{
					ID:  os.Getpid() & 0xffff,
					Seq: 1,
				},
			},
			expectTimeoutError: true,
			writeError:         false,
			readError:          true,
		},
		{
			name: "write_error",
			host: "127.0.0.1",
			expectedMessage: &icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{
					ID:  os.Getpid() & 0xffff,
					Seq: 1,
				},
			},
			expectTimeoutError: true,
			writeError:         true,
			readError:          false,
		},
		{
			name:               "malformed_host",
			host:               "monkey",
			expectedMessage:    nil,
			expectTimeoutError: false,
			writeError:         false,
			readError:          false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var message *icmp.Message
			var err error

			mockConn := &mockPacketConn{
				writeToFunc: func(b []byte, addr net.Addr) (int, error) {
					message, err = icmp.ParseMessage(1, b)
					if err != nil {
						t.Fatalf("Could not parse message")
					}
					if tc.writeError {
						err = &TimeoutError{}
					}
					return 0, err
				},
				readFunc: func(b []byte) (int, net.Addr, error) {
					err = nil
					if tc.readError {
						err = &TimeoutError{}
					}
					return 0, nil, err
				},
			}

			w := &wol{
				Name:    tc.name,
				Timeout: 100,
				Host:    tc.host,
				base:    base{},
				conn:    mockConn,
			}

			err = w.ping()

			if netErr, ok := err.(net.Error); ok && netErr.Timeout() != tc.expectTimeoutError {
				if tc.expectTimeoutError {
					t.Fatalf("Expected timeout error")
				} else {
					t.Fatalf("Unexpected timeout error")
				}
			}

			var expectedMessage *icmp.Message
			if tc.expectedMessage == nil {
				expectedMessage = nil
			} else {
				msgBytes, err := tc.expectedMessage.Marshal(nil)
				if err != nil {
					t.Fatalf("Could not marshal expectedMessage")
				}

				expectedMessage, err = icmp.ParseMessage(1, msgBytes)
				if err != nil {
					t.Fatalf("Could not parse expectedMessage")
				}
			}

			assert.Equal(t, expectedMessage, message, "Message does not match expected value")

		})
	}
}

func TestRoutes(t *testing.T) {
	testCases := []struct {
		name          string
		configPath    string
		routeCount    int
		expectedError error
	}{
		{
			name:          "wol_normal_config",
			configPath:    "testdata/config/normal_input.yaml",
			routeCount:    4,
			expectedError: nil,
		},
		{
			name:          "wol_empty_yaml_config",
			configPath:    "testdata/config/empty_yaml_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "wol_missing_config",
			configPath:    "testdata/config/missing_config.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
		{
			name:          "wol_missing_config_parameter",
			configPath:    "testdata/config/missing_config_parameter.yaml",
			routeCount:    0,
			expectedError: errors.New(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			merossConfigFile, err := os.ReadFile(tc.configPath)
			if err != nil {
				t.Fatalf("Could not read meross input")
			}

			merossConfig := device.Config{}

			if err := yaml.Unmarshal(merossConfigFile, &merossConfig); err != nil {
				t.Fatalf("Could not read meross input")
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

func TestWolHandler(t *testing.T) {
	testCases := []struct {
		name         string
		method       string
		url          string
		data         []byte
		writeError   error
		readError    error
		expectedCode int
		expectedBody string
	}{
		{
			name:         "status_no_error",
			method:       "POST",
			url:          "/wol/test1?code=status",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":"on"}`,
		},
		{
			name:         "status_read_timeout_error",
			method:       "POST",
			url:          "/wol/test1?code=status",
			data:         nil,
			readError:    &TimeoutError{},
			writeError:   nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":"off"}`,
		},
		{
			name:         "status_write_timeout_error",
			method:       "POST",
			url:          "/wol/test2",
			data:         []byte(`{"code": "status"}`),
			readError:    nil,
			writeError:   &TimeoutError{},
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":"off"}`,
		},
		{
			name:         "status_read_unknown_error",
			method:       "POST",
			url:          "/wol/test1?code=status",
			data:         nil,
			readError:    errors.New(""),
			writeError:   nil,
			expectedCode: 500,
			expectedBody: `{"message":"Internal Server Error"}`,
		},
		{
			name:         "power_no_error",
			method:       "POST",
			url:          "/wol/test1?code=power",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "power_timeout_write_error",
			method:       "POST",
			url:          "/wol/test2",
			data:         []byte(`{"code": "power"}`),
			readError:    nil,
			writeError:   &TimeoutError{},
			expectedCode: 500,
			expectedBody: `{"message":"Internal Server Error"}`,
		},
		{
			name:         "get_device_request",
			method:       "GET",
			url:          "/wol/test1",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["power","status"]}`,
		},
		{
			name:         "get_base_request",
			method:       "GET",
			url:          "/wol/",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["test1","test2"]}`,
		},
		{
			name:         "unsupported_device_method",
			method:       "DELETE",
			url:          "/wol/test1",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "unsupported_base_method",
			method:       "POST",
			url:          "/wol/",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "malformed_json_body",
			method:       "POST",
			url:          "/wol/test1",
			data:         []byte(`not_json`),
			readError:    nil,
			writeError:   nil,
			expectedCode: 400,
			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
		},
		{
			name:         "malformed_query_string",
			method:       "POST",
			url:          "/wol/test1?monkeytest",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 400,
			expectedBody: `{"message":"Malformed or empty query string"}`,
		},
		{
			name:         "unsupported_code_variable",
			method:       "POST",
			url:          "/wol/test1?code=monkey",
			data:         nil,
			readError:    nil,
			writeError:   nil,
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Parameter: code"}`,
		},
	}

	wolConfigFile, err := os.ReadFile("testdata/config/normal_input.yaml")
	if err != nil {
		t.Fatalf("Could not read wol input")
	}

	wolConfig := device.Config{}

	if err := yaml.Unmarshal(wolConfigFile, &wolConfig); err != nil {
		t.Fatalf("Could not read wol input")
	}

	base, routes, err := routes(&wolConfig)

	if err != nil {
		t.Fatalf("generateRoutesFromConfig returned an error: %v", err)
	}
	router := mux.NewRouter()
	for _, r := range routes {
		router.HandleFunc(r.Path, r.Handler)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockConn := &mockPacketConn{
				writeToFunc: func(b []byte, addr net.Addr) (int, error) {
					return 0, tc.writeError
				},
				readFunc: func(b []byte) (int, net.Addr, error) {
					return 0, nil, tc.readError
				},
			}
			for i, _ := range base.devices {
				base.devices[i].conn = mockConn
			}
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

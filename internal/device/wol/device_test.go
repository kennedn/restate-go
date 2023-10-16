package wol

import (
	"errors"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
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

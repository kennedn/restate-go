package frigate

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	alert "github.com/kennedn/restate-go/internal/device/alert/common"

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

type mockMqttClient struct {
	subscribeFunc func(client mqtt.Client, callback mqtt.MessageHandler)
}

// IsConnected returns a hardcoded true value indicating the client is always connected
func (mc *mockMqttClient) IsConnected() bool {
	return true
}

// IsConnectionOpen returns a hardcoded true value indicating the connection is always open
func (mc *mockMqttClient) IsConnectionOpen() bool {
	return true
}

// Connect simulates a connection and returns a mock Token
func (mc *mockMqttClient) Connect() mqtt.Token {
	return nil
}

// Disconnect simulates a disconnect operation with no real effect
func (mc *mockMqttClient) Disconnect(quiesce uint) {}

// Publish simulates publishing a message and returns a mock Token
func (mc *mockMqttClient) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token {
	return nil
}

// Subscribe simulates a subscription and returns a mock Token
func (mc *mockMqttClient) Subscribe(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token {
	mc.subscribeFunc(mc, callback)
	return nil
}

// SubscribeMultiple simulates multiple subscriptions and returns a mock Token
func (mc *mockMqttClient) SubscribeMultiple(filters map[string]byte, callback mqtt.MessageHandler) mqtt.Token {
	return nil
}

// Unsubscribe simulates unsubscribing from topics and returns a mock Token
func (mc *mockMqttClient) Unsubscribe(topics ...string) mqtt.Token {
	return nil
}

// AddRoute simulates adding a route with a message handler
func (mc *mockMqttClient) AddRoute(topic string, callback mqtt.MessageHandler) {}

// OptionsReader returns a nil ClientOptionsReader
func (mc *mockMqttClient) OptionsReader() mqtt.ClientOptionsReader {
	return mqtt.ClientOptionsReader{}
}

// mockMessage is a mock implementation of the Message interface
type mockMessage struct {
	duplicate bool
	qos       byte
	retained  bool
	topic     string
	messageID uint16
	payload   []byte
	ack       func()
	once      sync.Once
}

// Duplicate returns whether the message is a duplicate
func (m *mockMessage) Duplicate() bool {
	return m.duplicate
}

// Qos returns the QoS level of the message
func (m *mockMessage) Qos() byte {
	return m.qos
}

// Retained returns whether the message is retained
func (m *mockMessage) Retained() bool {
	return m.retained
}

// Topic returns the topic of the message
func (m *mockMessage) Topic() string {
	return m.topic
}

// MessageID returns the message ID
func (m *mockMessage) MessageID() uint16 {
	return m.messageID
}

// Payload returns the payload of the message
func (m *mockMessage) Payload() []byte {
	return m.payload
}

// Ack performs the acknowledgment callback
func (m *mockMessage) Ack() {
	m.once.Do(m.ack)
}

func findCode(name string, serverConfig *serverConfig) *code {
	for _, s := range serverConfig.Codes {
		if s.Name == name {
			return &s
		}
	}
	return nil
}

func TestListeners(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	testCases := []struct {
		name          string
		configPath    string
		listenerCount int
		expectedError error
	}{
		{
			name:          "default_config",
			configPath:    "testdata/alertConfig/normal_config.yaml",
			listenerCount: 2,
			expectedError: nil,
		},
		{
			name:          "empty_yaml_config",
			configPath:    "testdata/alertConfig/empty_yaml_config.yaml",
			listenerCount: 0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config",
			configPath:    "testdata/alertConfig/missing_config.yaml",
			listenerCount: 0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config_parameter",
			configPath:    "testdata/alertConfig/missing_config_parameter.yaml",
			listenerCount: 0,
			expectedError: errors.New(""),
		},
		{
			name:          "single_device_config",
			configPath:    "testdata/alertConfig/single_device_config.yaml",
			listenerCount: 1,
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configFile, err := os.ReadFile(tc.configPath)
			if err != nil {
				t.Fatalf("Could not read config file")
			}

			configMap := config.Config{}

			if err := yaml.Unmarshal(configFile, &configMap); err != nil {
				t.Fatalf("Could not read config file")
			}

			_, l, err := listeners(&configMap, &mockMqttClient{})

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(l) != tc.listenerCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.listenerCount, len(l))
			}

		})
	}
}

func TestListen(t *testing.T) {
	logging.SetLogLevel(logging.Error)
	testCases := []struct {
		name                 string
		configPath           string
		thumbnailPath        string
		mqttPayload          []byte
		expectedThumbnailUrl string
		expectedRequest      alert.Request
		expectedError        error
		expectedAlertHit     int
		expectedThumbnailHit int
	}{
		{
			name:                 "no_error",
			configPath:           "testdata/alertConfig/single_device_config.yaml",
			thumbnailPath:        "testdata/serverConfig/thumbnail.jpg",
			mqttPayload:          []byte(`{"type":"new","before":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"},"after":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedThumbnailUrl: "/api/events/1723938588.335444-ctmuov/thumbnail.jpg",
			expectedRequest: alert.Request{
				Message:          "Person detected at Front Enterance",
				Title:            "Frigate",
				Priority:         "0",
				Token:            "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
				User:             "",
				URL:              "http://test.url",
				URLTitle:         "Open Frigate",
				AttachmentBase64: "/9j/2wBDAAMCAgICAgMCAgIDAwMDBAYEBAQEBAgGBgUGCQgKCgkICQkKDA8MCgsOCwkJDRENDg8QEBEQCgwSExIQEw8QEBD/yQALCAABAAEBAREA/8wABgAQEAX/2gAIAQEAAD8A0s8g/9k=",
				AttachmentType:   "image/jpeg",
			},
			expectedError:        nil,
			expectedAlertHit:     1,
			expectedThumbnailHit: 1,
		},
		{
			name:                 "no_device_in_config",
			configPath:           "testdata/alertConfig/no_device_config.yaml",
			thumbnailPath:        "testdata/serverConfig/thumbnail.jpg",
			mqttPayload:          []byte(`{"type":"new","before":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"},"after":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedRequest:      alert.Request{},
			expectedThumbnailUrl: "/api/events/1723938588.335444-ctmuov/thumbnail.jpg",
			expectedError:        errors.New("no listeners found in config"),
			expectedAlertHit:     0,
			expectedThumbnailHit: 0,
		},
		{
			name:                 "frigate_server_404",
			configPath:           "testdata/alertConfig/single_device_config.yaml",
			thumbnailPath:        "testdata/serverConfig/does_not_exist.jpg",
			mqttPayload:          []byte(`{"type":"new","before":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"},"after":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedThumbnailUrl: "/api/events/1723938588.335444-ctmuov/thumbnail.jpg",
			expectedRequest: alert.Request{
				Message:          "Person detected at Front Enterance",
				Title:            "Frigate",
				Priority:         "0",
				Token:            "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
				User:             "",
				URL:              "http://test.url",
				URLTitle:         "Open Frigate",
				AttachmentBase64: "",
				AttachmentType:   "",
			},
			expectedError:        nil,
			expectedAlertHit:     1,
			expectedThumbnailHit: 1,
		},
		{
			name:                 "malformed_payload",
			configPath:           "testdata/alertConfig/single_device_config.yaml",
			thumbnailPath:        "testdata/serverConfig/thumbnail.jpg",
			mqttPayload:          []byte(`{"type":"new",mb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedThumbnailUrl: "/api/events/1723938588.335444-ctmuov/thumbnail.jpg",
			expectedRequest:      alert.Request{},
			expectedError:        nil,
			expectedAlertHit:     0,
			expectedThumbnailHit: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup wait group to wait for mqtt callback instead of immediately returning
			var wg sync.WaitGroup
			wg.Add(1)

			var testError error
			alertHit := 0
			thumbnailHit := 0

			// Run test in an anonymous function to capture error for assertion
			func() {
				// Capture error in the case of a panic
				defer func() {
					if r := recover(); r != nil {
						testError = errors.New("panic occurred")
					}
				}()

				// Read the config file
				configFile, err := os.ReadFile(tc.configPath)
				if err != nil {
					testError = err
					return
				}

				configMap := config.Config{}

				if err := yaml.Unmarshal(configFile, &configMap); err != nil {
					testError = err
					return
				}

				// Create a mock mqtt client, Upon topic subscription, immediately calls the user provided callback with the payload under test
				mockClient := &mockMqttClient{
					subscribeFunc: func(client mqtt.Client, callback mqtt.MessageHandler) {
						// Mark wait group as complete
						defer wg.Done()
						callback(client, &mockMessage{
							payload: tc.mqttPayload,
						})
					},
				}

				// Call the listeners function to get the listener objects
				_, ls, err := listeners(&configMap, mockClient)
				if err != nil {
					testError = err
					return
				}

				// Get the first listener object
				l := ls[0]

				// Create a mock frigate server to serve a thumbnail image
				frigateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					thumbnailHit++
					// Assert that the request path matches the expected thumbnail url
					if r.URL.Path != tc.expectedThumbnailUrl {
						testError = err
						return
					}
					http.ServeFile(w, r, tc.thumbnailPath)
				}))
				defer frigateServer.Close()

				// Load dummy alert responses for mock alert server
				serverConfigPath := "testdata/serverConfig/alert_responses.yaml"
				serverConfigFile, err := os.ReadFile(serverConfigPath)
				if err != nil {
					testError = err
					return
				}

				serverConfig := &serverConfig{}
				if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
					testError = err
					return
				}
				// Create a mock alert server to capture the alert request and respond appropriately
				alertServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					alertHit++
					receivedRequest := alert.Request{}

					rawBody, err := io.ReadAll(r.Body)
					if err != nil {
						testError = err
						return
					}

					if err := json.Unmarshal(rawBody, &receivedRequest); err != nil {
						testError = err
						return
					}

					// Assert that the requests JSON data matches the expected format
					assert.Equal(t, tc.expectedRequest, receivedRequest)

					resp := findCode("normal", serverConfig)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(resp.HttpCode)
					w.Write([]byte(resp.Json))
				}))
				defer alertServer.Close()
				l.Config.Frigate.URL = frigateServer.URL
				l.Config.Alert.URL = alertServer.URL
				go l.Listen()

				// Await the mqtt callback firing
				wg.Wait()
			}()

			// Assert the amount of times that the alert and thumbnail servers were hit by the listener
			assert.Equal(t, tc.expectedThumbnailHit, thumbnailHit)
			assert.Equal(t, tc.expectedAlertHit, alertHit)

			// Assert the error returned by the test
			if tc.expectedError != nil {
				assert.EqualError(t, testError, tc.expectedError.Error())
			} else if testError != nil {
				t.Fatalf("Unexpected error: %v", testError)
			}

		})
	}
}

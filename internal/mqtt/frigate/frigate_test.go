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

func findCode(name string, serverConfig *serverConfig) *code {
	for _, s := range serverConfig.Codes {
		if s.Name == name {
			return &s
		}
	}
	return nil
}

func mockFrigateServer(t *testing.T, thumbnailPath string) *httptest.Server {

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, thumbnailPath)

		// rawBody, err := io.ReadAll(r.Body)
		// if err != nil {
		// 	t.Fatalf("Could not read request body")
		// }

		// if err := json.Unmarshal(rawBody, &request); err != nil {
		// 	t.Fatalf("Could not parse request body")
		// }

		// resp := findCode("normal", serverConfig)
		// if request.Message == "error" {
		// 	resp = findCode("internal-error", serverConfig)
		// } else if request.Token == "error" {
		// 	resp = findCode("bad-token", serverConfig)
		// } else if request.User == "error" {
		// 	resp = findCode("bad-user", serverConfig)
		// } else if request.Priority.String() != "" {
		// 	priority, err := request.Priority.Int64()
		// 	if err != nil || priority < -2 || priority > 2 {
		// 		resp = findCode("bad-priority", serverConfig)
		// 	}
		// }
		// w.Header().Set("Content-Type", "application/json")
		// w.WriteHeader(resp.HttpCode)
		// w.Write([]byte(resp.Json))

	}))

	return server
}

func mockAlertServer(t *testing.T, serverConfigPath string) *httptest.Server {
	serverConfigFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		t.Fatalf("Could not read serverConfigPath")
	}

	serverConfig := &serverConfig{}
	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
		t.Fatalf("Could not parse serverConfigPath")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := alert.Request{}

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}

		if err := json.Unmarshal(rawBody, &request); err != nil {
			t.Fatalf("Could not parse request body")
		}

		resp := findCode("normal", serverConfig)
		// if request.Message == "error" {
		// 	resp = findCode("internal-error", serverConfig)
		// } else if request.Token == "error" {
		// 	resp = findCode("bad-token", serverConfig)
		// } else if request.User == "error" {
		// 	resp = findCode("bad-user", serverConfig)
		// } else if request.Priority.String() != "" {
		// 	priority, err := request.Priority.Int64()
		// 	if err != nil || priority < -2 || priority > 2 {
		// 		resp = findCode("bad-priority", serverConfig)
		// 	}
		// }
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.HttpCode)
		w.Write([]byte(resp.Json))

	}))

	return server
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
			// Setup wait group to wait for mqtt callback instead of immediatly returning
			var wg sync.WaitGroup
			wg.Add(1)

			var testError error
			alertHit := 0
			thumbnailHit := 0

			func() {
				defer func() {
					if r := recover(); r != nil {
						testError = errors.New("panic occurred")
					}
				}()

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
				mockClient := &mockMqttClient{
					subscribeFunc: func(client mqtt.Client, callback mqtt.MessageHandler) {
						// Mark wait group as complete
						defer wg.Done()
						callback(client, &mockMessage{
							payload: tc.mqttPayload,
						})
					},
				}

				_, ls, err := listeners(&configMap, mockClient)
				if err != nil {
					testError = err
					return
				}
				l := ls[0]

				frigateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					thumbnailHit++
					if r.URL.Path != tc.expectedThumbnailUrl {
						testError = err
						return
					}
					http.ServeFile(w, r, tc.thumbnailPath)
				}))
				defer frigateServer.Close()

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

			assert.Equal(t, tc.expectedThumbnailHit, thumbnailHit)
			assert.Equal(t, tc.expectedAlertHit, alertHit)

			if tc.expectedError != nil {
				assert.EqualError(t, testError, tc.expectedError.Error())
			} else if testError != nil {
				t.Fatalf("Unexpected error: %v", testError)
			}

		})
	}
}

// func TestHandlers(t *testing.T) {
// 	logging.SetLogLevel(logging.Error)
// 	testCases := []struct {
// 		name         string
// 		method       string
// 		url          string
// 		data         []byte
// 		serverConfig string
// 		alertConfig  string
// 		expectedCode int
// 		expectedBody string
// 	}{
// 		{
// 			name:         "no_error",
// 			method:       "POST",
// 			url:          "/alert/test1?message=test",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 200,
// 			expectedBody: `{"message":"OK"}`,
// 		},
// 		{
// 			name:         "no_error_json",
// 			method:       "POST",
// 			url:          "/alert/test2",
// 			data:         []byte(`{"message": "test"}`),
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 200,
// 			expectedBody: `{"message":"OK"}`,
// 		},
// 		{
// 			name:         "get_device_request",
// 			method:       "GET",
// 			url:          "/alert/test1",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 405,
// 			expectedBody: `{"message":"Method Not Allowed"}`,
// 		},
// 		{
// 			name:         "get_base_request",
// 			method:       "GET",
// 			url:          "/alert/",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 200,
// 			expectedBody: `{"message":"OK","data":["test1","test2"]}`,
// 		},
// 		{
// 			name:         "get_base_request_single_device",
// 			method:       "GET",
// 			url:          "/alert/",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/single_device_config.yaml",
// 			expectedCode: 404,
// 			expectedBody: "404 page not found\n",
// 		},
// 		{
// 			name:         "unsupported_base_method",
// 			method:       "POST",
// 			url:          "/alert/",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 405,
// 			expectedBody: `{"message":"Method Not Allowed"}`,
// 		},
// 		{
// 			name:         "malformed_json_body",
// 			method:       "POST",
// 			url:          "/alert/test1",
// 			data:         []byte(`not_json`),
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 400,
// 			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
// 		},
// 		{
// 			name:         "malformed_query_string",
// 			method:       "POST",
// 			url:          "/alert/test1?monkeytest",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 400,
// 			expectedBody: `{"message":"Malformed or empty query string"}`,
// 		},
// 		{
// 			name:         "missing_message_variable",
// 			method:       "POST",
// 			url:          "/alert/test1",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 400,
// 			expectedBody: `{"message":"Invalid Parameter: message"}`,
// 		},
// 		{
// 			name:         "invalid_user_variable",
// 			method:       "POST",
// 			url:          "/alert/test1?message=test&user=error",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 400,
// 			expectedBody: `{"message":"user identifier is not a valid user, group, or subscribed user key, see https://pushover.net/api#identifiers"}`,
// 		},
// 		{
// 			name:         "invalid_token_variable",
// 			method:       "POST",
// 			url:          "/alert/test1?message=test&token=error",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 400,
// 			expectedBody: `{"message":"application token is invalid, see https://pushover.net/api"}`,
// 		},
// 		{
// 			name:         "invalid_priority_variable",
// 			method:       "POST",
// 			url:          "/alert/test1?message=test&priority=3",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 400,
// 			expectedBody: `{"message":"priority is invalid, see https://pushover.net/api#priority"}`,
// 		},
// 		{
// 			name:         "internal_server_error",
// 			method:       "POST",
// 			url:          "/alert/test1?message=error",
// 			data:         nil,
// 			serverConfig: "testdata/serverConfig/normal_responses.yaml",
// 			alertConfig:  "testdata/alertConfig/normal_config.yaml",
// 			expectedCode: 500,
// 			expectedBody: `{"message":"Internal Server Error"}`,
// 		},
// 	}

// 	for _, tc := range testCases {
// 		t.Run(tc.name, func(t *testing.T) {
// 			alertConfigFile, err := os.ReadFile(tc.alertConfig)
// 			if err != nil {
// 				t.Fatalf("Could not read alert input")
// 			}

// 			alertConfig := config.Config{}

// 			if err := yaml.Unmarshal(alertConfigFile, &alertConfig); err != nil {
// 				t.Fatalf("Could not read alert input")
// 			}

// 			base, routes, err := routes(&alertConfig)

// 			if err != nil {
// 				t.Fatalf("routes returned an error: %v", err)
// 			}
// 			router := mux.NewRouter()
// 			for _, r := range routes {
// 				router.HandleFunc(r.Path, r.Handler)
// 			}

// 			server := setupHTTPServer(t, tc.serverConfig)
// 			for _, d := range base.Devices {
// 				d.Base.URL = server.URL
// 			}

// 			defer server.Close()
// 			recorder := httptest.NewRecorder()

// 			request := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.data))
// 			if tc.data != nil {
// 				request.Header.Set("Content-Type", "application/json")
// 			}

// 			router.ServeHTTP(recorder, request)

// 			if recorder.Code != tc.expectedCode {
// 				t.Fatalf("Unexpected HTTP status code. Expected: %d, Got: %d", tc.expectedCode, recorder.Code)
// 			}

// 			if recorder.Body.String() != tc.expectedBody {
// 				t.Fatalf("Unexpected response body. Expected: %s, Got: %s", tc.expectedBody, recorder.Body.String())
// 			}
// 		})
// 	}
// }

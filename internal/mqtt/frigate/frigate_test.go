package frigate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	alert "github.com/kennedn/restate-go/internal/device/alert/common"
	mockMqtt "github.com/kennedn/restate-go/internal/mqtt/frigate/mock"

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

type ListenTestCase struct {
	name                 string
	configPath           string
	thumbnailPath        string
	clipPath             string
	mqttPayload          []byte
	expectedBaseUrl      string
	expectedRequest      alert.Request
	expectedError        error
	expectedAlertHit     int
	expectedThumbnailHit int
}

func findCode(name string, serverConfig *serverConfig) *code {
	for _, s := range serverConfig.Codes {
		if s.Name == name {
			return &s
		}
	}
	return nil
}

func mockFrigateServer(w http.ResponseWriter, r *http.Request, tc *ListenTestCase) error {
	if r.URL.Path != "/api/events" && !strings.HasPrefix(r.URL.Path, tc.expectedBaseUrl) {
		return errors.New("unexpected base URL")
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/events/") && strings.HasSuffix(r.URL.Path, "/clip.mp4"):
		// Serve dummy clip.mp4
		http.ServeFile(w, r, tc.clipPath)
	case strings.HasPrefix(r.URL.Path, "/api/events/") && strings.HasSuffix(r.URL.Path, "/thumbnail.jpg"):
		// Serve dummy thumbnail.jpg
		http.ServeFile(w, r, tc.thumbnailPath)
	case strings.HasPrefix(r.URL.Path, "/api/events/") && len(strings.Split(r.URL.Path, "/")) == 4:
		// Serve dummy event JSON
		eventUID := strings.TrimPrefix(r.URL.Path, "/api/events/")
		dummyEvent := map[string]interface{}{
			"id":        eventUID,
			"startTime": 1234567890,
			"endTime":   1234567890,
			"camera":    "test_camera",
			"zones":     []string{"zone1", "zone2"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dummyEvent)
	case r.URL.Path == "/api/events":
		// Serve dummy events list JSON
		dummyEvents := []map[string]interface{}{
			{
				"id":        "event1",
				"startTime": 1234567890,
				"endTime":   1234567890,
				"camera":    "test_camera",
				"zones":     []string{"zone1", "zone2"},
			},
			{
				"id":        "event2",
				"startTime": 1234567890,
				"endTime":   1234567890,
				"camera":    "test_camera",
				"zones":     []string{"zone1", "zone2"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dummyEvents)
	default:
		http.NotFound(w, r)
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
			configPath:    "testdata/frigateConfig/normal_config.yaml",
			listenerCount: 2,
			expectedError: nil,
		},
		{
			name:          "empty_yaml_config",
			configPath:    "testdata/frigateConfig/empty_yaml_config.yaml",
			listenerCount: 0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config",
			configPath:    "testdata/frigateConfig/missing_config.yaml",
			listenerCount: 0,
			expectedError: errors.New(""),
		},
		{
			name:          "missing_config_parameter",
			configPath:    "testdata/frigateConfig/missing_config_parameter.yaml",
			listenerCount: 0,
			expectedError: errors.New(""),
		},
		{
			name:          "single_device_config",
			configPath:    "testdata/frigateConfig/single_device_config.yaml",
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

			_, l, err := listeners(&configMap, &mockMqtt.Client{})

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(l) != tc.listenerCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.listenerCount, len(l))
			}

		})
	}
}

func TestListen(t *testing.T) {
	logging.SetLogLevel(logging.Error)
	testCases := []ListenTestCase{
		{
			name:            "no_error",
			configPath:      "testdata/frigateConfig/single_device_config.yaml",
			thumbnailPath:   "testdata/frigateConfig/thumbnail.jpg",
			clipPath:        "testdata/frigateConfig/clip.mp4",
			mqttPayload:     []byte(`{"type":"new","before":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"},"after":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedBaseUrl: "/api/events/1723938588.335444-ctmuov",
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
			configPath:           "testdata/frigateConfig/no_device_config.yaml",
			thumbnailPath:        "testdata/frigateConfig/thumbnail.jpg",
			clipPath:             "testdata/frigateConfig/clip.mp4",
			mqttPayload:          []byte(`{"type":"new","before":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"},"after":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedRequest:      alert.Request{},
			expectedBaseUrl:      "/api/events/1723938588.335444-ctmuov",
			expectedError:        errors.New("no listeners found in config"),
			expectedAlertHit:     0,
			expectedThumbnailHit: 0,
		},
		{
			name:            "frigate_server_404",
			configPath:      "testdata/frigateConfig/single_device_config.yaml",
			thumbnailPath:   "testdata/serverConfig/does_not_exist.jpg",
			clipPath:        "testdata/frigateConfig/clip.mp4",
			mqttPayload:     []byte(`{"type":"new","before":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"},"after":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-ctmuov"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedBaseUrl: "/api/events/1723938588.335444-ctmuov",
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
			configPath:           "testdata/frigateConfig/single_device_config.yaml",
			thumbnailPath:        "testdata/frigateConfig/thumbnail.jpg",
			clipPath:             "testdata/frigateConfig/clip.mp4",
			mqttPayload:          []byte(`{"type":"new",mb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedBaseUrl:      "/api/events/1723938588.335444-ctmuov",
			expectedRequest:      alert.Request{},
			expectedError:        nil,
			expectedAlertHit:     0,
			expectedThumbnailHit: 0,
		},
		{
			name:            "end_event",
			configPath:      "testdata/frigateConfig/single_device_config.yaml",
			thumbnailPath:   "testdata/frigateConfig/thumbnail.jpg",
			clipPath:        "testdata/frigateConfig/clip.mp4",
			mqttPayload:     []byte(`{"type":"end","before":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-endevent"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"},"after":{"camera":"front_garden","data":{"audio":[],"detections":["1723938588.335444-endevent"],"objects":["person"],"sub_labels":[],"zones":["front_enterance"]},"end_time":1723938593.734983,"id":"1723938590.336533-y0wa6z","severity":"alert","start_time":1723938590.336533,"thumb_path":"/media/frigate/clips/review/thumb-front_garden-1723938590.336533-y0wa6z.webp"}}`),
			expectedBaseUrl: "/api/events/1723938588.335444-",
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
			expectedAlertHit:     0,
			expectedThumbnailHit: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup wait group to wait for mqtt callback instead of immediately returning
			var wg sync.WaitGroup
			wg.Add(1)
			// Make a channel to allow testing for stalled wg
			done := make(chan struct{})

			var testError error
			alertHit := 0
			thumbnailHit := 0

			// Create a temporary directory
			cacheDir, err := os.MkdirTemp("", "cache")
			if err != nil {
				t.Fatalf("Failed to create temporary directory: %v", err)
			}

			// Run test in an anonymous function to capture error for assertion
			func() {
				// Capture error in the case of a panic
				defer func() {
					if r := recover(); r != nil {
						testError = errors.New("panic occurred")
					}
					os.RemoveAll(cacheDir)
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
				mockClient := &mockMqtt.Client{
					SubscribeFunc: func(client mqtt.Client, callback mqtt.MessageHandler) {
						// Mark wait group as complete
						defer wg.Done()
						callback(client, &mockMqtt.Message{
							PayloadVar: tc.mqttPayload,
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
					testError = mockFrigateServer(w, r, &tc)
					if testError != nil {
						return
					}
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
				l.Config.Frigate.CachePath = cacheDir
				go l.connectionCallback(mockClient)

				// Await the mqtt callback firing
				go func() {
					defer close(done)
					wg.Wait()
				}()

				select {
				case <-done:
					fmt.Println("WaitGroup completed successfully")
				case <-time.After(1 * time.Second): // Timeout duration
					t.Fatalf("%s didn't finish in time", tc.name)
				}

				// end events should end up with 1 file after downloadEvent is called and 0 after removeOldClips is called
				files, err := os.ReadDir(cacheDir)
				if err != nil {
					testError = fmt.Errorf("failed to read directory: %w", err)
					return
				}
				assert.Equal(t, len(files), 0)
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

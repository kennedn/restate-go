package msh300hk

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
	normalRadiatorConfig = `apiVersion: v2
devices:
- type: meross
  config:
    name: rad1
    deviceType: radiator
    timeoutMs: 500
    host: "127.0.0.1"
    ids: ["dev1","dev2"]
- type: meross
  config:
    name: rad2
    deviceType: radiator
    timeoutMs: 500
    host: "127.0.0.2"
    ids: ["dev3"]`
	missingRadiatorConfig = `apiVersion: v2
devices:
- type: meross
  config:`
	emptyInternalConfig   = ``
	nonYamlInternalConfig = `not_yaml`
)

func bytesPtr(b []byte) *[]byte {
	return &b
}

func setupRadiatorServer(t *testing.T) *httptest.Server {
	t.Helper()

	response := `{"payload":{"all":[{"id":"dev1","scheduleBMode":0,"online":{"status":1,"lastActiveTime":0},"togglex":{"onoff":1},"timeSync":{"state":0},"mode":{"state":2},"temperature":{"room":200,"currentSet":220,"heating":1,"openWindow":0}}],"battery":[{"id":"dev1","value":95}]}}`

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}

		payload := struct {
			Header struct {
				Method string `json:"method"`
			} `json:"header"`
		}{}

		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("Could not parse request body")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if payload.Header.Method == "GET" {
			w.Write([]byte(response))
			return
		}

		w.Write([]byte("{}"))
	}))
}

func TestRoutes(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	tests := []struct {
		name           string
		cfg            string
		internalConfig *[]byte
		expectedRoutes int
		expectedError  error
	}{
		{
			name:           "default_config",
			cfg:            normalRadiatorConfig,
			internalConfig: nil,
			expectedRoutes: 7,
			expectedError:  nil,
		},
		{
			name:           "empty_internal_config",
			cfg:            normalRadiatorConfig,
			internalConfig: bytesPtr([]byte(emptyInternalConfig)),
			expectedRoutes: 0,
			expectedError:  errors.New(""),
		},
		{
			name:           "non_yaml_internal_config",
			cfg:            normalRadiatorConfig,
			internalConfig: bytesPtr([]byte(nonYamlInternalConfig)),
			expectedRoutes: 0,
			expectedError:  &yaml.TypeError{},
		},
		{
			name:           "missing_device_config",
			cfg:            missingRadiatorConfig,
			internalConfig: nil,
			expectedRoutes: 0,
			expectedError:  errors.New(""),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{}
			if err := yaml.Unmarshal([]byte(tc.cfg), &cfg); err != nil {
				t.Fatalf("Could not unmarshal meross config")
			}

			_, r, err := routes(&cfg, tc.internalConfig)
			assert.IsType(t, tc.expectedError, err)

			if len(r) != tc.expectedRoutes {
				t.Fatalf("Unexpected number of routes. Expected %d got %d", tc.expectedRoutes, len(r))
			}
		})
	}
}

func TestHandlers(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	cfg := config.Config{}
	if err := yaml.Unmarshal([]byte(normalRadiatorConfig), &cfg); err != nil {
		t.Fatalf("Could not unmarshal meross config")
	}

	base, routes, err := routes(&cfg, nil)
	if err != nil {
		t.Fatalf("routes returned an error: %v", err)
	}

	router := mux.NewRouter()
	for _, r := range routes {
		router.HandleFunc(r.Path, r.Handler)
	}

	server := setupRadiatorServer(t)
	for i := range base.Devices {
		base.Devices[i].Host = strings.TrimPrefix(server.URL, "http://")
	}
	defer server.Close()

	tests := []struct {
		name         string
		method       string
		url          string
		data         []byte
		expectedCode int
		expectedBody string
	}{
		{
			name:         "base_get_devices",
			method:       http.MethodGet,
			url:          "/radiator/",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":["dev1","dev2","dev3","rad1","rad2"]}`,
		},
		{
			name:         "single_get_codes",
			method:       http.MethodGet,
			url:          "/radiator/rad1",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":["toggle","mode","adjust","status","battery"]}`,
		},
		{
			name:         "single_invalid_method",
			method:       http.MethodDelete,
			url:          "/radiator/rad1",
			data:         nil,
			expectedCode: http.StatusMethodNotAllowed,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "multi_invalid_hosts",
			method:       http.MethodPost,
			url:          "/radiator/?code=status",
			data:         nil,
			expectedCode: http.StatusBadRequest,
			expectedBody: `{"message":"Invalid Parameter: hosts"}`,
		},
		{
			name:         "status_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=status",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"id":"dev1","onoff":1,"mode":2,"online":1,"temperature":{"current":200,"target":220,"heating":true,"openWindow":false}}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.data))
			if tc.data != nil {
				req.Header.Set("Content-Type", "application/json")
			}

			router.ServeHTTP(recorder, req)

			assert.Equal(t, tc.expectedCode, recorder.Code)

			if tc.name == "base_get_devices" {
				response := struct {
					Data []string `json:"data"`
				}{}
				assert.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				assert.ElementsMatch(t, []string{"rad1", "rad2", "dev1", "dev2", "dev3"}, response.Data)
				return
			}

			assert.Equal(t, tc.expectedBody, recorder.Body.String())
		})
	}
}

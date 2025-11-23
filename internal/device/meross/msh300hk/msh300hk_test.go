package msh300hk

import (
	"bytes"
	_ "embed"
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

//go:embed testdata/merossConfig/normal_config.yaml
var normalRadiatorConfig string

//go:embed testdata/merossConfig/missing_config.yaml
var missingRadiatorConfig string

//go:embed testdata/baseConfig/empty.yaml
var emptyInternalConfig string

//go:embed testdata/baseConfig/non_yaml_config.yaml
var nonYamlInternalConfig string

//go:embed testdata/serverResponse/single_status.json
var singleStatusResponse string

//go:embed testdata/serverResponse/multi_status.json
var multiStatusResponse string

func bytesPtr(b []byte) *[]byte {
	return &b
}

func setupRadiatorServer(t *testing.T) *httptest.Server {
	t.Helper()

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
			if r.URL.Query().Get("hosts") != "" || bytes.Contains(body, []byte("dev3")) {
				w.Write([]byte(multiStatusResponse))
				return
			}
			w.Write([]byte(singleStatusResponse))
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
			name:         "get_codes",
			method:       http.MethodGet,
			url:          "/radiator/rad1",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":["toggle","mode","adjust","status","battery"]}`,
		},
		{
			name:         "invalid_method",
			method:       http.MethodDelete,
			url:          "/radiator/rad1",
			data:         nil,
			expectedCode: http.StatusMethodNotAllowed,
			expectedBody: `{"message":"Method Not Allowed"}`,
		},
		{
			name:         "toggle_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=toggle",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_toggle_success",
			method:       http.MethodPost,
			url:          "/radiator?hosts=rad1,rad2&code=toggle",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "toggle_with_value_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=toggle&value=1",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "mode_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=mode",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"id":"dev1","value":3}]}`,
		},
		{
			name:         "multi_mode_success",
			method:       http.MethodPost,
			url:          "/radiator?hosts=rad1,rad2&code=mode",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"name":"rad1","status":{"id":"dev1","value":3}},{"name":"rad1","status":{"id":"dev2","value":3}},{"name":"rad2","status":{"id":"dev3","value":3}}]}`,
		},
		{
			name:         "mode_with_value_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=mode&value=1",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "adjust_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=adjust",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"id":"dev1","value":70}]}`,
		},
		{
			name:         "multi_adjust_get_success",
			method:       http.MethodPost,
			url:          "/radiator",
			data:         []byte(`{"code":"adjust","hosts":"rad1,rad2"}`),
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"name":"rad1","status":{"id":"dev1","value":270}},{"name":"rad1","status":{"id":"dev2","value":330}},{"name":"rad2","status":{"id":"dev3","value":220}}]}`,
		},
		{
			name:         "multi_adjust_success",
			method:       http.MethodPost,
			url:          "/radiator",
			data:         []byte(`{"code":"adjust","hosts":"rad1,rad2","value":210}`),
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK"}`,
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
			name:         "multi_status_success",
			method:       http.MethodPost,
			url:          "/radiator/?code=status&hosts=rad1,rad2",
			data:         []byte(`{"code":"status","hosts":"rad1,rad2"}`),
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"name":"rad1","status":{"id":"dev1","onoff":1,"mode":3,"online":1,"temperature":{"current":200,"target":220,"heating":true,"openWindow":false}}},{"name":"rad1","status":{"id":"dev2","onoff":1,"mode":3,"online":1,"temperature":{"current":200,"target":220,"heating":true,"openWindow":false}}},{"name":"rad2","status":{"id":"dev3","onoff":0,"mode":1,"online":1,"temperature":{"current":190,"target":190,"heating":false,"openWindow":true}}}]}`,
		},
		{
			name:         "status_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=status",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"id":"dev1","onoff":1,"mode":3,"online":1,"temperature":{"current":200,"target":220,"heating":true,"openWindow":false}}]}`,
		},
		{
			name:         "battery_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=battery",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"id":"dev1","value":95}]}`,
		},
		{
			name:         "multi_battery_success",
			method:       http.MethodPost,
			url:          "/radiator?hosts=rad1,rad2&code=battery",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"name":"rad1","status":{"id":"dev1","value":95}},{"name":"rad1","status":{"id":"dev2","value":85}},{"name":"rad2","status":{"id":"dev3","value":80}}]}`,
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

package msh300hk

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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

	// Create a temporary file for testing
	tmpFile, err := os.CreateTemp("", "heating-overrides-*.json")
	if err != nil {
		t.Fatalf("Failed to create temporary file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Override the heating overrides path for testing
	originalPath := heatingOverridesPath
	heatingOverridesPath = tmpFile.Name()
	t.Cleanup(func() {
		heatingOverridesPath = originalPath
	})

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
	if len(base.Devices) > 1 && len(base.Devices[1].Schedules) > 0 {
		base.Devices[1].Schedules = base.Devices[1].Schedules[:1]
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
			expectedBody: `{"message":"OK","data":["rad1","rad2","dev1","dev2","dev3"]}`,
		},
		{
			name:         "get_codes",
			method:       http.MethodGet,
			url:          "/radiator/rad1",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":["toggle","mode","adjust","boost","schedule","status","battery","heatTemp"]}`,
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
			name:         "schedule_get_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=schedule",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":["default","study"]}`,
		},
		{
			name:         "multi_schedule_get_success",
			method:       http.MethodPost,
			url:          "/radiator?hosts=rad1,rad2&code=schedule",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[]}`,
		},
		{
			name:         "schedule_set_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=schedule&value=study",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_schedule_set_success",
			method:       http.MethodPost,
			url:          "/radiator?code=schedule&hosts=rad1&value=default",
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
			name:         "boost_success",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=boost&value=2",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK"}`,
		},
		{
			name:         "multi_boost_success",
			method:       http.MethodPost,
			url:          "/radiator?hosts=rad1,rad2&code=boost&value=2",
			data:         nil,
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
		{
			name:         "status_no_boost_omitempty",
			method:       http.MethodPost,
			url:          "/radiator/rad1?code=status",
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"OK","data":[{"id":"dev1","onoff":1,"mode":3,"online":1,"temperature":{"current":200,"target":220,"heating":true,"openWindow":false}}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Clear the heating overrides file between tests to avoid cross-test pollution
			os.WriteFile(tmpFile.Name(), []byte{}, 0644)

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

			if tc.name == "boost_success" || tc.name == "multi_boost_success" {
				raw, err := os.ReadFile(tmpFile.Name())
				assert.NoError(t, err)

				response := struct {
					Boost map[string]string `json:"boost"`
				}{}
				assert.NoError(t, json.Unmarshal(raw, &response))
				if tc.name == "boost_success" {
					if assert.Len(t, response.Boost, 2) {
						for _, id := range []string{"dev1", "dev2"} {
							expiresStr, ok := response.Boost[id]
							if assert.True(t, ok, "missing override for %s", id) {
								expires, err := time.Parse(time.RFC3339, expiresStr)
								assert.NoError(t, err)
								assert.WithinDuration(t, time.Now().Add(2*time.Hour), expires, time.Minute)
							}
						}
					}
				} else {
					if assert.Len(t, response.Boost, 3) {
						for _, id := range []string{"dev1", "dev2", "dev3"} {
							expiresStr, ok := response.Boost[id]
							if assert.True(t, ok, "missing override for %s", id) {
								expires, err := time.Parse(time.RFC3339, expiresStr)
								assert.NoError(t, err)
								assert.WithinDuration(t, time.Now().Add(2*time.Hour), expires, time.Minute)
							}
						}
					}
				}
			}

			assert.Equal(t, tc.expectedBody, recorder.Body.String())
		})
	}

}

func TestSchedulePayloadFormat(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	// Create a temporary file for testing
	tmpFile, err := os.CreateTemp("", "heating-overrides-*.json")
	if err != nil {
		t.Fatalf("Failed to create temporary file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Override the heating overrides path for testing
	originalPath := heatingOverridesPath
	heatingOverridesPath = tmpFile.Name()
	t.Cleanup(func() {
		heatingOverridesPath = originalPath
	})

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

	// Capture the request body sent to the mock server
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	for i := range base.Devices {
		base.Devices[i].Host = strings.TrimPrefix(server.URL, "http://")
	}

	// Make request to set schedule
	req := httptest.NewRequest(http.MethodPost, "/radiator/rad1?code=schedule&value=study", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	// Verify response is OK
	assert.Equal(t, http.StatusOK, recorder.Code)

	// Parse the captured payload
	fullPayload := struct {
		Payload struct {
			Schedule []map[string]any `json:"schedule"`
		} `json:"payload"`
	}{}

	if err := json.Unmarshal(capturedBody, &fullPayload); err != nil {
		t.Fatalf("Could not parse captured payload: %v\nPayload: %s", err, string(capturedBody))
	}

	// Verify the schedule array is not nested
	assert.True(t, len(fullPayload.Payload.Schedule) > 0, "schedule should have at least one item")

	// Verify the schedule item has the expected fields (not double-wrapped)
	scheduleItem := fullPayload.Payload.Schedule[0]
	assert.NotNil(t, scheduleItem["id"], "schedule item should have id field")
	assert.NotNil(t, scheduleItem["mon"], "schedule item should have mon field")

	// Verify the structure is an array of objects, not array of arrays
	assert.IsType(t, []any(nil), scheduleItem["mon"], "mon field should be an array")

}

func TestMultiSchedulePerDevicePayload(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	// Create a temporary file for testing
	tmpFile, err := os.CreateTemp("", "heating-overrides-*.json")
	if err != nil {
		t.Fatalf("Failed to create temporary file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Override the heating overrides path for testing
	originalPath := heatingOverridesPath
	heatingOverridesPath = tmpFile.Name()
	t.Cleanup(func() {
		heatingOverridesPath = originalPath
	})

	cfg := config.Config{}
	if err := yaml.Unmarshal([]byte(normalRadiatorConfig), &cfg); err != nil {
		t.Fatalf("Could not unmarshal meross config")
	}

	base, routes, err := routes(&cfg, nil)
	if err != nil {
		t.Fatalf("routes returned an error: %v", err)
	}

	// Modify schedules so that devices share the same schedule name but different contents
	// rad1 (dev1,dev2) -> study with mon [[1,1]]
	// rad2 (dev3) -> study with mon [[2,2]]
	for _, d := range base.Devices {
		if d.Name == "rad1" {
			d.Schedules = []schedulePreset{{Name: "study", Mon: [][]int64{{1, 1}}}}
		}
		if d.Name == "rad2" {
			d.Schedules = []schedulePreset{{Name: "study", Mon: [][]int64{{2, 2}}}}
		}
	}

	router := mux.NewRouter()
	for _, r := range routes {
		router.HandleFunc(r.Path, r.Handler)
	}

	// Capture the request body sent to the mock server
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	for i := range base.Devices {
		base.Devices[i].Host = strings.TrimPrefix(server.URL, "http://")
	}

	// Make request to set schedule across rad1 and rad2
	req := httptest.NewRequest(http.MethodPost, "/radiator?hosts=rad1,rad2&code=schedule&value=study", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	// Verify response is OK
	assert.Equal(t, http.StatusOK, recorder.Code)

	// Parse the captured payload
	fullPayload := struct {
		Payload struct {
			Schedule []map[string]any `json:"schedule"`
		} `json:"payload"`
	}{}

	if err := json.Unmarshal(capturedBody, &fullPayload); err != nil {
		t.Fatalf("Could not parse captured payload: %v\nPayload: %s", err, string(capturedBody))
	}

	// Expect three entries: dev1, dev2 (from rad1) and dev3 (from rad2)
	if assert.Len(t, fullPayload.Payload.Schedule, 3) {
		byId := map[string]map[string]any{}
		for _, item := range fullPayload.Payload.Schedule {
			id, _ := item["id"].(string)
			byId[id] = item
		}

		// dev1 and dev2 should have mon [[1,1]]
		for _, id := range []string{"dev1", "dev2"} {
			it, ok := byId[id]
			if assert.True(t, ok, "missing id %s in payload", id) {
				mon, ok := it["mon"].([]any)
				if assert.True(t, ok, "mon field type for %s", id) {
					// check first inner pair
					firstPair := mon[0].([]any)
					assert.Equal(t, float64(1), firstPair[0])
					assert.Equal(t, float64(1), firstPair[1])
				}
			}
		}

		// dev3 should have mon [[2,2]]
		if it, ok := byId["dev3"]; assert.True(t, ok, "missing id dev3 in payload") {
			mon, ok := it["mon"].([]any)
			if assert.True(t, ok, "mon field type for dev3") {
				firstPair := mon[0].([]any)
				assert.Equal(t, float64(2), firstPair[0])
				assert.Equal(t, float64(2), firstPair[1])
			}
		}
	}
}

func TestStatusIncludesScheduleSingle(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	cfg := config.Config{}
	if err := yaml.Unmarshal([]byte(normalRadiatorConfig), &cfg); err != nil {
		t.Fatalf("Could not unmarshal meross config")
	}

	base, routes, err := routes(&cfg, nil)
	if err != nil {
		t.Fatalf("routes returned an error: %v", err)
	}

	// Build a schedule response that matches the 'study' preset for dev1
	scheduleResp := map[string]any{
		"header": map[string]any{"messageId": "x", "namespace": "Appliance.Hub.Mts100.ScheduleB", "method": "GETACK"},
		"payload": map[string]any{"schedule": []any{
			map[string]any{
				"id":  "dev1",
				"mon": [][]int{{420, 100}, {180, 100}, {180, 100}, {300, 100}, {240, 100}, {120, 100}},
				"tue": [][]int{{420, 100}, {180, 100}, {180, 100}, {300, 100}, {240, 100}, {120, 100}},
				"wed": [][]int{{420, 100}, {180, 100}, {180, 100}, {300, 100}, {240, 100}, {120, 100}},
				"thu": [][]int{{420, 100}, {180, 100}, {180, 100}, {300, 100}, {240, 100}, {120, 100}},
				"fri": [][]int{{420, 100}, {180, 100}, {180, 100}, {300, 100}, {240, 100}, {120, 100}},
				"sat": [][]int{{540, 100}, {60, 100}, {180, 100}, {300, 100}, {300, 100}, {60, 100}},
				"sun": [][]int{{540, 100}, {60, 100}, {180, 100}, {300, 100}, {300, 100}, {60, 100}},
			},
		}},
	}

	// Start server that returns schedule for schedule namespace, otherwise status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		header, _ := parsed["header"].(map[string]any)
		ns, _ := header["namespace"].(string)
		if ns == "Appliance.Hub.Mts100.ScheduleB" {
			b, _ := json.Marshal(scheduleResp)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(b)
			return
		}
		// default to single status response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(singleStatusResponse))
	}))
	defer server.Close()

	router := mux.NewRouter()
	for _, r := range routes {
		router.HandleFunc(r.Path, r.Handler)
	}

	// point devices at test server
	for i := range base.Devices {
		base.Devices[i].Host = strings.TrimPrefix(server.URL, "http://")
	}

	// Call status for rad1
	req := httptest.NewRequest(http.MethodPost, "/radiator/rad1?code=status", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)

	resp := struct {
		Message string           `json:"message"`
		Data    []map[string]any `json:"data"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("could not parse response: %v", err)
	}
	if assert.Len(t, resp.Data, 1) {
		sch, ok := resp.Data[0]["schedule"].(string)
		if assert.True(t, ok, "schedule field should be present and a string") {
			assert.Equal(t, "study", sch)
		}
	}
}

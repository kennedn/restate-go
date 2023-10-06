package meross

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	device "restate-go/internal/device/common"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func setupHTTPServer(t *testing.T, serverConfigPath string) *httptest.Server {
	serverConfigFile, err := os.ReadFile(serverConfigPath)
	if err != nil {
		t.Fatalf("Could not read serverConfigPath")
	}

	serverConfig := struct {
		Get struct {
			Code int    `yaml:"code"`
			JSON string `yaml:"json"`
		} `yaml:"get"`
		Set struct {
			Code int `yaml:"code"`
		} `yaml:"set"`
	}{}

	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
		t.Fatalf("Could not parse serverConfigPath")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}

		body := struct {
			Header struct {
				Method string `json:"method"`
			} `json:"header"`
		}{}

		if err := json.Unmarshal(rawBody, &body); err != nil {
			t.Fatalf("Could not parse request body")
		}

		switch body.Header.Method {
		case "GET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(serverConfig.Get.Code)
			w.Write([]byte(serverConfig.Get.JSON))
		case "SET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(serverConfig.Set.Code)
			w.Write([]byte(""))
		}

	}))

	return server
}

func TestRoutes(t *testing.T) {
	testCases := []struct {
		name               string
		configPath         string
		internalConfigPath string
		routeCount         int
		expectedError      error
	}{
		{
			name:               "default_config",
			configPath:         "testdata/merossConfig/normal_input.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         4,
			expectedError:      nil,
		},
		{
			name:               "base_bad_path",
			configPath:         "testdata/merossConfig/normal_input.yaml",
			internalConfigPath: "non/existant/file",
			routeCount:         0,
			expectedError:      &fs.PathError{},
		},
		{
			name:               "base_0_endpoints",
			configPath:         "testdata/merossConfig/normal_input.yaml",
			internalConfigPath: "testdata/baseConfig/0_endpoints.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "base_non_yaml_config",
			configPath:         "testdata/merossConfig/normal_input.yaml",
			internalConfigPath: "testdata/baseConfig/non_yaml_config.yaml",
			routeCount:         0,
			expectedError:      &yaml.TypeError{},
		},
		{
			name:               "base_empty_yaml_config",
			configPath:         "testdata/merossConfig/normal_input.yaml",
			internalConfigPath: "testdata/baseConfig/empty_yaml_config.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "meross_empty_yaml_config",
			configPath:         "testdata/merossConfig/empty_yaml_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      nil,
		},
		{
			name:               "meross_empty_yaml_config",
			configPath:         "testdata/merossConfig/empty_yaml_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      nil,
		},
		{
			name:               "meross_missing_config",
			configPath:         "testdata/merossConfig/missing_config.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "meross_missing_config_parameter",
			configPath:         "testdata/merossConfig/missing_config_parameter.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         0,
			expectedError:      errors.New(""),
		},
		{
			name:               "meross_unknown_deviceType",
			configPath:         "testdata/merossConfig/unknown_deviceType.yaml",
			internalConfigPath: "device.yaml",
			routeCount:         4,
			expectedError:      nil,
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
			r, err := Routes(&merossConfig, tc.internalConfigPath)

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(r) != tc.routeCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.routeCount, len(r))
			}

		})
	}
}

// func TestHandler(t *testing.T) {
// 	testCases := []struct {
// 		name         string
// 		method       string
// 		url          string
// 		data         []byte
// 		expectedCode int
// 		expectedBody string
// 	}{
// 		{
// 			name:         "GET_/tvcom",
// 			method:       "GET",
// 			url:          "/tvcom",
// 			data:         nil,
// 			expectedCode: 200,
// 			expectedBody: `{"message":"OK","data":["abnormal_state","add_skip","aspect_ratio","auto_configure_vga","backlight_lcd","balance","brightness","colour","colour_temp","contrast","input_select","ir_key","ism_method_plasma","osd_select","power","power_saving_plasma","remote_lock","screen_mute","sharpness","tint","volume","volume_mute"]}`,
// 		},
// 	}

// func TestTvcomHandler(t *testing.T) {
// 	testCases := []struct {
// 		name         string
// 		method       string
// 		url          string
// 		data         []byte
// 		expectedCode int
// 		expectedBody string
// 	}{
// 		{
// 			name:         "GET_/tvcom",
// 			method:       "GET",
// 			url:          "/tvcom",
// 			data:         nil,
// 			expectedCode: 200,
// 			expectedBody: `{"message":"OK","data":["abnormal_state","add_skip","aspect_ratio","auto_configure_vga","backlight_lcd","balance","brightness","colour","colour_temp","contrast","input_select","ir_key","ism_method_plasma","osd_select","power","power_saving_plasma","remote_lock","screen_mute","sharpness","tint","volume","volume_mute"]}`,
// 		},
// 		{
// 			name:         "GET_/tvcom/",
// 			method:       "GET",
// 			url:          "/tvcom/",
// 			data:         nil,
// 			expectedCode: 200,
// 			expectedBody: `{"message":"OK","data":["abnormal_state","add_skip","aspect_ratio","auto_configure_vga","backlight_lcd","balance","brightness","colour","colour_temp","contrast","input_select","ir_key","ism_method_plasma","osd_select","power","power_saving_plasma","remote_lock","screen_mute","sharpness","tint","volume","volume_mute"]}`,
// 		},
// 		{
// 			name:         "POST_/tvcom",
// 			method:       "POST",
// 			url:          "/tvcom",
// 			data:         nil,
// 			expectedCode: 405,
// 			expectedBody: `{"message":"Method Not Allowed"}`,
// 		},
// 	}

// 	routes, err := Routes(500, "ws://test.com", "device.yaml")
// 	if err != nil {
// 		t.Fatalf("Routes returned an error: %v", err)
// 	}
// 	router := mux.NewRouter()
// 	for _, r := range routes {
// 		router.HandleFunc(r.Path, r.Handler)
// 	}

// 	for _, tc := range testCases {
// 		t.Run(tc.name, func(t *testing.T) {
// 			recorder := httptest.NewRecorder()

// 			request := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.data))

// 			router.ServeHTTP(recorder, request)

// 			if recorder.Code != tc.expectedCode {
// 				t.Errorf("Unexpected HTTP status code. Expected: %d, Got: %d", tc.expectedCode, recorder.Code)
// 			}

// 			if recorder.Body.String() != tc.expectedBody {
// 				t.Errorf("Unexpected response body. Expected: %s, Got: %s", tc.expectedBody, recorder.Body.String())
// 			}
// 		})
// 	}
// }

func TestMerossHandler(t *testing.T) {
	testCases := []struct {
		method       string
		url          string
		data         []byte
		serverConfig string
		expectedCode int
		expectedBody string
	}{
		{
			method:       "POST",
			url:          "/meross/office?code=status",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_bulb.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":{"onoff":1,"rgb":255,"temperature":1,"luminance":-1}}`,
		},
		{
			method:       "POST",
			url:          "/meross/office?code=toggle",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_bulb.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			method:       "POST",
			url:          "/meross/office?code=luminance&value=1",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_bulb.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			method:       "POST",
			url:          "/meross/office?code=temperature&value=1",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_bulb.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			method:       "POST",
			url:          "/meross/office?code=fade",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_bulb.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK"}`,
		},
		{
			method:       "GET",
			url:          "/meross/office",
			data:         nil,
			serverConfig: "testdata/serverConfig/normal_bulb.yaml",
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["toggle","status","luminance","temperature","rgb","fade"]}`,
		},
	}

	merossConfigFile, err := os.ReadFile("testdata/merossConfig/normal_input.yaml")
	if err != nil {
		t.Fatalf("Could not read meross input")
	}

	merossConfig := device.Config{}

	if err := yaml.Unmarshal(merossConfigFile, &merossConfig); err != nil {
		t.Fatalf("Could not read meross input")
	}

	base, routes, err := generateRoutesFromConfig(&merossConfig, "device.yaml")
	if err != nil {
		t.Fatalf("generateRoutesFromConfig returned an error: %v", err)
	}
	router := mux.NewRouter()
	for _, r := range routes {
		router.HandleFunc(r.Path, r.Handler)
	}

	for _, tc := range testCases {
		t.Run(tc.method+tc.url, func(t *testing.T) {
			server := setupHTTPServer(t, tc.serverConfig)
			for i, _ := range base.Devices {
				base.Devices[i].Host = strings.TrimPrefix(server.URL, "http://")
			}
			defer server.Close()
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

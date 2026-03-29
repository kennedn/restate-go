package bins

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	config "github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

type upstreamPayload struct {
	FormValues map[string]map[string]map[string]string `json:"formValues"`
}

func setupHTTPServer(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/service/Bin_Collection_Dates" {
			http.SetCookie(w, &http.Cookie{
				Name:  "PHPSESSID",
				Value: "test-session",
				Path:  "/",
			})
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		cookie, err := r.Cookie("PHPSESSID")
		if err != nil || cookie.Value == "" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"You have been logged out"}`))
			return
		}

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Could not read request body")
		}

		request := upstreamPayload{}
		if err := json.Unmarshal(rawBody, &request); err != nil {
			t.Fatalf("Could not parse request body")
		}

		switch r.URL.Path {
		case "/uprn":
			postcode := request.FormValues["Section 1"]["postcode"]["value"]

			if postcode == "SERVER ERROR" {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message":"error"}`))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			if postcode == "AB1 2CD" {
				w.Write([]byte(`{
					"integration": {
						"transformed": {
							"select_data": [
								{
									"label": "42 EXAMPLE STREET TESTTOWN AB1 2CD",
									"value": "100000001"
								},
								{
									"label": "44 EXAMPLE STREET TESTTOWN AB1 2CD",
									"value": "100000002"
								}
							]
						}
					}
				}`))
				return
			}

			w.Write([]byte(`{
				"integration": {
					"transformed": {
						"select_data": []
					}
				}
			}`))
			return

		case "/lookup":
			uprn := request.FormValues["Section 1"]["uprn"]["value"]

			if uprn == "500" {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message":"error"}`))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"integration": {
					"transformed": {
						"rows_data": {
							"0": {
								"Round": "Food2",
								"Date": "23/03/2026 00:00:00"
							},
							"1": {
								"Round": "Red13",
								"Date": "23/03/2026 00:00:00"
							},
							"2": {
								"Round": "Grey3",
								"Date": "24/03/2026 00:00:00"
							}
						}
					}
				}
			}`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))

	return server
}

func loadConfig(t *testing.T, configData string) config.Config {
	cfg := config.Config{}
	if err := yaml.Unmarshal([]byte(configData), &cfg); err != nil {
		t.Fatalf("Could not parse config")
	}
	return cfg
}

func normalConfig() string {
	return `
devices:
  - type: bins
    config:
      name: test1
      timeoutMs: 5000
      defaultAddress: "42 EXAMPLE STREET TESTTOWN AB1 2CD"
      serviceUrl: "https://example.com/service/Bin_Collection_Dates"
      uprnUrl: "https://example.com/uprn"
      lookupUrl: "https://example.com/lookup"
  - type: bins
    config:
      name: test2
      timeoutMs: 5000
      defaultAddress: "42 EXAMPLE STREET TESTTOWN AB1 2CD"
      serviceUrl: "https://example.com/service/Bin_Collection_Dates"
      uprnUrl: "https://example.com/uprn"
      lookupUrl: "https://example.com/lookup"
`
}

func singleDeviceConfig() string {
	return `
devices:
  - type: bins
    config:
      name: test1
      timeoutMs: 5000
      defaultAddress: "42 EXAMPLE STREET TESTTOWN AB1 2CD"
      serviceUrl: "https://example.com/service/Bin_Collection_Dates"
      uprnUrl: "https://example.com/uprn"
      lookupUrl: "https://example.com/lookup"
`
}

func emptyConfig() string {
	return `
devices: []
`
}

func missingParameterConfig() string {
	return `
devices:
  - type: bins
    config:
      name: test1
      timeoutMs: 5000
`
}

func TestRoutes(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	testCases := []struct {
		name          string
		configData    string
		routeCount    int
		expectedError error
	}{
		{
			name:          "default_config",
			configData:    normalConfig(),
			routeCount:    4,
			expectedError: nil,
		},
		{
			name:          "empty_yaml_config",
			configData:    emptyConfig(),
			routeCount:    0,
			expectedError: assert.AnError,
		},
		{
			name:          "missing_config_parameter",
			configData:    missingParameterConfig(),
			routeCount:    0,
			expectedError: assert.AnError,
		},
		{
			name:          "single_device_config",
			configData:    singleDeviceConfig(),
			routeCount:    1,
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadConfig(t, tc.configData)
			device := &Device{}
			r, err := device.Routes(&cfg)

			assert.IsType(t, tc.expectedError, err, "Error should be of type \"%T\", got \"%T (%v)\"", tc.expectedError, err, err)

			if len(r) != tc.routeCount {
				t.Fatalf("Wrong number of routes returned, Expected: %d, Got: %d", tc.routeCount, len(r))
			}
		})
	}
}

func TestHandlers(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	testCases := []struct {
		name         string
		method       string
		url          string
		data         []byte
		configData   string
		expectedCode int
		expectedBody string
		contentType  string
	}{
		{
			name:         "no_error",
			method:       "POST",
			url:          "/bins/test1?address=42%20EXAMPLE%20STREET%20TESTTOWN%20AB1%202CD",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":[{"name":"Food","date":"2026-03-23T00:00:00Z"},{"name":"Red","date":"2026-03-23T00:00:00Z"},{"name":"Grey","date":"2026-03-24T00:00:00Z"}]}`,
			contentType:  "application/json",
		},
		{
			name:         "no_error_json",
			method:       "POST",
			url:          "/bins/test2",
			data:         []byte(`{"address":"42 EXAMPLE STREET TESTTOWN AB1 2CD"}`),
			configData:   normalConfig(),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":[{"name":"Food","date":"2026-03-23T00:00:00Z"},{"name":"Red","date":"2026-03-23T00:00:00Z"},{"name":"Grey","date":"2026-03-24T00:00:00Z"}]}`,
			contentType:  "application/json",
		},
		{
			name:         "default_address_from_config",
			method:       "POST",
			url:          "/bins/test1",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":[{"name":"Food","date":"2026-03-23T00:00:00Z"},{"name":"Red","date":"2026-03-23T00:00:00Z"},{"name":"Grey","date":"2026-03-24T00:00:00Z"}]}`,
			contentType:  "application/json",
		},
		{
			name:         "get_device_request",
			method:       "GET",
			url:          "/bins/test1",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["address"]}`,
			contentType:  "application/json",
		},
		{
			name:         "get_base_request",
			method:       "GET",
			url:          "/bins/",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":["test1","test2"]}`,
			contentType:  "application/json",
		},
		{
			name:         "get_base_request_single_device",
			method:       "GET",
			url:          "/bins/",
			data:         nil,
			configData:   singleDeviceConfig(),
			expectedCode: 404,
			expectedBody: "404 page not found\n",
			contentType:  "application/json",
		},
		{
			name:         "post_base_request_single_device",
			method:       "POST",
			url:          "/test1",
			data:         []byte(`{"address":"42 EXAMPLE STREET TESTTOWN AB1 2CD"}`),
			configData:   singleDeviceConfig(),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":[{"name":"Food","date":"2026-03-23T00:00:00Z"},{"name":"Red","date":"2026-03-23T00:00:00Z"},{"name":"Grey","date":"2026-03-24T00:00:00Z"}]}`,
			contentType:  "application/json",
		},
		{
			name:         "unsupported_base_method",
			method:       "POST",
			url:          "/bins/",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 405,
			expectedBody: `{"message":"Method Not Allowed"}`,
			contentType:  "application/json",
		},
		{
			name:         "malformed_json_body",
			method:       "POST",
			url:          "/bins/test1",
			data:         []byte(`not_json`),
			configData:   normalConfig(),
			expectedCode: 400,
			expectedBody: `{"message":"Malformed Or Empty JSON Body"}`,
			contentType:  "application/json",
		},
		{
			name:         "malformed_query_string",
			method:       "POST",
			url:          "/bins/test1?monkeytest",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 400,
			expectedBody: `{"message":"Malformed or empty query string"}`,
			contentType:  "application/json",
		},
		{
			name:         "invalid_address_variable",
			method:       "POST",
			url:          "/bins/test1?address=AB1",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 400,
			expectedBody: `{"message":"Invalid Parameter: address"}`,
			contentType:  "application/json",
		},
		{
			name:         "address_not_found",
			method:       "POST",
			url:          "/bins/test1?address=1%20UNKNOWN%20ROAD%20NOWHERE%20ZZ1%209ZZ",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 404,
			expectedBody: `{"message":"Address Not Found"}`,
			contentType:  "application/json",
		},
		{
			name:         "internal_server_error",
			method:       "POST",
			url:          "/bins/test1?address=1%20ERROR%20ROAD%20SERVER%20ERROR",
			data:         nil,
			configData:   normalConfig(),
			expectedCode: 500,
			expectedBody: `{"message":"Internal Server Error"}`,
			contentType:  "application/json",
		},
		{
			name:         "utf8_content_type",
			method:       "POST",
			url:          "/bins/test2",
			data:         []byte(`{"address":"42 EXAMPLE STREET TESTTOWN AB1 2CD"}`),
			configData:   normalConfig(),
			expectedCode: 200,
			expectedBody: `{"message":"OK","data":[{"name":"Food","date":"2026-03-23T00:00:00Z"},{"name":"Red","date":"2026-03-23T00:00:00Z"},{"name":"Grey","date":"2026-03-24T00:00:00Z"}]}`,
			contentType:  "application/json; charset=utf-8",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadConfig(t, tc.configData)

			base, routes, err := routes(&cfg)
			if err != nil && tc.expectedCode != 404 {
				t.Fatalf("routes returned an error: %v", err)
			}

			router := mux.NewRouter()
			for _, r := range routes {
				router.HandleFunc(r.Path, r.Handler)
			}

			server := setupHTTPServer(t)
			if base != nil {
				for _, d := range base.Devices {
					d.ServiceURL = server.URL + "/service/Bin_Collection_Dates"
					d.UprnURL = server.URL + "/uprn"
					d.LookupURL = server.URL + "/lookup"
				}
			}

			defer server.Close()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.data))
			if tc.data != nil {
				request.Header.Set("Content-Type", tc.contentType)
			}

			router.ServeHTTP(recorder, request)

			if recorder.Code != tc.expectedCode {
				t.Fatalf("Unexpected HTTP status code. Expected: %d, Got: %d", tc.expectedCode, recorder.Code)
			}

			if recorder.Body.String() != tc.expectedBody {
				t.Fatalf("Unexpected response body. Expected: %s, Got: %s", tc.expectedBody, recorder.Body.String())
			}
		})
	}
}

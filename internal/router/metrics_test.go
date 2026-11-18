package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestMetricsEndpoint(t *testing.T) {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "restate_test_metric", Help: "Endpoint integration test."})
	prometheus.MustRegister(gauge)
	defer prometheus.Unregister(gauge)
	gauge.Set(22.5)
	router := NewRouter(nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, response.Body.String(), "restate_test_metric 22.5")
}

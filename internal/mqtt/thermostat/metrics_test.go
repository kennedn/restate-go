package thermostat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mockMqtt "github.com/kennedn/restate-go/internal/mqtt/frigate/mock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func metricValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			matching := true
			for key, value := range labels {
				found := false
				for _, label := range metric.Label {
					if label.GetName() == key && label.GetValue() == value {
						found = true
					}
				}
				matching = matching && found
			}
			if matching {
				if metric.Gauge != nil {
					return metric.Gauge.GetValue(), true
				}
				return metric.Counter.GetValue(), true
			}
		}
	}
	return 0, false
}

func TestSubscriptionCallbackMetrics(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	metrics := newThermostatMetrics(registry)
	// Fail control requests deliberately: observing MQTT must not depend on a
	// downstream sync succeeding, and no real devices are contacted.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cfg := &listenerConfig{Name: "home", Timeout: 1000}
	cfg.BTHome.URL = server.URL
	cfg.Radiator.URL = server.URL
	l := &listener{Config: cfg, Base: base{RadiatorMap: map[string]string{"a": "lounge", "b": "study"}}, metrics: metrics}
	send := func(payload string) {
		t.Helper()
		l.subscriptionCallback(nil, &mockMqtt.Message{PayloadVar: []byte(payload)})
	}
	check := func(suffix, id string, expected float64) {
		t.Helper()
		value, found := metricValue(t, registry, "restate_thermostat_"+suffix, map[string]string{"listener": "home", "id": id})
		require.True(t, found, "%s for %s", suffix, id)
		assert.Equal(t, expected, value)
	}
	before := float64(time.Now().Unix())
	send(`{"header":{"namespace":"Appliance.Hub.Mts100.Temperature"},"payload":{"temperature":[{"id":"a","room":195,"currentSet":220},{"id":"b","room":230,"currentSet":200},{"id":"unknown","room":1,"currentSet":2}]}}`)
	check("radiator_room_temperature_celsius", "a", 19.5)
	check("radiator_target_temperature_celsius", "a", 22)
	check("radiator_heat_demand", "a", 1)
	check("radiator_room_temperature_celsius", "b", 23)
	check("radiator_heat_demand", "b", 0)
	check("radiator_updates_total", "b", 1)
	_, found := metricValue(t, registry, "restate_thermostat_radiator_room_temperature_celsius", map[string]string{"id": "unknown"})
	assert.False(t, found, "unconfigured IDs must not create series")
	_, found = metricValue(t, registry, "restate_thermostat_radiator_on", map[string]string{"id": "a"})
	assert.False(t, found, "missing fields must not appear as zero")

	// Partial messages retain previously observed fields, including across namespaces.
	send(`{"header":{"namespace":"Appliance.Hub.Mts100.Temperature"},"payload":{"temperature":[{"id":"a","currentSet":0}]}}`)
	send(`{"header":{"namespace":"Appliance.Hub.ToggleX"},"payload":{"togglex":[{"id":"a","onoff":0},{"id":"b","onoff":1}]}}`)
	check("radiator_room_temperature_celsius", "a", 19.5)
	check("radiator_target_temperature_celsius", "a", 0)
	check("radiator_heat_demand", "a", 0)
	check("radiator_on", "a", 0)
	check("radiator_on", "b", 1)
	check("radiator_updates_total", "a", 3)
	seen, found := metricValue(t, registry, "restate_thermostat_radiator_last_message_timestamp_seconds", map[string]string{"id": "a"})
	require.True(t, found)
	assert.GreaterOrEqual(t, seen, before)
	assert.LessOrEqual(t, seen, float64(time.Now().Unix()+1))

	send(`{"header":{"namespace":"unrelated"},"payload":{"temperature":[{"id":"a","room":1000}]}}`)
	send(`not json`)
	check("radiator_room_temperature_celsius", "a", 19.5)
	for result, expected := range map[string]float64{"valid_json": 4, "invalid_json": 1} {
		value, found := metricValue(t, registry, "restate_thermostat_mqtt_messages_total", map[string]string{"listener": "home", "result": result})
		require.True(t, found)
		assert.Equal(t, expected, value)
	}
}

func TestMetricsConcurrentUpdatesAndScrapes(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	metrics := newThermostatMetrics(registry)
	l := &listener{Config: &listenerConfig{Name: "home"}, Base: base{RadiatorMap: map[string]string{"a": "lounge"}}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			room, target := int64(200), int64(220)
			status := rawStatus{}
			status.Header.Namespace = "Appliance.Hub.Mts100.Temperature"
			status.Payload.Temperature = []radiatorStatus{{Id: "a", Room: &room, CurrentSet: &target}}
			for j := 0; j < 10; j++ {
				metrics.observe(l, &status)
				if _, err := registry.Gather(); err != nil {
					t.Error(fmt.Errorf("scrape: %w", err))
				}
			}
		}()
	}
	wg.Wait()
	value, found := metricValue(t, registry, "restate_thermostat_radiator_updates_total", map[string]string{"id": "a"})
	require.True(t, found)
	assert.Equal(t, float64(200), value)
}

func TestSubscriptionCallbackModeMetrics(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	metrics := newThermostatMetrics(registry)
	l := &listener{
		Config:  &listenerConfig{Name: "home"},
		Base:    base{RadiatorMap: map[string]string{"a": "lounge", "b": "study"}},
		metrics: metrics,
	}
	send := func(payload string) {
		t.Helper()
		l.subscriptionCallback(nil, &mockMqtt.Message{PayloadVar: []byte(payload)})
	}
	mode := func(id string, expected float64) {
		t.Helper()
		value, found := metricValue(t, registry, "restate_thermostat_radiator_mode", map[string]string{"listener": "home", "id": id})
		require.True(t, found)
		assert.Equal(t, expected, value)
	}
	// Record every entry, including custom mode 0; ignore unconfigured IDs.
	send(`{"header":{"namespace":"Appliance.Hub.Mts100.Mode"},"payload":{"mode":[{"id":"a","state":0},{"id":"b","state":3},{"id":"unknown","state":4}]}}`)
	mode("a", 0)
	mode("b", 3)
	_, found := metricValue(t, registry, "restate_thermostat_radiator_mode", map[string]string{"id": "unknown"})
	assert.False(t, found)
	_, found = metricValue(t, registry, "restate_thermostat_radiator_room_temperature_celsius", map[string]string{"id": "a"})
	assert.False(t, found, "mode messages must not create temperature readings")
	_, found = metricValue(t, registry, "restate_thermostat_radiator_heat_demand", map[string]string{"id": "a"})
	assert.False(t, found, "mode alone must not create a heat demand reading")

	send(`{"header":{"namespace":"Appliance.Hub.Mts100.Mode"},"payload":{"mode":[{"id":"a","state":4}]}}`)
	mode("a", 4)
	// An absent state must preserve the previous observation rather than setting 0.
	send(`{"header":{"namespace":"Appliance.Hub.Mts100.Mode"},"payload":{"mode":[{"id":"a"}]}}`)
	mode("a", 4)
	value, found := metricValue(t, registry, "restate_thermostat_radiator_updates_total", map[string]string{"id": "a"})
	require.True(t, found)
	assert.Equal(t, float64(2), value)
	_, found = metricValue(t, registry, "restate_thermostat_radiator_last_message_timestamp_seconds", map[string]string{"id": "b"})
	assert.True(t, found)

	// Other state updates must not clear the last reported mode.
	room := int64(200)
	status := rawStatus{}
	status.Header.Namespace = "Appliance.Hub.Mts100.Temperature"
	status.Payload.Temperature = []radiatorStatus{{Id: "a", Room: &room}}
	metrics.observe(l, &status)
	mode("a", 4)
	mode("b", 3)

	// Do not interpret a different namespace's state field as a TRV mode.
	send(`{"header":{"namespace":"Appliance.Control.Thermostat.Unknown"},"payload":{"mode":[{"id":"a","state":1}]}}`)
	mode("a", 4)
}

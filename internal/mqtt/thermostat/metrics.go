package thermostat

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Values are retained until another MQTT observation or a process restart.
// Only configured radiator IDs are used as labels to bound series cardinality.
type thermostatMetrics struct {
	messages     *prometheus.CounterVec
	updates      *prometheus.CounterVec
	room         *prometheus.GaugeVec
	target       *prometheus.GaugeVec
	onoff        *prometheus.GaugeVec
	mode         *prometheus.GaugeVec
	heating      *prometheus.GaugeVec
	lastSeen     *prometheus.GaugeVec
	mu           sync.Mutex
	temperatures map[radiatorMetricKey]observedTemperature
}

type radiatorMetricKey struct {
	listener string
	id       string
}

type observedTemperature struct {
	room   *int64
	target *int64
}

var defaultMetrics = newThermostatMetrics(prometheus.DefaultRegisterer)

func newThermostatMetrics(registerer prometheus.Registerer) *thermostatMetrics {
	labels := []string{"listener", "radiator", "id"}
	gauge := func(name, help string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "restate", Subsystem: "thermostat", Name: name, Help: help,
		}, labels)
	}
	m := &thermostatMetrics{
		messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "restate", Subsystem: "thermostat", Name: "mqtt_messages_total",
			Help: "MQTT messages received, classified by JSON decoding result (not sync success).",
		}, []string{"listener", "result"}),
		updates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "restate", Subsystem: "thermostat", Name: "radiator_updates_total",
			Help: "Configured radiator state entries observed in MQTT messages.",
		}, labels),
		room:         gauge("radiator_room_temperature_celsius", "Latest reported radiator room temperature in degrees Celsius."),
		target:       gauge("radiator_target_temperature_celsius", "Latest reported radiator target temperature in degrees Celsius."),
		onoff:        gauge("radiator_on", "Latest reported radiator on/off state (1 on, 0 off)."),
		mode:         gauge("radiator_mode", "Latest reported radiator mode code from MQTT."),
		heating:      gauge("radiator_heat_demand", "Inferred radiator heat demand: 1 if latest target exceeds latest room temperature, otherwise 0; not a boiler or valve measurement."),
		lastSeen:     gauge("radiator_last_message_timestamp_seconds", "Unix time of the latest MQTT state entry for this radiator."),
		temperatures: make(map[radiatorMetricKey]observedTemperature),
	}
	registerer.MustRegister(m.messages, m.updates, m.room, m.target, m.onoff, m.mode, m.heating, m.lastSeen)
	return m
}

func (m *thermostatMetrics) observe(l *listener, status *rawStatus) {
	var entries []radiatorStatus
	switch status.Header.Namespace {
	case "Appliance.Hub.Mts100.Temperature":
		entries = status.Payload.Temperature
	case "Appliance.Hub.ToggleX":
		entries = status.Payload.ToggleX
	case "Appliance.Hub.Mts100.Mode":
		entries = status.Payload.Mode
	default:
		return
	}

	// Keep cached partial temperatures and their derived demand in the same order
	// when callbacks arrive concurrently. Scraping never calls downstream APIs.
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range entries {
		name, known := l.Base.RadiatorMap[entry.Id]
		if !known || entry.Id == "" || (entry.Room == nil && entry.CurrentSet == nil && entry.OnOff == nil && entry.State == nil) {
			continue
		}
		labels := []string{l.Config.Name, name, entry.Id}
		key := radiatorMetricKey{listener: l.Config.Name, id: entry.Id}
		state := m.temperatures[key]
		if entry.Room != nil {
			state.room = entry.Room
			m.room.WithLabelValues(labels...).Set(float64(*entry.Room) / 10)
		}
		if entry.CurrentSet != nil {
			state.target = entry.CurrentSet
			m.target.WithLabelValues(labels...).Set(float64(*entry.CurrentSet) / 10)
		}
		if entry.OnOff != nil {
			m.onoff.WithLabelValues(labels...).Set(float64(*entry.OnOff))
		}
		if entry.State != nil {
			m.mode.WithLabelValues(labels...).Set(float64(*entry.State))
		}
		m.temperatures[key] = state
		if state.room != nil && state.target != nil {
			demand := float64(0)
			if *state.target > *state.room {
				demand = 1
			}
			m.heating.WithLabelValues(labels...).Set(demand)
		}
		m.lastSeen.WithLabelValues(labels...).SetToCurrentTime()
		m.updates.WithLabelValues(labels...).Inc()
	}
}

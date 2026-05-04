package thermostat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	msh "github.com/kennedn/restate-go/internal/device/meross/msh300hk"
	mockMqtt "github.com/kennedn/restate-go/internal/mqtt/frigate/mock"

	"github.com/stretchr/testify/assert"
)

func TestProcessExpiredOverrides(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	// create temp overrides file
	tmp, err := os.CreateTemp("", "heating-overrides-*.json")
	if err != nil {
		t.Fatalf("failed to create tmp file: %v", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	// prepare payload with two expired (dev1,dev3) and one future (dev2)
	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour).Format(time.RFC3339)
	future := now.Add(1 * time.Hour).Format(time.RFC3339)
	payload := map[string]map[string]string{"boost": {"dev1": past, "dev2": future, "dev3": past}}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(tmp.Name(), data, 0o644); err != nil {
		t.Fatalf("failed to write overrides: %v", err)
	}

	// override path
	original := ""
	original = ""
	msh.SetHeatingOverridesPath(tmp.Name())
	defer msh.SetHeatingOverridesPath(original)

	// mock radiator server to capture mode set calls
	received := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = body["hosts"]
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	// build config with radiator/thermostat entries
	cfg := config.Config{}
	// bthome device (name rad1)
	cfg.Devices = append(cfg.Devices, config.Devices{Type: "bthome", Config: map[string]any{"name": "rad1"}})
	// meross radiator device with ids dev1,dev2,dev3 and name rad1
	cfg.Devices = append(cfg.Devices, config.Devices{Type: "meross", Config: map[string]any{"name": "rad1", "deviceType": "radiator", "ids": []string{"dev1", "dev2", "dev3"}}})
	// thermostat device pointing to our mock radiator server
	thermoCfg := map[string]any{
		"name":       "therm1",
		"timeoutMs":  1000,
		"mqtt":       map[string]any{"host": "localhost", "port": 1883},
		"radiator":   map[string]any{"url": server.URL, "uuid": "rad-uuid"},
		"thermostat": map[string]any{"url": "", "uuid": "therm-uuid", "syncIntervalMs": 60000},
	}
	cfg.Devices = append(cfg.Devices, config.Devices{Type: "thermostat", Config: thermoCfg})

	// use mock mqtt client so listeners doesn't try to connect
	mockClient := &mockMqtt.Client{}

	_, ls, err := listeners(&cfg, mockClient)
	if err != nil {
		t.Fatalf("listeners returned error: %v", err)
	}
	if len(ls) == 0 {
		t.Fatalf("no listeners created")
	}

	l := ls[0]

	// call the function under test
	l.processExpiredOverrides()

	// verify radiator server received only expired hosts (order may vary)
	parts := strings.Split(received, ",")
	assert.Len(t, parts, 2)
	// verify overrides file now only contains dev2
	raw, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("failed to read overrides file: %v", err)
	}
	got := map[string]map[string]string{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("failed to parse overrides file: %v", err)
	}
	boost := got["boost"]
	if !assert.Len(t, boost, 1) {
		t.Fatalf("expected 1 remaining override, got %d", len(boost))
	}
	if _, ok := boost["dev2"]; !ok {
		t.Fatalf("expected dev2 to remain")
	}
}

func TestConcurrentAddAndClear(t *testing.T) {
	logging.SetLogLevel(logging.Error)

	tmp, err := os.CreateTemp("", "heating-overrides-*.json")
	if err != nil {
		t.Fatalf("failed to create tmp file: %v", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	// start with empty payload
	if err := os.WriteFile(tmp.Name(), []byte(`{"boost":{}}`), 0o644); err != nil {
		t.Fatalf("failed to init overrides: %v", err)
	}

	original := ""
	msh.SetHeatingOverridesPath(tmp.Name())
	defer msh.SetHeatingOverridesPath(original)

	// mock radiator server (accept requests)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	// build minimal config for listener
	cfg := config.Config{}
	cfg.Devices = append(cfg.Devices, config.Devices{Type: "bthome", Config: map[string]any{"name": "rad1"}})
	cfg.Devices = append(cfg.Devices, config.Devices{Type: "meross", Config: map[string]any{"name": "rad1", "deviceType": "radiator", "ids": []string{"dev1"}}})
	thermoCfg := map[string]any{"name": "therm1", "timeoutMs": 1000, "mqtt": map[string]any{"host": "localhost", "port": 1883}, "radiator": map[string]any{"url": server.URL, "uuid": "rad-uuid"}, "thermostat": map[string]any{"url": "", "uuid": "therm-uuid", "syncIntervalMs": 60000}}
	cfg.Devices = append(cfg.Devices, config.Devices{Type: "thermostat", Config: thermoCfg})

	mockClient := &mockMqtt.Client{}
	_, ls, err := listeners(&cfg, mockClient)
	if err != nil {
		t.Fatalf("listeners returned error: %v", err)
	}
	l := ls[0]

	// Run concurrent writers adding future overrides while clearing runs
	done := make(chan struct{})
	go func() {
		// add an override repeatedly
		for i := 0; i < 50; i++ {
			_ = msh.AddHeatingOverrides([]string{"dev1"}, time.Now().Add(2*time.Hour))
		}
		close(done)
	}()

	// while writers run, repeatedly clear expired (none should be expired)
	for i := 0; i < 50; i++ {
		l.processExpiredOverrides()
	}

	<-done

	// final file should be valid JSON and contain dev1
	raw, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("failed to read overrides file: %v", err)
	}
	got := map[string]map[string]string{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("failed to parse overrides file: %v", err)
	}
	boost := got["boost"]
	if !assert.GreaterOrEqual(t, len(boost), 1) {
		t.Fatalf("expected at least 1 override, got %d", len(boost))
	}
	if _, ok := boost["dev1"]; !ok {
		t.Fatalf("expected dev1 to be present")
	}
}

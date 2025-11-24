package thermostat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/kennedn/restate-go/internal/common/logging"
	"gopkg.in/yaml.v3"
)

type radiatorResponse struct {
	Data []struct {
		Status struct {
			Temperature struct {
				Heating bool `json:"heating"`
			} `json:"temperature,omitempty"`
		} `json:"status,omitempty"`
		Value json.Number `json:"value,omitempty"`
	} `json:"data"`
}

type btHomeResponse struct {
	Data struct {
		Temperature json.Number `json:"temperature"`
	} `json:"data"`
}

type radiatorConfig struct {
	Name       string   `yaml:"name"`
	DeviceType string   `yaml:"deviceType"`
	Ids        []string `yaml:"ids"`
}

type radiatorStatus struct {
	CurrentSet int64  `json:"currentSet"`
	Room       int64  `json:"room"`
	Id         string `json:"id"`
}

// rawStatus represents the raw status response from a Meross thermostat device.
type rawStatus struct {
	Header struct {
		Namespace string `json:"namespace"`
	} `json:"header"`
	Payload struct {
		Temperature []radiatorStatus `json:"temperature"`
	} `json:"payload"`
}

// Device represents an MQTT device that listens to messages and triggers alerts.
type listener struct {
	Base   base
	Config *listenerConfig
}

// Config represents the configuration for the MQTT alert device.
type listenerConfig struct {
	Name    string `yaml:"name"`
	Client  mqtt.Client
	Timeout uint `yaml:"timeoutMs"`
	MQTT    struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"mqtt"`
	BTHome struct {
		URL string `yaml:"url"`
	} `yaml:"bthome"`
	Radiator struct {
		URL  string `yaml:"url"`
		UUID string `yaml:"uuid"`
	} `yaml:"radiator"`
	Thermostat struct {
		URL          string `yaml:"url"`
		UUID         string `yaml:"uuid"`
		SyncInterval uint   `yaml:"syncIntervalMs"`
	} `yaml:"thermostat"`
}

type base struct {
	Listeners   []*listener
	RadiatorMap map[string]string
}

type Device struct{}

// Create mqtt Listeners from a config
func (d *Device) Listeners(config *config.Config) ([]listener, error) {
	_, listeners, err := listeners(config, nil)
	return listeners, err
}

// listeners is a function that creates one or more MQTT listeners
// It returns the base object and a slice of listeners.
func listeners(config *config.Config, client mqtt.Client) (*base, []listener, error) {
	listeners := []listener{}
	base := base{}
	base.RadiatorMap = map[string]string{}

	// Build map of radiator IDs to names for meross_radiator devices
	for _, d := range config.Devices {
		if d.Type != "meross" {
			continue
		}

		radiatorConfig := radiatorConfig{}
		// Marshal the device config to YAML
		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		// Unmarshal the YAML config into the deviceConfig struct
		if err := yaml.Unmarshal(yamlConfig, &radiatorConfig); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		if radiatorConfig.DeviceType != "radiator" {
			continue
		}

		for _, id := range radiatorConfig.Ids {
			base.RadiatorMap[id] = radiatorConfig.Name
		}
	}

	// Check if any listeners were created
	if len(base.RadiatorMap) == 0 {
		return nil, []listener{}, errors.New("no radiator devices found in config")
	}

	// Iterate through each thermostat device in the configuration
	for _, d := range config.Devices {
		if d.Type != "thermostat" {
			continue
		}

		listenerConfig := listenerConfig{}
		listener := listener{
			Base: base,
		}

		// Marshal the device config to YAML
		yamlConfig, err := yaml.Marshal(d.Config)
		if err != nil {
			logging.Log(logging.Info, "Unable to marshal device config")
			continue
		}

		// Unmarshal the YAML config into the listenerConfig struct
		if err := yaml.Unmarshal(yamlConfig, &listenerConfig); err != nil {
			logging.Log(logging.Info, "Unable to unmarshal device config")
			continue
		}

		// Check for missing parameters in the listenerConfig
		if listenerConfig.Name == "" || listenerConfig.Timeout == 0 || listenerConfig.Radiator.UUID == "" || listenerConfig.Thermostat.UUID == "" || listenerConfig.MQTT.Host == "" {
			logging.Log(logging.Info, "Unable to load device due to missing parameters")
			continue
		}

		if listenerConfig.Thermostat.SyncInterval == 0 {
			listenerConfig.Thermostat.SyncInterval = 15 * 60 * 1000 // 15 minutes default
		}

		// Set default values for optional parameters
		if listenerConfig.MQTT.Port == 0 {
			listenerConfig.MQTT.Port = 1883
		}

		// Create MQTT client if not provided
		if client == nil {
			clientOpts := mqtt.NewClientOptions()
			clientOpts.AddBroker(fmt.Sprintf("tcp://%s:%d", listenerConfig.MQTT.Host, listenerConfig.MQTT.Port))
			clientOpts.SetClientID("thermostat-restate-go")
			clientOpts.SetOnConnectHandler(listener.connectionCallback)
			clientOpts.SetConnectionLostHandler(listener.connectionLostCallback)
			client = mqtt.NewClient(clientOpts)
		}

		// Attempt to connect to the MQTT broker with a timeout
		token := client.Connect()
		if err = mqtt.WaitTokenTimeout(token, time.Duration(listenerConfig.Timeout)*time.Millisecond); err != nil {
			logging.Log(logging.Info, err.Error())
			continue
		}

		// Set the MQTT client in the listenerConfig
		listenerConfig.Client = client

		// Set the listenerConfig in the listener
		listener.Config = &listenerConfig

		// Start the thermostat reconcile loop
		listener.startThermostatSyncStalenessCheck()

		// Append the listener to the base object and the listeners slice
		base.Listeners = append(base.Listeners, &listener)
		listeners = append(listeners, listener)

		logging.Log(logging.Info, "Setup device \"%s\"", listener.Config.Name)
	}

	// Check if any listeners were created
	if len(listeners) == 0 {
		return nil, []listener{}, errors.New("no listeners found in config")
	}

	return &base, listeners, nil
}

// connectionCallback subscribes to the necessary MQTT topics upon connection.
func (l *listener) connectionCallback(client mqtt.Client) {
	logging.Log(logging.Info, "MQTT connected")

	UUIDs := []string{l.Config.Thermostat.UUID, l.Config.Radiator.UUID}

	for _, UUID := range UUIDs {
		topic := fmt.Sprintf("/appliance/%s/publish", UUID)
		token := client.Subscribe(topic, 0, l.subscriptionCallback)
		err := mqtt.WaitTokenTimeout(token, time.Duration(l.Config.Timeout)*time.Millisecond)
		if err != nil {
			logging.Log(logging.Error, "Failed to subscribe to %s: %v", topic, token.Error())
			continue
		}
		logging.Log(logging.Info, "Successfully subscribed to %s", topic)
	}
}

func (l *listener) connectionLostCallback(_ mqtt.Client, err error) {
	logging.Log(logging.Info, "MQTT connection lost: %v", err)
}

// lastThermostatSyncFromMQTT stores the last time thermostatSync was triggered by MQTT.
// 0 means "never".
var lastThermostatSyncFromMQTT atomic.Int64

// startThermostatSyncStalenessCheck starts a loop that periodically synchronizes the thermostat state with the TRVs,
// but only if MQTT-triggered sync has not happened within SyncInterval.
func (l *listener) startThermostatSyncStalenessCheck() {
	interval := 60 * time.Second
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			last := lastThermostatSyncFromMQTT.Load()
			lastTime := time.Unix(0, last)
			staleAfter := time.Duration(l.Config.Thermostat.SyncInterval) * time.Millisecond

			if time.Since(lastTime) >= staleAfter {
				logging.Log(logging.Info, "thermostatSync has gone stale after %s, syncing now", staleAfter)
				l.thermostatSync()
			}
		}
	}()
}

// subscriptionCallback is called when a message is received on the subscribed MQTT topic.
func (l *listener) subscriptionCallback(_ mqtt.Client, message mqtt.Message) {
	status := rawStatus{}
	if err := json.Unmarshal(message.Payload(), &status); err != nil {
		logging.Log(logging.Error, "Failed to unmarshal MQTT message: %v", err)
		return
	}

	// Extract radiatorStatus information safely
	var radiatorStatus *radiatorStatus
	if len(status.Payload.Temperature) != 0 {
		radiatorStatus = &status.Payload.Temperature[0]
	}

	// Perform thermostat sync if the message originates from thermostat or TRV,
	// and record that MQTT was the trigger.
	if status.Header.Namespace == "Appliance.Hub.Mts100.Temperature" || status.Header.Namespace == "Appliance.Control.Thermostat.Mode" {
		l.thermostatSync()
	}

	// Only proceed with btHomeSync if message originates from TRV and has room data
	if status.Header.Namespace != "Appliance.Hub.Mts100.Temperature" || radiatorStatus == nil || radiatorStatus.Id == "" || radiatorStatus.Room == 0 {
		return
	}

	l.btHomeSyncTemperature(radiatorStatus)
}

// Paho can trigger writes to this map concurrently, so we need to use a concurrent map implementation, e.g sync.Map
var radiatorLastProcessed sync.Map
var settleTime = 1 * time.Minute

// btHomeSyncTemperature uses external BTHome temperature sensors to make adjustments to the onboard Meross TRV temperature reading
// @param message The MQTT message containing the thermostat status
func (l *listener) btHomeSyncTemperature(radiatorStatus *radiatorStatus) {

	// Lookup BTHome name from radiator ID
	btHomeName, ok := l.Base.RadiatorMap[radiatorStatus.Id]
	if !ok {
		logging.Log(logging.Info, "Unable to find device with id %s in radiator map", radiatorStatus.Id)
		return
	}

	// Skip if we already processed recently
	if lastProcessedRaw, ok := radiatorLastProcessed.Load(radiatorStatus.Id); ok {
		if lastProcessed, ok := lastProcessedRaw.(time.Time); ok && time.Since(lastProcessed) < settleTime {
			logging.Log(logging.Info, "Skipping TRV with name %s (id: %s) as it was processed recently", btHomeName, radiatorStatus.Id)
			return
		}
	}

	// Get BTHome response
	btHomeResponse, httpStatus, err := l.getBTHomeTemperature(btHomeName)
	if err != nil || httpStatus != 200 {
		logging.Log(logging.Error, "Failed to get BTHome temperature for %s: %v (HTTP status: %d)", btHomeName, err, httpStatus)
		return
	}

	// Parse BTHome temperature
	btHomeTemperature, err := btHomeResponse.Data.Temperature.Float64()
	if err != nil {
		logging.Log(logging.Error, "Failed to parse BTHome temperature for %s: %v", btHomeName, err)
		return
	}

	// Round temperature to nearest 0.5°C, then convert to Meross "tenths" integer format
	btHomeTemperatureRounded := math.Round(btHomeTemperature*2.0) / 2.0
	btHomeTemperatureInt := int64(btHomeTemperatureRounded * 10.0)

	// Get radiator adjust response
	adjustResponse, httpStatus, err := l.getRadiatorAdjust(radiatorStatus.Id)
	if err != nil || httpStatus != 200 || len(adjustResponse.Data) == 0 {
		logging.Log(logging.Error, "Failed to get radiator adjust for %s: %v (HTTP status: %d)", btHomeName, err, httpStatus)
		return
	}

	// Parse radiator adjust value
	adjustDelta, err := adjustResponse.Data[0].Value.Int64()
	if err != nil {
		logging.Log(logging.Error, "Failed to parse radiator adjust for %s: %v", btHomeName, err)
		return
	}

	// Meross does not expose a pre-adjusted temperature, so we need to calculate it manually with the adjust delta
	correctedTemperature := radiatorStatus.Room - (adjustDelta / 10)

	// Calculate new adjust delta
	delta := (btHomeTemperatureInt - correctedTemperature) * 10

	// Sanity check delta, meross allows +/- 500 max
	if max(delta, -delta) > 500 {
		logging.Log(logging.Info, "Delta(%d) exceeds +/- 500 and won't be accepted by TRV, not applying", delta)
		return
	}

	// Set new radiator adjust value on the TRV
	httpStatus, err = l.setRadiatorAdjust(radiatorStatus.Id, delta)
	if err != nil || httpStatus != 200 {
		logging.Log(logging.Error, "Failed to set radiator adjust for %s: %v (HTTP status: %d)", btHomeName, err, httpStatus)
		return
	}

	logging.Log(logging.Info, "Synced TRV with name %s (id: %s, old_temp: %d, new_temp: %d, delta: %d)", btHomeName, radiatorStatus.Id, radiatorStatus.Room, btHomeTemperatureInt, delta/10)

	// Update last processed time
	radiatorLastProcessed.Store(radiatorStatus.Id, time.Now())
}

// thermostatSync checks if any TRVs are requesting heat and toggles the thermostat state accordingly
func (l *listener) thermostatSync() {
	radiatorStatus, httpStatus, err := l.getEachRadiatorStatus()
	if err != nil || httpStatus != 200 {
		logging.Log(logging.Error, "Failed to get radiator status: %v (HTTP status: %d)", err, httpStatus)
		return
	}

	if len(radiatorStatus.Data) == 0 {
		logging.Log(logging.Info, "No radiator status data received")
		return
	}

	// Set heat to min / max based on any TRV heating requests
	var heat int64 = 50
	for _, d := range radiatorStatus.Data {
		if d.Status.Temperature.Heating {
			heat = 350
			break
		}
	}

	// Set thermostat temperature
	httpStatus, err = l.setThermostatHeat(heat)
	if err != nil || httpStatus != 200 {
		logging.Log(logging.Error, "Failed to set thermostat heat: %v (HTTP status: %d)", err, httpStatus)
		return
	}

	logging.Log(logging.Info, "Set thermostat heat to %d", heat)

	// Update time of last successful sync
	lastThermostatSyncFromMQTT.Store(time.Now().UnixNano())
}

// post sends a POST request to the specified URL with the given name and request body.
// @param baseUrl The base URL to send the request to.
// @param name The endpoint name to append to the base URL.
// @param request The request body as a map of strings.
// @return A pointer to the response body, HTTP status code, and error if any.
func (l *listener) post(url string, request map[string]string) (*[]byte, int, error) {
	method := "POST"
	client := &http.Client{
		Timeout: time.Duration(l.Config.Timeout) * time.Millisecond,
	}

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(requestBytes))
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return &body, resp.StatusCode, nil
}

// sendAlert sends a pushover alert based on the provided request.
// @param name The BTHome device name.
// @return A pointer to btHomeResponse, HTTP status code, and error if any.
func (l *listener) getBTHomeTemperature(name string) (*btHomeResponse, int, error) {
	body, httpStatus, err := l.post(fmt.Sprintf("%s/%s", l.Config.BTHome.URL, name), map[string]string{"code": "status"})
	if err != nil || httpStatus != 200 {
		return nil, httpStatus, err
	}

	rawResponse := btHomeResponse{}
	if err := json.Unmarshal(*body, &rawResponse); err != nil {
		return nil, httpStatus, err
	}

	return &rawResponse, httpStatus, nil
}

// getEachRadiatorStatus retrieves the status of all radiators.
// @return A pointer to radiatorResponse, HTTP status code, and error if any.
func (l *listener) getEachRadiatorStatus() (*radiatorResponse, int, error) {
	// Build comma-separated list of radiator IDs
	radiatorKeys := []string{}
	for key := range l.Base.RadiatorMap {
		radiatorKeys = append(radiatorKeys, key)
	}
	hosts := strings.Join(radiatorKeys, ",")

	body, httpStatus, err := l.post(l.Config.Radiator.URL, map[string]string{"hosts": hosts, "code": "status"})
	if err != nil || httpStatus != 200 {
		return nil, httpStatus, err
	}

	rawResponse := radiatorResponse{}
	if err := json.Unmarshal(*body, &rawResponse); err != nil {
		return nil, httpStatus, err
	}

	return &rawResponse, httpStatus, nil
}

// getRadiatorAdjust retrieves the radiator adjustment for a given ID.
// @param id The radiator ID.
// @return A pointer to adjustGetResponse, HTTP status code, and error if any.
func (l *listener) getRadiatorAdjust(id string) (*radiatorResponse, int, error) {
	body, httpStatus, err := l.post(fmt.Sprintf("%s/%s", l.Config.Radiator.URL, id), map[string]string{"code": "adjust"})
	if err != nil || httpStatus != 200 {
		return nil, httpStatus, err
	}

	rawResponse := radiatorResponse{}
	if err := json.Unmarshal(*body, &rawResponse); err != nil {
		return nil, httpStatus, err
	}

	return &rawResponse, httpStatus, nil
}

// setRadiatorAdjust sets the radiator adjustment for a given ID and value.
// @param id The radiator ID.
// @param value The adjustment value to set.
// @return HTTP status code, and error if any.
func (l *listener) setRadiatorAdjust(id string, value int64) (int, error) {
	_, httpStatus, err := l.post(fmt.Sprintf("%s/%s", l.Config.Radiator.URL, id), map[string]string{"code": "adjust", "value": fmt.Sprintf("%d", value)})
	if err != nil || httpStatus != 200 {
		return httpStatus, err
	}

	return httpStatus, nil
}

// setThermostatHeat sets the thermostat heat temperature for a given ID and value.
// @param id The radiator ID.
// @param value The adjustment value to set.
// @return HTTP status code, and error if any.
func (l *listener) setThermostatHeat(value int64) (int, error) {
	_, httpStatus, err := l.post(l.Config.Thermostat.URL, map[string]string{"code": "heatTemp", "value": fmt.Sprintf("%d", value)})
	if err != nil || httpStatus != 200 {
		return httpStatus, err
	}

	return httpStatus, nil
}

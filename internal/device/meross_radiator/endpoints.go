package meross_radiator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"slices"

	device "github.com/kennedn/restate-go/internal/device/common"
)

// defaultBase contains the endpoint metadata and base template previously
// captured from device.yaml. Embedding it removes the runtime file
// dependency and keeps parity with the original YAML configuration.
// endpoint describes a Meross device control endpoint with code,
// supported devices, and other properties.
type endpoint struct {
	Code             string   `yaml:"code"`
	SupportedDevices []string `yaml:"supportedDevices"`
	MinValue         int64    `yaml:"minValue,omitempty"`
	MaxValue         int64    `yaml:"maxValue,omitempty"`
	Namespace        string   `yaml:"namespace"`
	Template         string   `yaml:"template"`
}

// EndpointIF defines the behaviours each concrete endpoint must provide.
type EndpointIF interface {
	Code() string
	Namespace() string
	Template() string
	Supports(deviceType string) bool
	ValidateValue(value json.Number) error
	BuildPayload(ids []string, value json.Number) string
	ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error)
	ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error)
	HandleMeross(m *meross, req device.Request) (int, any, string, error)
	HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error)
}

// namedStatus associates a device name with its status payload.
type namedStatus struct {
	Name   string `json:"name"`
	Status any    `json:"status"`
}

type temperature struct {
	Current    *int64 `json:"current"`
	Target     *int64 `json:"target"`
	Heating    *bool  `json:"heating"`
	OpenWindow *bool  `json:"openWindow"`
}

type statusGet struct {
	Id          *string      `json:"id,omitempty"`
	Onoff       *int64       `json:"onoff,omitempty"`
	Mode        *int64       `json:"mode,omitempty"`
	Online      *int64       `json:"online,omitempty"`
	Temperature *temperature `json:"temperature,omitempty"`
}

type singleGet struct {
	Id    *string `json:"id,omitempty"`
	Value *int64  `json:"value,omitempty"`
}

// rawStatus is a simplified representation of Meross raw responses used by
// the radiator endpoints. It doesn't need to match every other device's
// shape, only the fields we consume here.
type rawStatus struct {
	Payload struct {
		Error struct {
			Code   int64  `json:"code,omitempty"`
			Detail string `json:"detail,omitempty"`
		} `json:"error,omitempty"`
		All []struct {
			ID      string `json:"id"`
			Togglex struct {
				Onoff int64 `json:"onoff"`
			} `json:"togglex,omitempty"`
			Temperature struct {
				Room       int64 `json:"room,omitempty"`
				CurrentSet int64 `json:"currentSet,omitempty"`
				OpenWindow int64 `json:"openWindow,omitempty"`
			} `json:"temperature,omitempty"`
			Mode struct {
				State int64 `json:"state,omitempty"`
			} `json:"mode,omitempty"`
			Online struct {
				Status int64 `json:"status,omitempty"`
			} `json:"online,omitempty"`
		} `json:"all,omitempty"`
		Battery []struct {
			ID    string `json:"id"`
			Value int64  `json:"value"`
		} `json:"battery,omitempty"`
		Mode []struct {
			ID    string `json:"id"`
			State int64  `json:"state"`
		} `json:"mode,omitempty"`
		Adjust []struct {
			ID          string `json:"id"`
			Temperature int64  `json:"temperature"`
		} `json:"adjust,omitempty"`
	} `json:"payload"`
}

var defaultBase = base{
	BaseTemplate: `{"header":{"from": "http://10.10.10.1/config", "messageId":"%s","method":"%s","namespace":"%s","payloadVersion":1,"sign":"%s","timestamp":0},"payload":%s}`,
	Endpoints: []*endpoint{
		{Code: "toggle", SupportedDevices: []string{"radiator"}, MinValue: 0, MaxValue: 1, Namespace: "Appliance.Hub.ToggleX", Template: `{"channel":0,"id":"%s","onoff":%s}`},
		{Code: "mode", SupportedDevices: []string{"radiator"}, MinValue: 0, MaxValue: 4, Namespace: "Appliance.Hub.Mts100.Mode", Template: `{"id":"%s","state":%s}`},
		{Code: "adjust", SupportedDevices: []string{"radiator"}, MinValue: -32767, MaxValue: 32767, Namespace: "Appliance.Hub.Mts100.Adjust", Template: `{"id":"%s","temperature":%s}`},
		{Code: "status", SupportedDevices: []string{"radiator"}, Namespace: "Appliance.Hub.Mts100.All", Template: `{"id":"%s","dummy":%s}`},
		{Code: "battery", SupportedDevices: []string{"radiator"}, Namespace: "Appliance.Hub.Battery", Template: `{"id":"%s","dummy":%s}`},
	},
}

// baseEndpoint provides default implementations for metadata access,
// validation, payload building, and default GET/SET handling. Specific
// endpoints can embed/override this behaviour.
type baseEndpoint struct{ e *endpoint }

func (be baseEndpoint) Code() string      { return be.e.Code }
func (be baseEndpoint) Namespace() string { return be.e.Namespace }
func (be baseEndpoint) Template() string  { return be.e.Template }
func (be baseEndpoint) Supports(deviceType string) bool {
	return slices.Contains(be.e.SupportedDevices, deviceType)
}
func (be baseEndpoint) ValidateValue(value json.Number) error {
	if value == "" {
		return nil
	}
	if be.e.MaxValue == 0 && be.e.MinValue == 0 {
		return nil
	}
	v, err := value.Int64()
	if err != nil {
		return err
	}
	if v < be.e.MinValue || v > be.e.MaxValue {
		return fmt.Errorf("value out of range (Min: %d, Max: %d)", be.e.MinValue, be.e.MaxValue)
	}
	return nil
}
func (be baseEndpoint) BuildPayload(ids []string, value json.Number) string {
	var payload strings.Builder
	for i, id := range ids {
		payload.WriteString(fmt.Sprintf(be.e.Template, id, string(value)))
		if i < len(ids)-1 {
			payload.WriteString(",")
		}
	}
	return payload.String()
}

func (be baseEndpoint) ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error) {
	return nil, fmt.Errorf("not implemented")
}
func (be baseEndpoint) ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error) {
	return nil, fmt.Errorf("not implemented")
}

// DefaultHandleMeross is the shared implementation used by endpoint types
// that don't need bespoke SET/GET handling. It calls into the endpoint
// via the EndpointIF interface so ProcessGetForMeross is dynamically
// dispatched to the concrete endpoint implementation.
func DefaultHandleMeross(ep EndpointIF, m *meross, req device.Request) (int, any, string, error) {
	method := "SET"
	if req.Value == "" {
		method = "GET"
		req.Value = toJsonNumber(0)
	}

	payload := m.buildPayload(ep.Template(), req.Value)
	raw, err := m.post(method, ep.Namespace(), payload)
	if err != nil {
		return 0, nil, "", err
	}
	if method == "SET" {
		return http.StatusOK, nil, "OK", nil
	}
	data, err := ep.ProcessGetForMeross(raw, m)
	if err != nil {
		return http.StatusNotImplemented, nil, "Not Implemented", nil
	}
	return http.StatusOK, data, "OK", nil
}

// DefaultHandleBase is the base implementation for multi-device requests.
// It constructs a combined payload for all devices and calls ProcessGetForBase
// on the endpoint interface so concrete types are invoked.
func DefaultHandleBase(ep EndpointIF, b *base, devices []*meross, req device.Request) (int, any, string, error) {
	method := "SET"
	if req.Value == "" {
		method = "GET"
		req.Value = toJsonNumber(0)
	}

	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(ep.Template(), req.Value))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}
	raw, err := b.post(devices[0].Host, method, ep.Namespace(), payload.String(), devices[0].Key, devices[0].Timeout)
	if err != nil {
		return 0, nil, "", err
	}
	if method == "SET" {
		return http.StatusOK, nil, "OK", nil
	}
	data, err := ep.ProcessGetForBase(raw, b)
	if err != nil {
		return http.StatusNotImplemented, nil, "Not Implemented", nil
	}
	return http.StatusOK, data, "OK", nil
}

func (be baseEndpoint) HandleMeross(m *meross, req device.Request) (int, any, string, error) {
	method := "SET"
	if req.Value == "" {
		method = "GET"
		req.Value = toJsonNumber(0)
	}

	payload := m.buildPayload(be.Template(), req.Value)
	raw, err := m.post(method, be.Namespace(), payload)
	if err != nil {
		return 0, nil, "", err
	}
	if method == "SET" {
		return http.StatusOK, nil, "OK", nil
	}
	data, err := be.ProcessGetForMeross(raw, m)
	if err != nil {
		return http.StatusNotImplemented, nil, "Not Implemented", nil
	}
	return http.StatusOK, data, "OK", nil
}

func (be baseEndpoint) HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error) {
	method := "SET"
	if req.Value == "" {
		method = "GET"
		req.Value = toJsonNumber(0)
	}

	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(be.Template(), req.Value))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}
	raw, err := b.post(devices[0].Host, method, be.Namespace(), payload.String(), devices[0].Key, devices[0].Timeout)
	if err != nil {
		return 0, nil, "", err
	}
	if method == "SET" {
		return http.StatusOK, nil, "OK", nil
	}
	data, err := be.ProcessGetForBase(raw, b)
	if err != nil {
		return http.StatusNotImplemented, nil, "Not Implemented", nil
	}
	return http.StatusOK, data, "OK", nil
}

// toggleEndpoint implements the behaviour for the 'toggle' endpoint.
type toggleEndpoint struct{ baseEndpoint }

func (te toggleEndpoint) ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error) {
	var status []any
	for i := range raw.Payload.All {
		status = append(status, &statusGet{Id: &raw.Payload.All[i].ID})
	}
	return status, nil
}

func (te toggleEndpoint) ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error) {
	var status []*namedStatus
	for i := range raw.Payload.All {
		status = append(status, &namedStatus{Name: b.getDeviceById(raw.Payload.All[i].ID).Name, Status: &statusGet{Id: &raw.Payload.All[i].ID}})
	}
	return status, nil
}

func (te toggleEndpoint) HandleMeross(m *meross, req device.Request) (int, any, string, error) {
	if req.Value == "" {
		statusEp := m.getEndpoint("status")
		payload := m.buildPayload(statusEp.Template(), toJsonNumber(0))
		raw, err := m.post("GET", statusEp.Namespace(), payload)
		if err != nil {
			return 0, nil, "", err
		}
		req.Value = toJsonNumber(1 - raw.Payload.All[0].Togglex.Onoff)
	}
	payload := m.buildPayload(te.Template(), req.Value)
	_, err := m.post("SET", te.Namespace(), payload)
	if err != nil {
		return 0, nil, "", err
	}
	return http.StatusOK, nil, "OK", nil
}

func (te toggleEndpoint) HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error) {
	if req.Value == "" {
		req.Value = toJsonNumber(0)
		var payload strings.Builder
		for i, m := range devices {
			payload.WriteString(m.buildPayload(b.getDevice(m.Name).getEndpoint("status").Template(), toJsonNumber(0)))
			if i < len(devices)-1 {
				payload.WriteString(",")
			}
		}
		raw, err := b.post(devices[0].Host, "GET", b.getDevice(devices[0].Name).getEndpoint("status").Namespace(), payload.String(), devices[0].Key, devices[0].Timeout)
		if err != nil {
			return 0, nil, "", err
		}
		valueTally := int64(0)
		for _, s := range raw.Payload.All {
			valueTally += s.Togglex.Onoff
		}
		if valueTally <= int64(len(devices))/2 {
			req.Value = toJsonNumber(1)
		}
	}
	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(te.Template(), req.Value))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}
	_, err := b.post(devices[0].Host, "SET", te.Namespace(), payload.String(), devices[0].Key, devices[0].Timeout)
	if err != nil {
		return 0, nil, "", err
	}
	return http.StatusOK, nil, "OK", nil
}

// statusEndpoint implements 'status' behaviour for GET conversions.
type statusEndpoint struct{ baseEndpoint }

func (se statusEndpoint) ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error) {
	var status []any
	deviceStates := raw.Payload.All
	for i := range deviceStates {
		heating := deviceStates[i].Temperature.CurrentSet-deviceStates[i].Temperature.Room > 0
		openWindow := deviceStates[i].Temperature.OpenWindow != 0
		status = append(status, &statusGet{
			Id:     &deviceStates[i].ID,
			Onoff:  &deviceStates[i].Togglex.Onoff,
			Mode:   &deviceStates[i].Mode.State,
			Online: &deviceStates[i].Online.Status,
			Temperature: &temperature{
				Current:    &deviceStates[i].Temperature.Room,
				Target:     &deviceStates[i].Temperature.CurrentSet,
				Heating:    &heating,
				OpenWindow: &openWindow,
			},
		})
	}
	return status, nil
}

func (se statusEndpoint) ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error) {
	var status []*namedStatus
	deviceStates := raw.Payload.All
	for i := range deviceStates {
		heating := deviceStates[i].Temperature.CurrentSet-deviceStates[i].Temperature.Room > 0
		openWindow := deviceStates[i].Temperature.OpenWindow != 0
		status = append(status, &namedStatus{
			Name: b.getDeviceById(deviceStates[i].ID).Name,
			Status: &statusGet{
				Id:     &deviceStates[i].ID,
				Onoff:  &deviceStates[i].Togglex.Onoff,
				Mode:   &deviceStates[i].Mode.State,
				Online: &deviceStates[i].Online.Status,
				Temperature: &temperature{
					Current:    &deviceStates[i].Temperature.Room,
					Target:     &deviceStates[i].Temperature.CurrentSet,
					Heating:    &heating,
					OpenWindow: &openWindow,
				},
			},
		})
	}

	return status, nil
}

func (se statusEndpoint) HandleMeross(m *meross, req device.Request) (int, any, string, error) {
	return DefaultHandleMeross(se, m, req)
}
func (se statusEndpoint) HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error) {
	return DefaultHandleBase(se, b, devices, req)
}

// batteryEndpoint, modeEndpoint and adjustEndpoint all follow a similar
// implementation pattern: convert the specific raw payload arrays into the
// expected response shapes.
type batteryEndpoint struct{ baseEndpoint }

func (be batteryEndpoint) ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error) {
	var status []any
	for i := range raw.Payload.Battery {
		status = append(status, &singleGet{Id: &raw.Payload.Battery[i].ID, Value: &raw.Payload.Battery[i].Value})
	}
	return status, nil
}
func (be batteryEndpoint) ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error) {
	var status []*namedStatus
	for i := range raw.Payload.Battery {
		status = append(status, &namedStatus{Name: b.getDeviceById(raw.Payload.Battery[i].ID).Name, Status: &singleGet{Id: &raw.Payload.Battery[i].ID, Value: &raw.Payload.Battery[i].Value}})
	}
	return status, nil
}
func (be batteryEndpoint) HandleMeross(m *meross, req device.Request) (int, any, string, error) {
	return DefaultHandleMeross(be, m, req)
}
func (be batteryEndpoint) HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error) {
	return DefaultHandleBase(be, b, devices, req)
}

type modeEndpoint struct{ baseEndpoint }

func (me modeEndpoint) ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error) {
	var status []any
	for i := range raw.Payload.Mode {
		status = append(status, &singleGet{Id: &raw.Payload.Mode[i].ID, Value: &raw.Payload.Mode[i].State})
	}
	return status, nil
}
func (me modeEndpoint) ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error) {
	var status []*namedStatus
	for i := range raw.Payload.Mode {
		status = append(status, &namedStatus{Name: b.getDeviceById(raw.Payload.Mode[i].ID).Name, Status: singleGet{Id: &raw.Payload.Mode[i].ID, Value: &raw.Payload.Mode[i].State}})
	}
	return status, nil
}
func (me modeEndpoint) HandleMeross(m *meross, req device.Request) (int, any, string, error) {
	return DefaultHandleMeross(me, m, req)
}
func (me modeEndpoint) HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error) {
	return DefaultHandleBase(me, b, devices, req)
}

type adjustEndpoint struct{ baseEndpoint }

func (ae adjustEndpoint) ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error) {
	var status []any
	for i := range raw.Payload.Adjust {
		status = append(status, &singleGet{Id: &raw.Payload.Adjust[i].ID, Value: &raw.Payload.Adjust[i].Temperature})
	}
	return status, nil
}
func (ae adjustEndpoint) ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error) {
	var status []*namedStatus
	for i := range raw.Payload.Adjust {
		status = append(status, &namedStatus{Name: b.getDeviceById(raw.Payload.Adjust[i].ID).Name, Status: singleGet{Id: &raw.Payload.Adjust[i].ID, Value: &raw.Payload.Adjust[i].Temperature}})
	}
	return status, nil
}
func (ae adjustEndpoint) HandleMeross(m *meross, req device.Request) (int, any, string, error) {
	return DefaultHandleMeross(ae, m, req)
}
func (ae adjustEndpoint) HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error) {
	return DefaultHandleBase(ae, b, devices, req)
}

type genericEndpoint struct{ baseEndpoint }

func (ge genericEndpoint) ProcessGetForMeross(raw *rawStatus, m *meross) ([]any, error) {
	return nil, fmt.Errorf("not implemented")
}
func (ge genericEndpoint) ProcessGetForBase(raw *rawStatus, b *base) ([]*namedStatus, error) {
	return nil, fmt.Errorf("not implemented")
}
func (ge genericEndpoint) HandleMeross(m *meross, req device.Request) (int, any, string, error) {
	return DefaultHandleMeross(ge, m, req)
}
func (ge genericEndpoint) HandleBase(b *base, devices []*meross, req device.Request) (int, any, string, error) {
	return DefaultHandleBase(ge, b, devices, req)
}

func getConcreteEndpoint(e *endpoint) EndpointIF {
	switch e.Code {
	case "toggle":
		return &toggleEndpoint{baseEndpoint{e}}
	case "status":
		return &statusEndpoint{baseEndpoint{e}}
	case "battery":
		return &batteryEndpoint{baseEndpoint{e}}
	case "mode":
		return &modeEndpoint{baseEndpoint{e}}
	case "adjust":
		return &adjustEndpoint{baseEndpoint{e}}
	default:
		return &genericEndpoint{baseEndpoint{e}}
	}
}

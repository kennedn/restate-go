package msh300hk

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
)

// ---------------------------
// Handler Plumbing
// ---------------------------

// wireHandlers binds Handler implementations to each endpoint based on its Code.
func (b *base) wireHandlers() {
	for _, ep := range b.Endpoints {
		switch ep.Code {
		case "status":
			ep.Handler = StatusHandler{}
		case "toggle":
			ep.Handler = ToggleHandler{}
		case "battery":
			ep.Handler = BatteryHandler{}
		case "mode":
			ep.Handler = ModeHandler{Min: 0, Max: 4} // adjust range if your devices differ
		case "adjust":
			ep.Handler = AdjustHandler{Min: -32767, Max: 32767} // typical TRV target range
		default:
			log.Fatalf("Unhandled endpoint code '%s' in msh300hk device", ep.Code)
		}
	}
}

// ---------------------------
// Endpoint Handlers (bespoke validation here)
// ---------------------------

// CodeValueRequest is the generic request shape for endpoints that accept an optional value.
type CodeValueRequest struct {
	Code  string      `json:"code" schema:"code"`
	Value json.Number `json:"value,omitempty" schema:"value"`
	Hosts string      `json:"hosts,omitempty" schema:"hosts"`
}

// StatusHandler: GET-only, returns flattened status.
type StatusHandler struct{}

func (h StatusHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	ep := m.getEndpoint("status")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	payload := m.buildPayload(ep.Template, toJsonNumber(0))
	raw, err := m.post("GET", ep.Namespace, payload)
	if err != nil {
		return nil, err
	}

	deviceStates := raw.Payload.All
	out := make([]any, 0, len(deviceStates))
	for i := range deviceStates {
		heating := deviceStates[i].Temperature.CurrentSet-deviceStates[i].Temperature.Room > 0
		openWindow := deviceStates[i].Temperature.OpenWindow != 0
		out = append(out, &statusGet{
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

	return out, nil
}

func (h StatusHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	m0 := devices[0]
	ep := m0.getEndpoint("status")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(ep.Template, toJsonNumber(0)))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}

	raw, err := b.post(m0.Host, "GET", ep.Namespace, payload.String(), m0.Key, m0.Timeout)
	if err != nil {
		return nil, err
	}

	deviceStates := raw.Payload.All
	out := make([]*namedStatus, 0, len(deviceStates))
	for i := range deviceStates {
		heating := deviceStates[i].Temperature.CurrentSet-deviceStates[i].Temperature.Room > 0
		openWindow := deviceStates[i].Temperature.OpenWindow != 0

		dev := b.getDeviceById(deviceStates[i].ID)
		name := deviceStates[i].ID
		if dev != nil {
			name = dev.Name
		}

		out = append(out, &namedStatus{
			Name: name,
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

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// BatteryHandler: GET-only.
type BatteryHandler struct{}

func (h BatteryHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	ep := m.getEndpoint("battery")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	payload := m.buildPayload(ep.Template, toJsonNumber(0))
	raw, err := m.post("GET", ep.Namespace, payload)
	if err != nil {
		return nil, err
	}

	deviceStates := raw.Payload.Battery
	out := make([]any, 0, len(deviceStates))
	for i := range deviceStates {
		out = append(out, &singleGet{
			Id:    &deviceStates[i].ID,
			Value: &deviceStates[i].Value,
		})
	}
	return out, nil
}

func (h BatteryHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	m0 := devices[0]
	ep := m0.getEndpoint("battery")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(ep.Template, toJsonNumber(0)))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}

	raw, err := b.post(m0.Host, "GET", ep.Namespace, payload.String(), m0.Key, m0.Timeout)
	if err != nil {
		return nil, err
	}

	deviceStates := raw.Payload.Battery
	out := make([]*namedStatus, 0, len(deviceStates))
	for i := range deviceStates {
		dev := b.getDeviceById(deviceStates[i].ID)
		name := deviceStates[i].ID
		if dev != nil {
			name = dev.Name
		}
		out = append(out, &namedStatus{
			Name: name,
			Status: &singleGet{
				Id:    &deviceStates[i].ID,
				Value: &deviceStates[i].Value,
			},
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ToggleHandler: optional value (0/1). If absent, toggles current state.
// Validation is bespoke here.
type ToggleHandler struct{}

func (h ToggleHandler) validateValue(v json.Number) (json.Number, error) {
	if v == "" {
		return "", nil
	}
	i, err := v.Int64()
	if err != nil || (i != 0 && i != 1) {
		return "", fmt.Errorf("invalid value (expected 0 or 1)")
	}
	return v, nil
}

func (h ToggleHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validateValue(req.Value)
	if err != nil {
		return nil, err
	}

	if val == "" {
		// GET status for this device and invert first id's onoff.
		statusEp := m.getEndpoint("status")
		payload := m.buildPayload(statusEp.Template, toJsonNumber(0))
		raw, err := m.post("GET", statusEp.Namespace, payload)
		if err != nil {
			return nil, err
		}
		val = toJsonNumber(1 - raw.Payload.All[0].Togglex.Onoff)
	}

	toggleEp := m.getEndpoint("toggle")
	payload := m.buildPayload(toggleEp.Template, val)
	if _, err := m.post("SET", toggleEp.Namespace, payload); err != nil {
		return nil, err
	}
	return nil, nil
}

func (h ToggleHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validateValue(req.Value)
	if err != nil {
		return nil, err
	}

	m0 := devices[0]

	if val == "" {
		// Vote-based default toggle
		statusEp := m0.getEndpoint("status")

		var payload strings.Builder
		for i, m := range devices {
			payload.WriteString(m.buildPayload(statusEp.Template, toJsonNumber(0)))
			if i < len(devices)-1 {
				payload.WriteString(",")
			}
		}

		raw, err := b.post(m0.Host, "GET", statusEp.Namespace, payload.String(), m0.Key, m0.Timeout)
		if err != nil {
			return nil, err
		}

		valueTally := int64(0)
		for _, s := range raw.Payload.All {
			valueTally += s.Togglex.Onoff
		}

		if valueTally <= int64(len(devices))/2 {
			val = toJsonNumber(1)
		} else {
			val = toJsonNumber(0)
		}
	}

	toggleEp := m0.getEndpoint("toggle")
	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(toggleEp.Template, val))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}

	if _, err := b.post(m0.Host, "SET", toggleEp.Namespace, payload.String(), m0.Key, m0.Timeout); err != nil {
		return nil, err
	}

	return nil, nil
}

// ModeHandler: GET if no value, SET if value present. Bespoke range validation.
type ModeHandler struct {
	Min int64
	Max int64
}

func (h ModeHandler) validate(v json.Number) (json.Number, error) {
	if v == "" {
		return "", nil
	}
	i, err := v.Int64()
	if err != nil || i < h.Min || i > h.Max {
		return "", fmt.Errorf("invalid value (min %d, max %d)", h.Min, h.Max)
	}
	return v, nil
}

func (h ModeHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	ep := m.getEndpoint("mode")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	method := "SET"
	if val == "" {
		method = "GET"
		val = toJsonNumber(0)
	}

	payload := m.buildPayload(ep.Template, val)
	raw, err := m.post(method, ep.Namespace, payload)
	if err != nil {
		return nil, err
	}
	if method == "SET" {
		return nil, nil
	}

	deviceStates := raw.Payload.Mode
	out := make([]any, 0, len(deviceStates))
	for i := range deviceStates {
		out = append(out, &singleGet{
			Id:    &deviceStates[i].ID,
			Value: &deviceStates[i].State,
		})
	}
	return out, nil
}

func (h ModeHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	m0 := devices[0]
	ep := m0.getEndpoint("mode")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	method := "SET"
	if val == "" {
		method = "GET"
		val = toJsonNumber(0)
	}

	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(ep.Template, val))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}

	raw, err := b.post(m0.Host, method, ep.Namespace, payload.String(), m0.Key, m0.Timeout)
	if err != nil {
		return nil, err
	}

	if method == "SET" {
		return nil, nil
	}

	deviceStates := raw.Payload.Mode
	out := make([]*namedStatus, 0, len(deviceStates))
	for i := range deviceStates {
		dev := b.getDeviceById(deviceStates[i].ID)
		name := deviceStates[i].ID
		if dev != nil {
			name = dev.Name
		}
		out = append(out, &namedStatus{
			Name: name,
			Status: &singleGet{
				Id:    &deviceStates[i].ID,
				Value: &deviceStates[i].State,
			},
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AdjustHandler: target temperature. Bespoke range validation.
type AdjustHandler struct {
	Min int64
	Max int64
}

func (h AdjustHandler) validate(v json.Number) (json.Number, error) {
	if v == "" {
		return "", nil
	}
	i, err := v.Int64()
	if err != nil || i < h.Min || i > h.Max {
		return "", fmt.Errorf("invalid value (min %d, max %d)", h.Min, h.Max)
	}
	return v, nil
}

func (h AdjustHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	ep := m.getEndpoint("adjust")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	method := "SET"
	if val == "" {
		method = "GET"
		val = toJsonNumber(0)
	}

	payload := m.buildPayload(ep.Template, val)
	raw, err := m.post(method, ep.Namespace, payload)
	if err != nil {
		return nil, err
	}
	if method == "SET" {
		return nil, nil
	}

	deviceStates := raw.Payload.Adjust
	out := make([]any, 0, len(deviceStates))
	for i := range deviceStates {
		out = append(out, &singleGet{
			Id:    &deviceStates[i].ID,
			Value: &deviceStates[i].Temperature,
		})
	}
	return out, nil
}

func (h AdjustHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	m0 := devices[0]
	ep := m0.getEndpoint("adjust")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	method := "SET"
	if val == "" {
		method = "GET"
		val = toJsonNumber(0)
	}

	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(ep.Template, val))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}

	raw, err := b.post(m0.Host, method, ep.Namespace, payload.String(), m0.Key, m0.Timeout)
	if err != nil {
		return nil, err
	}

	if method == "SET" {
		return nil, nil
	}

	deviceStates := raw.Payload.Adjust
	out := make([]*namedStatus, 0, len(deviceStates))
	for i := range deviceStates {
		dev := b.getDeviceById(deviceStates[i].ID)
		name := deviceStates[i].ID
		if dev != nil {
			name = dev.Name
		}
		out = append(out, &namedStatus{
			Name: name,
			Status: &singleGet{
				Id:    &deviceStates[i].ID,
				Value: &deviceStates[i].Temperature,
			},
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

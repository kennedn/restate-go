package mts200b

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"errors"
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
		case "mode":
			ep.Handler = ModeHandler{Min: 0, Max: 4}
		case "heatTemp":
			ep.Handler = HeatTempHandler{Min: 50, Max: 350}
		default:
			log.Fatalf("unknown endpoint code %q", ep.Code)
		}
	}
}

// ---------------------------
// Endpoint Handlers
// ---------------------------

// CodeValueRequest is the generic request shape for endpoints that accept an optional value.
type CodeValueRequest struct {
	Code  string      `json:"code" schema:"code"`
	Value json.Number `json:"value,omitempty" schema:"value"`
	Hosts string      `json:"hosts,omitempty" schema:"hosts"`
}

// StatusHandler implements "status" behavior for single and multi device.
type StatusHandler struct{}

func (h StatusHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	rawResponse, err := m.post("GET", *m.getEndpoint("status"), "")
	if err != nil {
		return nil, err
	}
	heating := rawResponse.Payload.All.Digest.Thermostat.Mode[0].TargetTemp-rawResponse.Payload.All.Digest.Thermostat.Mode[0].CurrentTemp > 0
	openWindow := rawResponse.Payload.All.Digest.Thermostat.WindowOpened[0].Status != 0
	response := status{
		Onoff: &rawResponse.Payload.All.Digest.Thermostat.Mode[0].Onoff,
		Mode:  &rawResponse.Payload.All.Digest.Thermostat.Mode[0].Mode,
		Temperature: &temperature{
			Current:    &rawResponse.Payload.All.Digest.Thermostat.Mode[0].CurrentTemp,
			Target:     &rawResponse.Payload.All.Digest.Thermostat.Mode[0].TargetTemp,
			Heating:    &heating,
			OpenWindow: &openWindow,
		},
	}
	return response, nil
}

func (h StatusHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	return nil, errors.New("not implemented")
}

// ToggleHandler implements "toggle" behavior for single and multi device.
// Validation is endpoint-specific here.
type ToggleHandler struct{}

func (h ToggleHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	if req.Value != "" {
		v, err := req.Value.Int64()
		if err != nil || (v != 0 && v != 1) {
			return nil, fmt.Errorf("invalid value (expected 0 or 1)")
		}
	} else {
		rawResponse, err := m.post("GET", *m.getEndpoint("status"), "")
		if err != nil {
			return nil, err
		}
		req.Value = toJsonNumber(1 - rawResponse.Payload.All.Digest.Thermostat.Mode[0].Onoff)
	}

	ep := m.getEndpoint("toggle")
	_, err := m.post("SET", *ep, req.Value)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (h ToggleHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	return nil, errors.New("not implemented")
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

	out, err := m.post(method, *ep, val)
	if err != nil {
		return nil, err
	}
	if method == "SET" {
		return nil, nil
	}

	response := map[string]any{
		"mode": out.Payload.Mode[0].Mode,
	}

	return response, nil
}

func (h ModeHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	return nil, errors.New("not implemented")
}

// HeatTempHandler: GET if no value, SET if value present. Bespoke range validation.
type HeatTempHandler struct {
	Min int64
	Max int64
}

func (h HeatTempHandler) validate(v json.Number) (json.Number, error) {
	if v == "" {
		return "", nil
	}
	i, err := v.Int64()
	if err != nil || i < h.Min || i > h.Max {
		return "", fmt.Errorf("invalid value (min %d, max %d)", h.Min, h.Max)
	}
	return v, nil
}

func (h HeatTempHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	ep := m.getEndpoint("heatTemp")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	method := "SET"
	if val == "" {
		method = "GET"
		val = toJsonNumber(0)
	}

	out, err := m.post(method, *ep, val)
	if err != nil {
		return nil, err
	}
	if method == "SET" {
		return nil, nil
	}

	response := map[string]any{
		"heatTemp": out.Payload.Mode[0].HeatTemp,
	}

	return response, nil
}

func (h HeatTempHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	return nil, errors.New("not implemented")
}

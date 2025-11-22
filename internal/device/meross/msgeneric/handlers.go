package msgeneric

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"gopkg.in/yaml.v3"
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
		case "fade":
			ep.Handler = FadeHandler{}
		case "luminance":
			ep.Handler = NumericHandler{Code: "luminance", Min: 0, Max: 100}
		case "temperature":
			ep.Handler = NumericHandler{Code: "temperature", Min: 0, Max: 100}
		case "rgb":
			ep.Handler = NumericHandler{Code: "rgb", Min: 0, Max: 16777215}
		default:
			ep.Handler = DefaultHandler{}
		}
	}
}

// ---------------------------
// Endpoint Handlers
// ---------------------------

// DefaultRequest is the generic per-endpoint request shape (now endpoint-specific).
type DefaultRequest struct {
	Code  string      `json:"code" schema:"code"`
	Value json.Number `json:"value,omitempty" schema:"value"`
}

// DefaultHandler implements the existing "default:" behavior (requires value, no range check).
type DefaultHandler struct{}

func (h DefaultHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := DefaultRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	ep := m.getEndpoint(req.Code)
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	if req.Value == "" {
		return nil, fmt.Errorf("invalid value")
	}

	_, err := m.post("SET", *ep, req.Value)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (h DefaultHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := DefaultRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	if req.Value == "" {
		return nil, fmt.Errorf("invalid value")
	}

	responses := b.multiPost(devices, "SET", req.Code, req.Value)

	ok := 0
	for ns := range responses {
		if ns.Status != nil {
			ok++
		}
	}
	if ok == 0 {
		return nil, fmt.Errorf("all devices errored")
	}

	return nil, nil
}

// StatusHandler implements "status" behavior for single and multi device.
type StatusHandler struct{}

func (h StatusHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	st, err := m.post("GET", *m.getEndpoint("status"), "")
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (h StatusHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	responses := b.multiPost(devices, "GET", "status", "")

	responseStruct := struct {
		Devices []*namedStatus `json:"devices,omitempty"`
		Errors  []string       `json:"errors,omitempty"`
	}{}

	for ns := range responses {
		if ns.Status == nil {
			responseStruct.Errors = append(responseStruct.Errors, ns.Name)
			continue
		}
		responseStruct.Devices = append(responseStruct.Devices, ns)
	}

	sort.SliceStable(responseStruct.Devices, func(i int, j int) bool {
		return responseStruct.Devices[i].Name < responseStruct.Devices[j].Name
	})

	return responseStruct, nil
}

// ToggleRequest is a bespoke request shape for toggle endpoints.
type ToggleRequest struct {
	Code  string      `json:"code" schema:"code"`
	Value json.Number `json:"value,omitempty" schema:"value"`
}

// ToggleHandler implements "toggle" behavior for single and multi device.
// Validation is endpoint-specific here.
type ToggleHandler struct{}

func (h ToggleHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := ToggleRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	if req.Value != "" {
		v, err := req.Value.Int64()
		if err != nil || (v != 0 && v != 1) {
			return nil, fmt.Errorf("invalid value (expected 0 or 1)")
		}
	} else {
		st, err := m.post("GET", *m.getEndpoint("status"), "")
		if err != nil {
			return nil, err
		}
		req.Value = toJsonNumber(1 - st.Onoff)
	}

	ep := m.getEndpoint("toggle")
	_, err := m.post("SET", *ep, req.Value)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (h ToggleHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := ToggleRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	valueTally := int64(0)

	if req.Value != "" {
		v, err := req.Value.Int64()
		if err != nil || (v != 0 && v != 1) {
			return nil, fmt.Errorf("invalid value (expected 0 or 1)")
		}
	} else {
		// Vote-based default toggle state.
		responses := b.multiPost(devices, "GET", "status", "")
		filtered := make([]*meross, 0, len(devices))

		for ns := range responses {
			if ns.Status == nil {
				continue
			}

			filtered = append(filtered, b.getDevice(ns.Name))

			var st *status
			yml, err := yaml.Marshal(ns.Status)
			if err != nil {
				return nil, err
			}
			if err := yaml.Unmarshal(yml, &st); err != nil {
				return nil, err
			}
			valueTally += st.Onoff
		}

		devices = filtered
		if len(devices) == 0 {
			return nil, fmt.Errorf("all devices errored")
		}

		if valueTally <= int64(len(devices))/2 {
			req.Value = toJsonNumber(1)
		} else {
			req.Value = toJsonNumber(0)
		}
	}

	responses := b.multiPost(devices, "SET", "toggle", req.Value)

	ok := 0
	for ns := range responses {
		if ns.Status != nil {
			ok++
		}
	}
	if ok == 0 {
		return nil, fmt.Errorf("all devices errored")
	}

	return nil, nil
}

// NumericRequest is used for endpoints that accept a numeric value.
type NumericRequest struct {
	Code  string      `json:"code" schema:"code"`
	Value json.Number `json:"value" schema:"value"`
}

// NumericHandler validates a numeric range per-endpoint and then performs SET.
type NumericHandler struct {
	Code string
	Min  int64
	Max  int64
}

func (h NumericHandler) validate(req NumericRequest) (json.Number, error) {
	if req.Value == "" {
		return "", fmt.Errorf("invalid value")
	}
	v, err := req.Value.Int64()
	if err != nil || v < h.Min || v > h.Max {
		return "", fmt.Errorf("invalid value (min %d, max %d)", h.Min, h.Max)
	}
	return req.Value, nil
}

func (h NumericHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := NumericRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req)
	if err != nil {
		return nil, err
	}

	ep := m.getEndpoint(h.Code)
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	_, err = m.post("SET", *ep, val)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (h NumericHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := NumericRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req)
	if err != nil {
		return nil, err
	}

	responses := b.multiPost(devices, "SET", h.Code, val)

	ok := 0
	for ns := range responses {
		if ns.Status != nil {
			ok++
		}
	}
	if ok == 0 {
		return nil, fmt.Errorf("all devices errored")
	}

	return nil, nil
}

// FadeHandler implements "fade" behavior for single and multi device (no value required).
type FadeHandler struct{}

func (h FadeHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	if _, err := m.post("SET", *m.getEndpoint("toggle"), toJsonNumber(0)); err != nil {
		return nil, err
	}
	if _, err := m.post("SET", *m.getEndpoint("fade"), toJsonNumber(-1)); err != nil {
		return nil, err
	}
	return nil, nil
}

func (h FadeHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	responses := b.multiPost(devices, "SET", "toggle", toJsonNumber(0))

	okDevices := make([]*meross, 0, len(devices))
	for ns := range responses {
		if ns.Status != nil {
			okDevices = append(okDevices, b.getDevice(ns.Name))
		}
	}
	if len(okDevices) == 0 {
		return nil, fmt.Errorf("all devices errored")
	}

	responses = b.multiPost(okDevices, "SET", "fade", toJsonNumber(-1))

	ok := 0
	for ns := range responses {
		if ns.Status != nil {
			ok++
		}
	}
	if ok == 0 {
		return nil, fmt.Errorf("all devices errored")
	}

	return nil, nil
}

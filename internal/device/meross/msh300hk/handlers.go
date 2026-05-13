package msh300hk

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
		case "boost":
			ep.Handler = BoostHandler{Base: b}
		case "schedule":
			ep.Handler = ScheduleHandler{}
		case "heatTemp":
			ep.Handler = HeatTempHandler{Min: 50, Max: 350}
		default:
			log.Fatalf("Unhandled endpoint code '%s' in msh300hk device", ep.Code)
		}
	}
}

// ---------------------------
// Endpoint Handlers (bespoke validation here)
// ---------------------------

var heatingOverridesPath = "/tmp/state/heating-overrides.json"
var heatingOverridesMu sync.Mutex

type heatingOverrideFile struct {
	Boost map[string]string `json:"boost"`
}

// GetHeatingOverridesPath returns the current path for the heating overrides file.
func GetHeatingOverridesPath() string {
	return heatingOverridesPath
}

// WithHeatingOverridesLock runs fn while holding the heating overrides mutex.
func WithHeatingOverridesLock(fn func() error) error {
	heatingOverridesMu.Lock()
	defer heatingOverridesMu.Unlock()
	return fn()
}

func collectTargets(devices []*meross) []string {
	seen := make(map[string]struct{})
	targets := make([]string, 0)
	for _, device := range devices {
		for _, id := range device.Ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			targets = append(targets, id)
		}
	}
	return targets
}

// SetHeatingOverridesPath allows tests to override the default path used
// for storing heating override state.
func SetHeatingOverridesPath(p string) {
	heatingOverridesPath = p
}

func writeHeatingOverrides(targets []string, expires time.Time) error {
	if len(targets) == 0 {
		return fmt.Errorf("no targets provided")
	}

	heatingOverridesMu.Lock()
	defer heatingOverridesMu.Unlock()

	dirPath := filepath.Dir(heatingOverridesPath)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return err
	}

	payload := heatingOverrideFile{Boost: map[string]string{}}
	if existing, err := os.ReadFile(heatingOverridesPath); err == nil && len(existing) > 0 {
		if err := json.Unmarshal(existing, &payload); err != nil {
			return err
		}
		if payload.Boost == nil {
			payload.Boost = map[string]string{}
		}
	}

	for _, target := range targets {
		payload.Boost[target] = expires.UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return os.WriteFile(heatingOverridesPath, data, 0o644)
}

// AddHeatingOverrides is an exported helper that appends/sets overrides atomically.
func AddHeatingOverrides(targets []string, expires time.Time) error {
	return writeHeatingOverrides(targets, expires)
}

func (m *meross) scheduleNames() []string {
	if m == nil {
		return nil
	}

	names := make([]string, 0, len(m.Schedules))
	for _, preset := range m.Schedules {
		if preset.Name != "" {
			names = append(names, preset.Name)
		}
	}

	return names
}

func (m *meross) schedulePreset(name string) *schedulePreset {
	for i := range m.Schedules {
		if m.Schedules[i].Name == name {
			return &m.Schedules[i]
		}
	}

	return nil
}

func (p schedulePreset) toPayload() json.Number {
	var b strings.Builder

	writeField := func(name string, v any, first *bool) {
		if !*first {
			b.WriteByte(',')
		}
		*first = false

		b.WriteString(`"`)
		b.WriteString(name)
		b.WriteString(`":`)

		encoded, _ := json.Marshal(v) // ensures valid JSON
		b.Write(encoded)
	}

	first := true

	writeField("mon", p.Mon, &first)
	writeField("tue", p.Tue, &first)
	writeField("wed", p.Wed, &first)
	writeField("thu", p.Thu, &first)
	writeField("fri", p.Fri, &first)
	writeField("sat", p.Sat, &first)
	writeField("sun", p.Sun, &first)

	return json.Number(b.String())
}

func commonScheduleNames(devices []*meross) []string {
	if len(devices) == 0 {
		return nil
	}

	counts := map[string]int{}
	order := make([]string, 0)
	for idx, dev := range devices {
		seen := map[string]struct{}{}
		for _, preset := range dev.Schedules {
			if preset.Name == "" {
				continue
			}
			if _, ok := seen[preset.Name]; ok {
				continue
			}
			seen[preset.Name] = struct{}{}
			counts[preset.Name]++
			if idx == 0 {
				order = append(order, preset.Name)
			}
		}
	}

	common := make([]string, 0)
	for _, name := range order {
		if counts[name] == len(devices) {
			common = append(common, name)
		}
	}

	return common
}

// ScheduleHandler lists configured schedule presets and applies them when selected.
type ScheduleHandler struct {
	Base *base
}

func (h ScheduleHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	if req.Value == "" {
		return m.scheduleNames(), nil
	}

	ep := m.getEndpoint("schedule")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	preset := m.schedulePreset(string(req.Value))
	if preset == nil {
		return nil, fmt.Errorf("invalid value (unknown schedule)")
	}

	payload := m.buildPayload(ep.Template, preset.toPayload())

	if _, err := m.post("SET", ep.Namespace, string(payload), ep.PayloadName); err != nil {
		return nil, err
	}

	return nil, nil
}

func (h ScheduleHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("invalid code")
	}

	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	if req.Value == "" {
		return commonScheduleNames(devices), nil
	}

	m0 := devices[0]

	// validate preset exists on all devices
	for _, dev := range devices {
		ep := dev.getEndpoint("schedule")
		if ep == nil {
			return nil, fmt.Errorf("invalid code")
		}
		if dev.schedulePreset(string(req.Value)) == nil {
			return nil, fmt.Errorf("invalid value (unknown schedule)")
		}
	}

	// build a single payload for all unique target ids, using each device's own preset
	ep := m0.getEndpoint("schedule")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	var payloadBuilder strings.Builder
	first := true
	for _, dev := range devices {
		preset := dev.schedulePreset(string(req.Value))
		if preset == nil {
			return nil, fmt.Errorf("invalid value (unknown schedule)")
		}
		for _, id := range dev.Ids {
			if !first {
				payloadBuilder.WriteString(",")
			}
			first = false
			payloadBuilder.WriteString(fmt.Sprintf(ep.Template, id, string(preset.toPayload())))
		}
	}

	payload := payloadBuilder.String()

	if _, err := b.post(m0.Host, "SET", ep.Namespace, payload, m0.Key, m0.Timeout, ep.PayloadName); err != nil {
		return nil, err
	}

	return nil, nil
}

// StatusHandler: GET-only, returns flattened status.
type StatusHandler struct{}

func (h StatusHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	ep := m.getEndpoint("status")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	payload := m.buildPayload(ep.Template, toJsonNumber(0))
	raw, err := m.post("GET", ep.Namespace, payload, "")
	if err != nil {
		return nil, err
	}

	// also fetch schedules for the ids and compare to configured presets
	scheduleEp := m.getEndpoint("schedule")
	var scheduleMap map[string]map[string]any
	if scheduleEp != nil {
		idsPayload := meross{Ids: m.Ids}.buildIdsPayload()
		schedRaw, err := m.post("GET", scheduleEp.Namespace, idsPayload, scheduleEp.PayloadName)
		if err == nil && schedRaw != nil && len(schedRaw.Payload.Schedule) > 0 {
			scheduleMap = map[string]map[string]any{}
			for _, item := range schedRaw.Payload.Schedule {
				if idv, ok := item["id"].(string); ok {
					scheduleMap[idv] = item
				}
			}
		}
	}

	deviceStates := raw.Payload.All
	// read existing boost overrides (non-destructive)
	heatingOverridesMu.Lock()
	boostPayload := heatingOverrideFile{Boost: map[string]string{}}
	if existing, err := os.ReadFile(heatingOverridesPath); err == nil && len(existing) > 0 {
		_ = json.Unmarshal(existing, &boostPayload)
	}
	heatingOverridesMu.Unlock()

	out := make([]any, 0, len(deviceStates))
	for i := range deviceStates {
		heating := deviceStates[i].Temperature.CurrentSet-deviceStates[i].Temperature.Room > 0
		openWindow := deviceStates[i].Temperature.OpenWindow != 0
		var boostVal *string = nil
		if boostPayload.Boost != nil {
			if v, ok := boostPayload.Boost[deviceStates[i].ID]; ok {
				boostVal = &v
			}
		}
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
				Boost:      boostVal,
			},
			Schedule: func() *string {
				if scheduleMap != nil {
					if s, ok := scheduleMap[deviceStates[i].ID]; ok {
						// compare s (map) to configured schedules
						// make a copy without id to avoid mutating scheduleMap
						copyMap := map[string]any{}
						for k, v := range s {
							if k == "id" {
								continue
							}
							copyMap[k] = v
						}
						if name := matchPreset(copyMap, m.Schedules); name != "" {
							v := name
							return &v
						}
						unknown := "unknown"
						return &unknown
					}
				}
				return nil
			}(),
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

	// fetch schedules for all target ids so we can annotate status with matching schedule name
	var scheduleMap map[string]map[string]any
	scheduleEp := m0.getEndpoint("schedule")
	if scheduleEp != nil {
		ids := collectTargets(devices)
		idsPayload := meross{Ids: ids}.buildIdsPayload()
		schedRaw, err := b.post(m0.Host, "GET", scheduleEp.Namespace, idsPayload, m0.Key, m0.Timeout, scheduleEp.PayloadName)
		if err == nil && schedRaw != nil && len(schedRaw.Payload.Schedule) > 0 {
			scheduleMap = map[string]map[string]any{}
			for _, item := range schedRaw.Payload.Schedule {
				if idv, ok := item["id"].(string); ok {
					scheduleMap[idv] = item
				}
			}
		}
	}

	raw, err := b.post(m0.Host, "GET", ep.Namespace, payload.String(), m0.Key, m0.Timeout, "")
	if err != nil {
		return nil, err
	}

	deviceStates := raw.Payload.All
	// read existing boost overrides (non-destructive)
	heatingOverridesMu.Lock()
	boostPayload := heatingOverrideFile{Boost: map[string]string{}}
	if existing, err := os.ReadFile(heatingOverridesPath); err == nil && len(existing) > 0 {
		_ = json.Unmarshal(existing, &boostPayload)
	}
	heatingOverridesMu.Unlock()

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
				Temperature: func() *temperature {
					var boostVal *string = nil
					if boostPayload.Boost != nil {
						if v, ok := boostPayload.Boost[deviceStates[i].ID]; ok {
							boostVal = &v
						}
					}
					return &temperature{
						Current:    &deviceStates[i].Temperature.Room,
						Target:     &deviceStates[i].Temperature.CurrentSet,
						Heating:    &heating,
						OpenWindow: &openWindow,
						Boost:      boostVal,
					}
				}(),
				Schedule: func() *string {
					if scheduleMap != nil {
						if s, ok := scheduleMap[deviceStates[i].ID]; ok {
							// compare s (map) to configured schedules for this device
							copyMap := map[string]any{}
							for k, v := range s {
								if k == "id" {
									continue
								}
								copyMap[k] = v
							}
							if dev != nil {
								if name := matchPreset(copyMap, dev.Schedules); name != "" {
									return &name
								}
							}
							unknown := "unknown"
							return &unknown
						}
					}
					return nil
				}(),
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
	raw, err := m.post("GET", ep.Namespace, payload, "")
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

	raw, err := b.post(m0.Host, "GET", ep.Namespace, payload.String(), m0.Key, m0.Timeout, "")
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

func (h ToggleHandler) validateValue(v StringNumber) (json.Number, error) {
	if v == "" {
		return "", nil
	}
	i, err := v.Int64()
	if err != nil || (i != 0 && i != 1) {
		return "", fmt.Errorf("invalid value (expected 0 or 1)")
	}
	return toJsonNumber(i), nil
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
		raw, err := m.post("GET", statusEp.Namespace, payload, "")
		if err != nil {
			return nil, err
		}
		val = toJsonNumber(1 - raw.Payload.All[0].Togglex.Onoff)
	}

	toggleEp := m.getEndpoint("toggle")
	payload := m.buildPayload(toggleEp.Template, val)
	if _, err := m.post("SET", toggleEp.Namespace, payload, ""); err != nil {
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

		raw, err := b.post(m0.Host, "GET", statusEp.Namespace, payload.String(), m0.Key, m0.Timeout, "")
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

	if _, err := b.post(m0.Host, "SET", toggleEp.Namespace, payload.String(), m0.Key, m0.Timeout, ""); err != nil {
		return nil, err
	}

	return nil, nil
}

// ModeHandler: GET if no value, SET if value present. Bespoke range validation.
type ModeHandler struct {
	Min int64
	Max int64
}

func (h ModeHandler) validate(v StringNumber) (json.Number, error) {
	if v == "" {
		return "", nil
	}
	i, err := v.Int64()
	if err != nil || i < h.Min || i > h.Max {
		return "", fmt.Errorf("invalid value (min %d, max %d)", h.Min, h.Max)
	}
	return toJsonNumber(i), nil
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
	raw, err := m.post(method, ep.Namespace, payload, "")
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

	raw, err := b.post(m0.Host, method, ep.Namespace, payload.String(), m0.Key, m0.Timeout, "")
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

// HeatTempHandler controls the thermostat heat temperature exposed by the hub.
type HeatTempHandler struct {
	Min int64
	Max int64
}

func (h HeatTempHandler) validate(v StringNumber) (json.Number, error) {
	// heatTemp must be supplied for SET operations; do not treat empty as GET.
	if v == "" {
		return "", fmt.Errorf("invalid value (expected temperature)")
	}
	i, err := v.Int64()
	if err != nil || i < h.Min || i > h.Max {
		return "", fmt.Errorf("invalid value (min %d, max %d)", h.Min, h.Max)
	}
	return toJsonNumber(i), nil
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

	payload := m.buildPayload(ep.Template, val)
	raw, err := m.post(method, ep.Namespace, payload, "")
	if err != nil {
		return nil, err
	}
	if method == "SET" {
		return nil, nil
	}

	// build response from returned payload All -> Temperature.CurrentSet
	deviceStates := raw.Payload.All
	out := make([]any, 0, len(deviceStates))
	for i := range deviceStates {
		out = append(out, &singleGet{
			Id:    &deviceStates[i].ID,
			Value: &deviceStates[i].Temperature.CurrentSet,
		})
	}
	return out, nil
}

func (h HeatTempHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	val, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	m0 := devices[0]
	ep := m0.getEndpoint("heatTemp")
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

	raw, err := b.post(m0.Host, method, ep.Namespace, payload.String(), m0.Key, m0.Timeout, "")
	if err != nil {
		return nil, err
	}

	if method == "SET" {
		return nil, nil
	}

	deviceStates := raw.Payload.All
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
				Value: &deviceStates[i].Temperature.CurrentSet,
			},
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (h AdjustHandler) validate(v StringNumber) (json.Number, error) {
	if v == "" {
		return "", nil
	}
	i, err := v.Int64()
	if err != nil || i < h.Min || i > h.Max {
		return "", fmt.Errorf("invalid value (min %d, max %d)", h.Min, h.Max)
	}
	return toJsonNumber(i), nil
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
	raw, err := m.post(method, ep.Namespace, payload, "")
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

	raw, err := b.post(m0.Host, method, ep.Namespace, payload.String(), m0.Key, m0.Timeout, "")
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

// BoostHandler: set mode 4, then persist a heating override with the requested duration.
type BoostHandler struct {
	Base *base
}

func (h BoostHandler) validate(v StringNumber) (time.Duration, error) {
	if v == "" {
		return 0, fmt.Errorf("invalid value (expected hours)")
	}

	hours, err := strconv.ParseFloat(string(v), 64)
	if err != nil || hours < 0 {
		return 0, fmt.Errorf("invalid value (expected hours)")
	}

	return time.Duration(hours * float64(time.Hour)), nil
}

func (h BoostHandler) HandleSingle(m *meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	duration, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	ep := m.getEndpoint("mode")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	payload := m.buildPayload(ep.Template, toJsonNumber(4))
	if _, err := m.post("SET", ep.Namespace, payload, ""); err != nil {
		return nil, err
	}

	targets := collectTargets([]*meross{m})
	if err := h.writeHeatingOverrides(targets, time.Now().Add(duration)); err != nil {
		return nil, err
	}

	return nil, nil
}

func (h BoostHandler) HandleMulti(b *base, devices []*meross, r *http.Request) (any, error) {
	req := CodeValueRequest{}
	if err := decodeRequest(r, &req); err != nil {
		return nil, err
	}

	duration, err := h.validate(req.Value)
	if err != nil {
		return nil, err
	}

	m0 := devices[0]
	ep := m0.getEndpoint("mode")
	if ep == nil {
		return nil, fmt.Errorf("invalid code")
	}

	var payload strings.Builder
	for i, m := range devices {
		payload.WriteString(m.buildPayload(ep.Template, toJsonNumber(4)))
		if i < len(devices)-1 {
			payload.WriteString(",")
		}
	}

	if _, err := b.post(m0.Host, "SET", ep.Namespace, payload.String(), m0.Key, m0.Timeout, ""); err != nil {
		return nil, err
	}

	targets := collectTargets(devices)
	if err := h.writeHeatingOverrides(targets, time.Now().Add(duration)); err != nil {
		return nil, err
	}

	return nil, nil
}

func (h BoostHandler) writeHeatingOverrides(targets []string, expires time.Time) error {
	if err := writeHeatingOverrides(targets, expires); err != nil {
		return err
	}
	return h.clearExpiredHeatingOverrides()
}

func (h BoostHandler) clearExpiredHeatingOverrides() error {
	return WithHeatingOverridesLock(func() error {
		path := GetHeatingOverridesPath()
		payload := heatingOverrideFile{Boost: map[string]string{}}
		if existing, readErr := os.ReadFile(path); readErr == nil && len(existing) > 0 {
			if err := json.Unmarshal(existing, &payload); err != nil {
				return err
			}
		}

		now := time.Now().UTC()
		expired := make([]string, 0)
		changed := false
		for id, expiresStr := range payload.Boost {
			if expiresStr == "" {
				continue
			}
			expiresAt, err := time.Parse(time.RFC3339, expiresStr)
			if err != nil || !expiresAt.After(now) {
				expired = append(expired, id)
				delete(payload.Boost, id)
				changed = true
			}
		}

		if changed {
			data, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return err
			}
		}

		if len(expired) > 0 {
			return h.revertExpiredHeatingOverridesMode3(expired)
		}

		return nil
	})
}

func (h BoostHandler) revertExpiredHeatingOverridesMode3(ids []string) error {
	if len(ids) == 0 || h.Base == nil {
		return nil
	}

	grouped := map[*meross][]string{}
	for _, id := range ids {
		dev := h.Base.getDeviceById(id)
		if dev == nil {
			continue
		}
		grouped[dev] = append(grouped[dev], id)
	}

	for dev, devIDs := range grouped {
		ep := dev.getEndpoint("mode")
		if ep == nil {
			return fmt.Errorf("invalid code")
		}

		var payload strings.Builder
		for i, id := range devIDs {
			payload.WriteString(fmt.Sprintf(ep.Template, id, string(toJsonNumber(3))))
			if i < len(devIDs)-1 {
				payload.WriteString(",")
			}
		}

		if _, err := h.Base.post(dev.Host, "SET", ep.Namespace, payload.String(), dev.Key, dev.Timeout, ""); err != nil {
			return err
		}
	}

	return nil
}

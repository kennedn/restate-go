package radiator

import (
	"encoding/json"
	"fmt"
	"strings"
)

const BaseTemplate = `{"header":{"from": "http://10.10.10.1/config", "messageId":"%s","method":"%s","namespace":"%s","payloadVersion":1,"sign":"%s","timestamp":0},"payload":%s}`

type DeviceDefinition struct {
	BaseTemplate string
	Endpoints    map[string]*Endpoint
}

type Endpoint struct {
	Code      string
	MinValue  int64
	MaxValue  int64
	Namespace string
	prepare   PrepareFunc
	build     BuildFunc
	parse     ResponseFunc
}

type PrepareFunc func(ctx EndpointContext) (method string, value json.Number, err error)
type BuildFunc func(ids []string, value json.Number) string

type ResponseFunc func(raw *RawStatus, lookup LookupDeviceName) ([]any, error)

type EndpointContext struct {
	HasValue       bool
	RequestedValue json.Number
	FetchStatus    func() (*RawStatus, error)
}

type LookupDeviceName func(id string) string

func (e *Endpoint) Validate(value json.Number) error {
	if e.MaxValue == 0 {
		return nil
	}

	val, err := value.Int64()
	if err != nil {
		return err
	}

	if val < e.MinValue || val > e.MaxValue {
		return fmt.Errorf("invalid value: must be between %d and %d", e.MinValue, e.MaxValue)
	}

	return nil
}

func (e *Endpoint) Prepare(ctx EndpointContext) (string, json.Number, error) {
	return e.prepare(ctx)
}

func (e *Endpoint) BuildPayload(ids []string, value json.Number) string {
	return e.build(ids, value)
}

func (e *Endpoint) Parse(raw *RawStatus, lookup LookupDeviceName) ([]any, error) {
	if e.parse == nil {
		return nil, fmt.Errorf("endpoint %s does not support GET operations", e.Code)
	}
	return e.parse(raw, lookup)
}

func templateBuilder(template string) BuildFunc {
	return func(ids []string, value json.Number) string {
		var payload strings.Builder
		for i, id := range ids {
			payload.WriteString(fmt.Sprintf(template, id, string(value)))
			if i < len(ids)-1 {
				payload.WriteString(",")
			}
		}
		return payload.String()
	}
}

func defaultPrepare(ctx EndpointContext) (string, json.Number, error) {
	if ctx.HasValue {
		return "SET", ctx.RequestedValue, nil
	}
	return "GET", json.Number("0"), nil
}

func Definitions() map[string]DeviceDefinition {
	radiatorEndpoints := map[string]*Endpoint{
		"toggle": {
			Code:      "toggle",
			MinValue:  0,
			MaxValue:  1,
			Namespace: "Appliance.Hub.ToggleX",
			prepare: func(ctx EndpointContext) (string, json.Number, error) {
				if ctx.HasValue {
					return "SET", ctx.RequestedValue, nil
				}

				if ctx.FetchStatus == nil {
					return "", json.Number(""), fmt.Errorf("status fetcher not configured")
				}

				raw, err := ctx.FetchStatus()
				if err != nil {
					return "", json.Number(""), err
				}

				valueTally := int64(0)
				for i := range raw.Payload.All {
					valueTally += raw.Payload.All[i].Togglex.Onoff
				}

				desired := int64(1)
				if valueTally > int64(len(raw.Payload.All))/2 {
					desired = 0
				}
				return "SET", json.Number(fmt.Sprintf("%d", desired)), nil
			},
			build: templateBuilder("{\"channel\":0,\"id\":\"%s\",\"onoff\":%s}"),
		},
		"mode": {
			Code:      "mode",
			MinValue:  0,
			MaxValue:  4,
			Namespace: "Appliance.Hub.Mts100.Mode",
			prepare:   defaultPrepare,
			build:     templateBuilder("{\"id\":\"%s\",\"state\":%s}"),
			parse:     singleValueParser,
		},
		"adjust": {
			Code:      "adjust",
			MinValue:  -32767,
			MaxValue:  32767,
			Namespace: "Appliance.Hub.Mts100.Adjust",
			prepare:   defaultPrepare,
			build:     templateBuilder("{\"id\":\"%s\",\"temperature\":%s}"),
			parse:     singleValueParser,
		},
		"status": {
			Code:      "status",
			Namespace: "Appliance.Hub.Mts100.All",
			prepare:   defaultPrepare,
			build:     templateBuilder("{\"id\":\"%s\",\"dummy\":%s}"),
			parse:     statusParser,
		},
		"battery": {
			Code:      "battery",
			Namespace: "Appliance.Hub.Battery",
			prepare:   defaultPrepare,
			build:     templateBuilder("{\"id\":\"%s\",\"dummy\":%s}"),
			parse:     singleValueParser,
		},
	}

	return map[string]DeviceDefinition{
		"radiator": {
			BaseTemplate: BaseTemplate,
			Endpoints:    radiatorEndpoints,
		},
	}
}

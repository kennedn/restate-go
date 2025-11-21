package radiator

import (
	"encoding/json"
	"fmt"
)

type StatusGet struct {
	Id          *string      `json:"id"`
	Onoff       *int64       `json:"onoff,omitempty"`
	Mode        *int64       `json:"mode,omitempty"`
	Online      *int64       `json:"online,omitempty"`
	Temperature *Temperature `json:"temperature,omitempty"`
}

type SingleGet struct {
	Id    *string `json:"id"`
	Value *int64  `json:"value,omitempty"`
}

type Temperature struct {
	Current    *int64 `json:"current"`
	Target     *int64 `json:"target"`
	Heating    *bool  `json:"heating"`
	OpenWindow *bool  `json:"openWindow"`
}

type NamedStatus struct {
	Name   string `json:"name"`
	Status any    `json:"status"`
}

type RawStatus struct {
	Payload struct {
		Error struct {
			Code   int64  `json:"code,omitempty"`
			Detail string `json:"detail,omitempty"`
		} `json:"error,omitempty"`
		All []struct {
			ID            string `json:"id"`
			ScheduleBMode int64  `json:"scheduleBMode"`
			Online        struct {
				Status         int64 `json:"status"`
				LastActiveTime int64 `json:"lastActiveTime"`
			} `json:"online"`
			Togglex struct {
				Onoff int64 `json:"onoff"`
			} `json:"togglex"`
			TimeSync struct {
				State int64 `json:"state"`
			} `json:"timeSync"`
			Mode struct {
				State int64 `json:"state"`
			} `json:"mode"`
			Temperature struct {
				Room       int64 `json:"room"`
				CurrentSet int64 `json:"currentSet"`
				Heating    int64 `json:"heating"`
				OpenWindow int64 `json:"openWindow"`
			} `json:"temperature"`
		} `json:"all"`
		Battery []struct {
			ID    string `json:"id"`
			Value int64  `json:"value"`
		} `json:"battery"`
		Mode []struct {
			ID    string `json:"id"`
			State int64  `json:"state"`
		} `json:"mode"`
		Adjust []struct {
			ID          string `json:"id"`
			Temperature int64  `json:"temperature"`
		} `json:"adjust"`
	} `json:"payload"`
}

func singleValueParser(raw *RawStatus, lookup LookupDeviceName) ([]any, error) {
	var status []any

	if len(raw.Payload.Battery) > 0 {
		for i := range raw.Payload.Battery {
			entry := &SingleGet{Id: &raw.Payload.Battery[i].ID, Value: &raw.Payload.Battery[i].Value}
			status = appendStatus(status, entry, lookup, raw.Payload.Battery[i].ID)
		}
		return status, nil
	}

	if len(raw.Payload.Mode) > 0 {
		for i := range raw.Payload.Mode {
			entry := &SingleGet{Id: &raw.Payload.Mode[i].ID, Value: &raw.Payload.Mode[i].State}
			status = appendStatus(status, entry, lookup, raw.Payload.Mode[i].ID)
		}
		return status, nil
	}

	if len(raw.Payload.Adjust) > 0 {
		for i := range raw.Payload.Adjust {
			entry := &SingleGet{Id: &raw.Payload.Adjust[i].ID, Value: &raw.Payload.Adjust[i].Temperature}
			status = appendStatus(status, entry, lookup, raw.Payload.Adjust[i].ID)
		}
		return status, nil
	}

	return status, nil
}

func statusParser(raw *RawStatus, lookup LookupDeviceName) ([]any, error) {
	var status []any

	for i := range raw.Payload.All {
		heating := raw.Payload.All[i].Temperature.CurrentSet-raw.Payload.All[i].Temperature.Room > 0
		openWindow := raw.Payload.All[i].Temperature.OpenWindow != 0
		entry := &StatusGet{
			Id:     &raw.Payload.All[i].ID,
			Onoff:  &raw.Payload.All[i].Togglex.Onoff,
			Mode:   &raw.Payload.All[i].Mode.State,
			Online: &raw.Payload.All[i].Online.Status,
			Temperature: &Temperature{
				Current:    &raw.Payload.All[i].Temperature.Room,
				Target:     &raw.Payload.All[i].Temperature.CurrentSet,
				Heating:    &heating,
				OpenWindow: &openWindow,
			},
		}

		status = appendStatus(status, entry, lookup, raw.Payload.All[i].ID)
	}

	return status, nil
}

func appendStatus(status []any, entry any, lookup LookupDeviceName, id string) []any {
	if lookup == nil {
		return append(status, entry)
	}

	return append(status, &NamedStatus{Name: lookup(id), Status: entry})
}

func ToJSONNumber(value any) json.Number {
	return json.Number(fmt.Sprintf("%d", value))
}

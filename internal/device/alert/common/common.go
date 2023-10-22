package common

import "encoding/json"

type Request struct {
	Message  string      `json:"message"`
	Title    string      `json:"title,omitempty"`
	Priority json.Number `json:"priority,omitempty"`
	Token    string      `json:"token,omitempty"`
	User     string      `json:"user,omitempty"`
}

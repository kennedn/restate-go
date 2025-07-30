package common

import "encoding/json"

type Request struct {
	Message          string      `json:"message"`
	Title            string      `json:"title,omitempty"`
	Priority         json.Number `json:"priority,omitempty"`
	Token            string      `json:"token,omitempty"`
	User             string      `json:"user,omitempty"`
	AttachmentBase64 string      `json:"attachment_base64,omitempty"`
	AttachmentType   string      `json:"attachment_type,omitempty"`
	URL              string      `json:"url,omitempty"`
	URLTitle         string      `json:"url_title,omitempty"`
	Sound            string      `json:"sound,omitempty"`
}

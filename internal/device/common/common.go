package common

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Message string `json:"message" schema:"message"`
	Data    any    `json:"data,omitempty" schema:"data,omitempty"`
}

type Request struct {
	Code  string      `json:"code"`
	Value json.Number `json:"value,omitempty"`
	Hosts string      `json:"hosts,omitempty"`
}

type Config struct {
	ApiVersion string    `yaml:"apiVersion"`
	Devices    []Devices `yaml:"devices"`
}

type Devices struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

func JSONResponse(w http.ResponseWriter, httpCode int, jsonResponse []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	w.Write(jsonResponse)
}

func SetJSONResponse(code int, message string, data any) (int, []byte) {
	httpCode := code
	jsonResponse, _ := json.Marshal(&Response{
		Message: message,
		Data:    data,
	})
	return httpCode, jsonResponse
}

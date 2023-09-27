package common

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type Request struct {
	Code string `json:"code"`
}

func JSONResponse(w http.ResponseWriter, httpCode int, jsonResponse []byte) {
	// Respond to the RESTful client with the received data
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

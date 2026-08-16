package ms600

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/kennedn/restate-go/internal/common/logging"
	device "github.com/kennedn/restate-go/internal/device/common"
)

// targetHandler is the Matter equivalent of mts200b's per-endpoint handlers.
// Targets are discovered rather than selected from a static endpoint manifest.
func targetHandler(client matterClient, target routeTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if target.Attribute != nil {
			if r.Method != http.MethodGet {
				respond(w, http.StatusMethodNotAllowed, "Method Not Allowed", nil)
				return
			}
			value, err := client.Read(target.Endpoint, target.Cluster, *target.Attribute)
			if err != nil {
				logging.Log(logging.Error, "%s", err)
				respond(w, http.StatusBadGateway, err.Error(), nil)
				return
			}
			respond(w, http.StatusOK, "OK", value)
			return
		}

		if r.Method != http.MethodPost {
			respond(w, http.StatusMethodNotAllowed, "Method Not Allowed", nil)
			return
		}
		var request struct {
			TLVHex string `json:"tlvHex"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				respond(w, http.StatusBadRequest, "Malformed JSON Body", nil)
				return
			}
		}
		payload, err := hex.DecodeString(request.TLVHex)
		if err != nil {
			respond(w, http.StatusBadRequest, "tlvHex must be hexadecimal Matter TLV", nil)
			return
		}
		if err := client.Invoke(target.Endpoint, target.Cluster, *target.Command, payload); err != nil {
			logging.Log(logging.Error, "%s", err)
			respond(w, http.StatusBadGateway, err.Error(), nil)
			return
		}
		respond(w, http.StatusOK, "OK", nil)
	}
}

func respond(w http.ResponseWriter, code int, message string, data any) {
	status, body := device.SetJSONResponse(code, message, data)
	device.JSONResponse(w, status, body)
}

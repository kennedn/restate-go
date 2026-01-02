package router

import (
	"github.com/kennedn/restate-go/internal/common/logging"
	router "github.com/kennedn/restate-go/internal/router/common"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

func NewRouter(routes []router.Route) *mux.Router {

	router := mux.NewRouter()

	// Enable logging middleware
	router.Use(logging.RequestLogger)

	// Allow CORS via middleware
	router.Use(handlers.CORS(
		handlers.AllowedOrigins([]string{"http://restate-go.default.svc.cluster.local"}),
		handlers.AllowedMethods([]string{"GET", "POST", "OPTIONS"}),
		handlers.AllowedHeaders([]string{"*"}),
	))

	for _, route := range routes {
		router.HandleFunc(route.Path, route.Handler)
	}

	return router
}

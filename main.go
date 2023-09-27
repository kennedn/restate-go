package main

import (
	"log"
	"net/http"
	"restate-go/router"
)

func main() {
	r := router.NewRouter()
	if r == nil {
		log.Fatal("failed to create router")
	}
	log.Println("Server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}

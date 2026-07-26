// Command ssa-study serves the local interactive source-to-SSA study lab.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "loopback address for the study lab")
	flag.Parse()

	server := &http.Server{
		Addr:              *address,
		Handler:           newHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	log.Printf("SSA Study Lab: http://%s", *address)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

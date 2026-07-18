// Command ssa-study serves the local interactive source-to-SSA study lab.
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "loopback address for the study lab")
	flag.Parse()

	log.Printf("SSA Study Lab: http://%s", *address)
	if err := http.ListenAndServe(*address, newHandler()); err != nil {
		log.Fatal(err)
	}
}

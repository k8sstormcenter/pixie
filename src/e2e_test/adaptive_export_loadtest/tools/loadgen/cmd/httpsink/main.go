// httpsink — a minimal HTTP server for the AE load-test data plane.
//
// It exists only to terminate cleanloadgen's counted HTTP requests with a 200
// and zero side effects. No logging, no metrics endpoint, no readiness/liveness
// surface — anything extra would be captured by Pixie and pollute the per-pod
// http_events / conn_stats counts on the sink side. (AE filters to the client
// pod, so the sink's rows are excluded anyway, but keeping it silent removes any
// chance of cross-talk.)
package main

import (
	"net/http"
	"os"
)

func main() {
	addr := ":8080"
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		addr = v
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		panic(err)
	}
}

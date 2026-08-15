// Package server hosts the central Qianshou control server. Per ADR 0001
// it is the sole owner of SQLite and the versioned HTTP/JSON API defined
// by protocol/openapi.yaml. This skeleton proves the binary shape and the
// CI pipeline only; the domain arrives with the M1 delivery issues.
package server

import (
	"flag"
	"fmt"
	"net"
	"net/http"
)

func Serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:41727", "listen address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	// Loopback only. Remote operation arrives with M2-05 behind
	// authentication and TLS; that boundary is deliberate.
	return http.Serve(listener, handler())
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	return mux
}

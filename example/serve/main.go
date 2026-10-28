// Command serve runs the demo site over HTTPS so you can browse it manually
// (directly, or through the portila proxy by configuring your browser).
package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"time"

	"portila/example/site"
	tlsutil "portila/pkg/tls"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	work := flag.Duration("work", 0, "artificial per-asset delay")
	flag.Parse()

	handler, _ := site.New(*work)

	cert, err := tlsutil.GenerateSelfSignedCert(365)
	if err != nil {
		log.Fatalf("cert: %v", err)
	}
	ln, err := tls.Listen("tcp", *addr, &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	log.Printf("Demo site: https://localhost%s", *addr)
	log.Printf("Login accounts: %v", site.Creds)
	log.Printf("Pages: /p/<name>, name in %v", site.Pages)

	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.Serve(ln))
}

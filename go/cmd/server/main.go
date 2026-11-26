package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ms-text-to-speech/internal/tts"
	"ms-text-to-speech/internal/web"
)

func main() {
	// -addr overrides ADDR; -port joins the -addr host when set. Flags win
	// over environment variables, and env vars win over built-in defaults.
	addrDefault := strings.TrimSpace(os.Getenv("ADDR"))
	if addrDefault == "" {
		addrDefault = ":3000"
	}
	addrFlag := flag.String("addr", addrDefault, "listen address, e.g. :3000 or 127.0.0.1:8080 (overrides ADDR)")
	portFlag := flag.String("port", "", "listen port; when set, joins the -addr host (overrides ADDR port)")
	tokenFlag := flag.String("token", "", "access token; when set, enables auth (overrides MS_RA_FORWARDER_TOKEN/TOKEN)")
	publicURLFlag := flag.String("public-url", os.Getenv("PUBLIC_BASE_URL"), "canonical public URL for Legado links (overrides PUBLIC_BASE_URL)")
	// -edge-endpoint overrides EDGE_ENDPOINT; both pin the default Edge
	// synthesis endpoint. Empty or invalid keeps v2-first automatic fallback.
	edgeEndpointFlag := flag.String("edge-endpoint", os.Getenv("EDGE_ENDPOINT"), "default Edge TTS endpoint: v1 or v2 (empty = v2-first automatic fallback)")
	flag.Parse()

	address := *addrFlag
	if p := strings.TrimSpace(*portFlag); p != "" {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			log.Fatalf("invalid -addr %q: %v", address, err)
		}
		address = net.JoinHostPort(host, p)
	}

	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = os.Getenv("MS_RA_FORWARDER_TOKEN")
	}
	if token == "" {
		token = os.Getenv("TOKEN")
	}

	publicURL := *publicURLFlag

	speech := tts.NewService()
	defer speech.Close()
	speech.SetDefaultEdgeEndpoint(*edgeEndpointFlag)

	if token != "" {
		log.Printf("auth enabled")
	} else {
		log.Printf("auth disabled (no token configured)")
	}

	server := &http.Server{
		Addr:              address,
		Handler:           web.New(speech, token, publicURL).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	stopped, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-stopped.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("MS Read Aloud Forwarder Go listening on %s", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

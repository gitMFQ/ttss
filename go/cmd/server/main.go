package main

import (
	"context"
	"flag"
	"log"
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
	address := strings.TrimSpace(os.Getenv("ADDR"))
	if address == "" {
		address = ":3000"
	}
	token := os.Getenv("MS_RA_FORWARDER_TOKEN")
	if token == "" {
		token = os.Getenv("TOKEN")
	}
	publicURL := os.Getenv("PUBLIC_BASE_URL")

	// -edge-endpoint overrides EDGE_ENDPOINT; both pin the default Edge
	// synthesis endpoint. Empty or invalid keeps v2-first automatic fallback.
	edgeEndpointFlag := flag.String("edge-endpoint", os.Getenv("EDGE_ENDPOINT"), "default Edge TTS endpoint: v1 or v2 (empty = v2-first automatic fallback)")
	flag.Parse()

	speech := tts.NewService()
	defer speech.Close()
	speech.SetDefaultEdgeEndpoint(*edgeEndpointFlag)

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

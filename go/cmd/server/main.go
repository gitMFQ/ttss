package main

import (
	"context"
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

	speech := tts.NewService()
	defer speech.Close()

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

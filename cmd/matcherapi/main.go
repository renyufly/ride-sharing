package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	runhttp "ride-sharing/internal/transport/http/run"
	"ride-sharing/shared/env"
	"syscall"
	"time"
)

// HTTP 入口
// 只负责服务启动和依赖装配

var (
	httpAddr = env.GetString("MATCHER_HTTP_ADDR", ":8082")
)

func main() {

	handler := runhttp.NewHandler()

	mux := newMux(handler)

	server := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      135 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	// 启动一个 goroutine
	go func() {
		log.Printf("Server listening on %s", httpAddr)
		serverErrors <- server.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)

	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if err != nil && errors.Is(err, http.ErrServerClosed) == false {
			log.Printf("matcher API server failed: %v", err)
		}
	case sig := <-shutdown:
		log.Printf("Server is shutting down due to %v signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Could not stop the server gracefully: %v", err)
			closeErr := server.Close()
			if closeErr != nil {
				log.Printf("Could not stop the server forcibly: %v", closeErr)
			}
		}
	}

}

func newMux(handler *runhttp.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /matcher/run", handler.Run)
	mux.HandleFunc("POST /matcher/retry", handler.Retry)
	return mux
}

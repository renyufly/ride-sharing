package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"

	"ride-sharing/internal/adapter/realtime"
	"ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/platform/config"
	runhttp "ride-sharing/internal/transport/http/run"
	riderws "ride-sharing/internal/transport/ws/rider"
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

	// 建立骑手连接
	realtimeConfig := config.DefaultRealtime()
	if err := realtimeConfig.Validate(); err != nil {
		log.Fatal(err)
	}

	connections := realtime.NewManager(realtimeConfig.WriteTimeout)
	dispatcher := dispatch.NewService()

	mux.Handle("GET /matcher/ws/rider", riderws.NewHandler(
		connections,
		dispatcher,
		identifyDemoRider,
		realtimeConfig.ReadLimitBytes,
		realtimeConfig.WriteTimeout,
		realtimeConfig.PingInterval,
		realtimeConfig.PongTimeout,
	))

	mux.Handle("POST /matcher/live/demo", runhttp.NewLiveHandler(
		dispatcher,
		connections,
		realtimeConfig.ResponseTimeout,
	))

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


func identifyDemoRider(r *http.Request) (uint64, error) {
	uid, err := strconv.ParseUint(r.URL.Query().Get("riderUid"), 10, 64)
	if err != nil || uid == 0 {
		return 0, errors.New("invalid rider UID")
	}
	return uid, nil
}
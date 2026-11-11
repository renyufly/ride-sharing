package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"

	"github.com/google/uuid"

	"ride-sharing/internal/adapter/kubernetes"
	"ride-sharing/internal/adapter/mongostore"
	"ride-sharing/internal/adapter/realtime"
	"ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/platform/config"
	runhttp "ride-sharing/internal/transport/http/run"
	riderws "ride-sharing/internal/transport/ws/rider"
	"ride-sharing/shared/db"
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

	if scaleToken := os.Getenv("MATCHER_SCALE_TOKEN"); scaleToken != "" {
		namespace := os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			log.Fatal("POD_NAMESPACE is required when matcher scaling is enabled")
		}

		// TODO: "MATCHER_MAX_REPLICAS", "4"
		maxReplicas, err := strconv.ParseInt(env.GetString("MATCHER_MAX_REPLICAS", "4"), 10, 32)
		if err != nil || maxReplicas < 1 {
			log.Fatal("MATCHER_MAX_REPLICAS must be a positive integer")
		}

		scaler, err := kubernetes.NewMatcherScaler(namespace)
		if err != nil {
			log.Fatalf("configure matcher scaling: %v", err)
		}
		runhttp.RegisterReplicaRoutes(mux, scaler, scaleToken, int32(maxReplicas))
	}

	// 建立骑手连接
	realtimeConfig := config.DefaultRealtime()
	if err := realtimeConfig.Validate(); err != nil {
		log.Fatal(err)
	}

	connections := realtime.NewManager(realtimeConfig.WriteTimeout)
	dispatcher := dispatch.NewService()
	ledger := dispatch.NewLiveLedger()

	mux.Handle("GET /matcher/ws/rider", riderws.NewHandler(
		connections,
		dispatcher,
		identifyDemoRider,
		realtimeConfig.ReadLimitBytes,
		realtimeConfig.WriteTimeout,
		realtimeConfig.PingInterval,
		realtimeConfig.PongTimeout,
		ledger,
	))

	maxOrdersPerRider := 0
	if raw, ok := os.LookupEnv("MATCHER_MAX_ORDERS_PER_RIDER"); ok {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			log.Fatalf("invalid MATCHER_MAX_ORDERS_PER_RIDER: %q", raw)
		}
		maxOrdersPerRider = value
	}

	serviceCtx, stopService := context.WithCancel(context.Background())
	defer stopService()

	internalToken := os.Getenv("MATCHER_INTERNAL_TOKEN")
	if internalToken == "" {
		log.Fatal("MATCHER_INTERNAL_TOKEN must be set")
	}

	if os.Getenv("MATCHER_ASYNC_ORDERS") == "1" {
		dispatchConfig, err := config.LoadDispatchRuntime()
		if err != nil {
			log.Fatalf("load dispatch runtime config: %v", err)
		}
		mongoConfig := db.NewMongoDefaultConfig()
		client, err := db.NewMongoClient(serviceCtx, mongoConfig)
		if err != nil {
			log.Fatalf("connect matcher MongoDB: %v", err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.Disconnect(ctx); err != nil {
				log.Printf("disconnect matcher MongoDB: %v", err)
			}
		}()

		podName := os.Getenv("POD_NAME")
		if podName == "" {
			podName, _ = os.Hostname()
		}
		store := mongostore.NewOrderStore(db.GetDatabase(client, mongoConfig), podName)

		indexCtx, cancelIndexes := context.WithTimeout(serviceCtx, 20*time.Second)
		indexErr := store.EnsureIndexes(indexCtx)
		cancelIndexes()
		if indexErr != nil {
			log.Fatalf("create matcher order indexes: %v", indexErr)
		}
		
		runhttp.RegisterScheduledOrderRoutes(mux, store)

		gatewayID := uuid.NewString()
		mux.Handle("GET /matcher/v2/ws/rider", riderws.NewScheduledHandler(
			connections, identifyDemoRider, realtimeConfig, gatewayID, store,
		))
		mux.Handle(
			"POST /matcher/v2/live/orders/complete",
			runhttp.NewCompleteOrderHandler(internalToken, store.CompleteScheduledOrder),
		)

		
		processor, err := newScheduledOrderProcessor(
			store, maxOrdersPerRider, realtimeConfig.ResponseTimeout,
		)
		if err != nil {
			log.Fatalf("create scheduled order processor: %v", err)
		}

		heartbeatDone, err := startGatewayHeartbeat(serviceCtx, store, gatewayID)
		if err != nil {
			log.Fatalf("register matcher gateway: %v", err)
		}

		log.Printf(
			"starting async dispatch: scheduler_workers=%d delivery_workers=%d",
			dispatchConfig.SchedulerWorkers,
			dispatchConfig.DeliveryWorkers,
		)

		schedulerDone := make(chan struct{})
		go func() {
			defer close(schedulerDone)
			dispatch.RunScheduler(
				serviceCtx,
				store,
				processor.process,
				dispatchConfig.SchedulerWorkers,
			)
		}()

		deliveryDone := make(chan struct{})
		go func() {
			defer close(deliveryDone)
			dispatch.RunOfferDelivery(
				serviceCtx,
				gatewayID,
				store,
				connections.SendScheduledOffer,
				dispatchConfig.DeliveryWorkers,
			)
		}()

		defer func() {
			stopService()
			<-heartbeatDone
			<-schedulerDone
			<-deliveryDone
		}()
	}

	liveHandler := runhttp.NewLiveHandler(
		dispatcher, connections, 
		realtimeConfig.ResponseTimeout,
		ledger,
		maxOrdersPerRider,
		serviceCtx,
	)

	mux.Handle("POST /matcher/live/demo", liveHandler)
	mux.HandleFunc("POST /matcher/live/order", liveHandler.Order)

	mux.Handle(
		"POST /matcher/live/order/complete",
		runhttp.NewLiveCompleteHandler(ledger, internalToken),
	)

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

		ctx, cancel := context.WithTimeout(context.Background(), 140*time.Second)
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
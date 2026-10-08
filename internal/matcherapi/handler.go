package matcherapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"ride-sharing/internal/config"
	"ride-sharing/internal/deferred"
	"ride-sharing/internal/matchrun"
	"ride-sharing/internal/pipeline"
	"time"
)

// 后端收到请求后怎么处理
// 处理 HTTP 请求，然后把任务交给真正的业务层

type Handler struct {
	runSlot chan struct{}
	nextRunID   uint64
	lastSession *retrySession
}


type retrySession struct {
	runID          string
	currentRound   int
	config         config.Config
	previewSize    int
	deferredOrders []pipeline.DeferredOrder
	rounds         []RoundResponse
}



func NewHandler() *Handler{
	handler := Handler{
		runSlot: make(chan struct{}, 1),
	}

	// 所有请求必须共享同一个 channel
	return &handler

}

// 辅助函数
func writeJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	// 写入状态码
	w.WriteHeader(status)
	// 返回 Encode 的错误
	return json.NewEncoder(w).Encode(value)
}


func (h *Handler) Run(w http.ResponseWriter, r *http.Request) {

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var apiRequest Request
	if err:= decoder.Decode(&apiRequest); err != nil {
		// 解码失败时返回 400 Bad Request
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: err.Error(),
		})
		return 
	}

	// 再执行一次解码，确认后面已经是 io.EOF
	var extra struct{}
	if err:= decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Error: "request body must only contain a single JSON object",
			})
	
		} else {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
			})
		}
		return
	}

	runRequest, err := apiRequest.toRunRequest()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
			})
	
		return 
	}

	select {
	case h.runSlot <- struct{}{}:
		defer func() {<- h.runSlot }()
	default:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "a match run is already in progress",
		})
		return 
	}

	var memorySink *deferred.MemorySink
	if runRequest.Config.Strategy == config.StrategyBalanced {

		limit := runRequest.Config.OrderCount

	memorySink, err = deferred.NewMemorySink(limit)
		
		// memorySink, err = deferred.NewMemorySink(runRequest.PreviewSize)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{
				Error: "cannot create memory sink",
			})
		
			return 
		}
		
		runRequest.DeferredSink = memorySink
	}

	runResult, runErr := matchrun.Run(r.Context(), runRequest)
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
				Error: "matcher run failed",
		})
		
		return
	}

	var memorySnapshot deferred.MemorySnapshot
	if memorySink != nil {
		memorySnapshot = memorySink.Snapshot()
	}

	firstRound := mapRoundResponse(
		runResult,
		memorySnapshot,
		runRequest.PreviewSize,
		1,
		"generated",
		0,
		runResult.Config.ArrivalWindow.Milliseconds(),
	)

	h.nextRunID++
	runID := fmt.Sprintf("run-%d", h.nextRunID)

	rounds := []RoundResponse{firstRound}

	h.lastSession = &retrySession{
		runID:        runID,
		currentRound: 1,
		config:       runResult.Config,
		previewSize:  runRequest.PreviewSize,
		deferredOrders: append(
			[]pipeline.DeferredOrder(nil),
			memorySnapshot.Orders...,
		),
		rounds: rounds,
	}

	// runResponse := RunResponse{
	// 	Result: runResult,
	// 	OrderPreview: runResult.Pipeline.OrderPreview,
	// 	AssignmentPreview: runResult.Pipeline.AssignmentPreview,
	// 	Deferred: memorySnapshot,
	// }
	runResponse := toRunResponse(runResult, memorySnapshot, runRequest.PreviewSize, runID, 1, rounds)


	writeJSON(w, http.StatusOK, runResponse)

	
}

func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var apiRequest RetryRequest
	if err:= decoder.Decode(&apiRequest); err != nil {
		// 解码失败时返回 400 Bad Request
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: err.Error(),
		})
		return 
	}

	// 再执行一次解码，确认后面已经是 io.EOF
	var extra struct{}
	if err:= decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Error: "request body must only contain a single JSON object",
			})
	
		} else {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Error: err.Error(),
			})
		}
		return
	}

	// 获取与普通 Run 相同的运行槽。
	select {
	case h.runSlot <- struct{}{}:
		defer func() {
			<-h.runSlot
		}()

	default:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "a match run is already in progress",
		})
		return
	}

	session := h.lastSession

	if session == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Error: "run session not found",
		})
		return
	}

	if apiRequest.RunID != session.runID {
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Error: "run session not found or replaced",
		})
		return
	}

	if apiRequest.SourceRound != session.currentRound {
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "source round is stale",
		})
		return
	}

	if len(session.deferredOrders) == 0 {
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "no deferred orders available for retry",
		})
		return
	}

	if session.config.Strategy != config.StrategyBalanced {
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "retry requires balanced strategy",
		})
		return
	}


	// 准备下一轮。
	nextRound := session.currentRound + 1
	inputCount := len(session.deferredOrders)

	retryConfig := session.config

	retryConfig.OrderCount = inputCount
	retryConfig.Attempt = uint(nextRound)
	retryConfig.MaxOrdersPerRider = apiRequest.MaxOrdersPerRider
	retryConfig.DeferredInput = ""

	if apiRequest.GracePeriodMs > 0 {
		retryConfig.ArrivalWindow =
			time.Duration(apiRequest.GracePeriodMs) * time.Millisecond
	}


	memorySink, err := deferred.NewMemorySink(inputCount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error: "cannot create retry memory sink",
		})
		return
	}

	memorySource, err := deferred.NewMemorySource(session.deferredOrders)


	retryRequest := matchrun.Request{
	    Config:       retryConfig,
	    PreviewSize:  session.previewSize,
	    DeferredSink: memorySink,
	    OrderSource:  memorySource,
	}


	// 运行下一轮。
	
	result, err := matchrun.Run(r.Context(), retryRequest)
	if err != nil {
	    writeJSON(w, http.StatusInternalServerError, ErrorResponse{
	        Error: "matcher retry failed",
	    })
	    return
	}

	
	snapshot := memorySink.Snapshot()

	// 映射第二轮结果
	
	roundResponse := mapRoundResponse(
	    result,
	    snapshot,
	    session.previewSize,
	    nextRound,
	    "deferred",
	    apiRequest.SourceRound,
	    retryConfig.ArrivalWindow.Milliseconds(),
	)

	// 创建新的轮次历史
	newRounds := make(
	    []RoundResponse,
	    0,
	    len(session.rounds)+1,
	)
	
	newRounds = append(newRounds, session.rounds...)
	newRounds = append(newRounds, roundResponse)

	// 准备新的 Session
	newSession := &retrySession{
	    runID:        session.runID,
	    currentRound: nextRound,
	    config:       retryConfig,
	    previewSize:  session.previewSize,
	    deferredOrders: append(
	        []pipeline.DeferredOrder(nil),
	        snapshot.Orders...,
	    ),
	    rounds: newRounds,
	}

	
	response := toRunResponse(
	    result,
	    snapshot,
	    session.previewSize,
	    session.runID,
	    nextRound,
	    newRounds,
	)

	
	h.lastSession = newSession
	
	writeJSON(w, http.StatusOK, response)


}
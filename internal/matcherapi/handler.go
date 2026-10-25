package matcherapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"ride-sharing/internal/adapter/deferred/memory"
	sessionmemory "ride-sharing/internal/adapter/session/memory"
	"ride-sharing/internal/adapter/simulation/runsetup"
	"ride-sharing/internal/app/run"
	"ride-sharing/internal/app/run/replay"
	"ride-sharing/internal/platform/config"
)

// 后端收到请求后怎么处理
// 处理 HTTP 请求，然后把任务交给真正的业务层

type Handler struct {
	sessions *sessionmemory.Manager[RoundResponse]
}

func NewHandler() *Handler {
	return &Handler{sessions: sessionmemory.New[RoundResponse]()}
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
	if err := decoder.Decode(&apiRequest); err != nil {
		// 解码失败时返回 400 Bad Request
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// 再执行一次解码，确认后面已经是 io.EOF
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
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

	lease, ok := h.sessions.TryBegin()
	if !ok {
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "a match run is already in progress",
		})
		return
	}
	defer lease.End()

	var memorySink *memory.MemorySink
	if runRequest.Config.Strategy == config.StrategyBalanced {

		limit := runRequest.Config.OrderCount

		memorySink, err = memory.NewMemorySink(limit)

		// memorySink, err = memory.NewMemorySink(runRequest.PreviewSize)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{
				Error: "cannot create memory sink",
			})

			return
		}

		runRequest.DeferredSink = memorySink
	}

	runResult, runErr := run.Run(r.Context(), runRequest, runsetup.Factory{})
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error: "matcher run failed",
		})

		return
	}

	var memorySnapshot memory.MemorySnapshot
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

	saved := lease.SaveFirst(runResult.Config, runRequest.PreviewSize, memorySnapshot.Orders, firstRound)
	runResponse := toRunResponse(runResult, memorySnapshot, runRequest.PreviewSize, saved.RunID, saved.CurrentRound, saved.Rounds)

	writeJSON(w, http.StatusOK, runResponse)

}

func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var apiRequest RetryRequest
	if err := decoder.Decode(&apiRequest); err != nil {
		// 解码失败时返回 400 Bad Request
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// 再执行一次解码，确认后面已经是 io.EOF
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
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

	lease, ok := h.sessions.TryBegin()
	if !ok {
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "a match run is already in progress",
		})
		return
	}
	defer lease.End()

	session, status := lease.RetryState(apiRequest.RunID, apiRequest.SourceRound)
	switch status {
	case sessionmemory.RetrySessionMissing:
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Error: "run session not found",
		})
		return
	case sessionmemory.RetryRunReplaced:
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Error: "run session not found or replaced",
		})
		return
	case sessionmemory.RetryRoundStale:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "source round is stale",
		})
		return
	case sessionmemory.RetryNoDeferredOrders:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "no deferred orders available for retry",
		})
		return
	case sessionmemory.RetryRequiresBalanced:
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "retry requires balanced strategy",
		})
		return
	}

	// 准备下一轮。
	nextRound := session.CurrentRound + 1
	inputCount := len(session.DeferredOrders)

	memorySink, err := memory.NewMemorySink(inputCount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error: "cannot create retry memory sink",
		})
		return
	}

	memorySource, err := memory.NewMemorySource(session.DeferredOrders)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "cannot create retry memory source"})
		return
	}

	retryRequest, err := replay.Prepare(
		session.Config, session.CurrentRound, inputCount,
		apiRequest.MaxOrdersPerRider, apiRequest.GracePeriodMs,
		session.PreviewSize, memorySource, memorySink,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "cannot prepare retry run"})
		return
	}
	retryConfig := retryRequest.Config

	// 运行下一轮。

	result, err := run.Run(r.Context(), retryRequest, runsetup.Factory{})
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
		session.PreviewSize,
		nextRound,
		"deferred",
		apiRequest.SourceRound,
		retryConfig.ArrivalWindow.Milliseconds(),
	)

	saved := lease.SaveNext(retryConfig, snapshot.Orders, roundResponse)

	response := toRunResponse(
		result,
		snapshot,
		saved.PreviewSize,
		saved.RunID,
		saved.CurrentRound,
		saved.Rounds,
	)

	writeJSON(w, http.StatusOK, response)

}

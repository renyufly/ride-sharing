package rider

import (
	"net/http"
	"ride-sharing/internal/adapter/realtime"
	"ride-sharing/internal/app/dispatch"
	"time"

	"github.com/gorilla/websocket"
)

// 从身份信息中获取 riderUID
type Identify func(*http.Request) (uint64, error)

// Handler 处理骑手 WebSocket 连接及其业务消息
type Handler struct {
	connections *realtime.Manager
	dispatcher  *dispatch.Service
	identify    Identify
	upgrader    websocket.Upgrader
	readLimitBytes int64
	writeTimeout time.Duration
	pingInterval time.Duration
	pongTimeout time.Duration
}

func NewHandler(
	connections *realtime.Manager,
	dispatcher *dispatch.Service,
	identify Identify,
	readLimitBytes int64,
	writeTimeout time.Duration,
	pingInterval time.Duration,
	pongTimeout time.Duration,
) *Handler {
	return &Handler{
		connections: connections,
		dispatcher:  dispatcher,
		identify:    identify,
		readLimitBytes: readLimitBytes,
		writeTimeout: writeTimeout,
		pingInterval: pingInterval,
		pongTimeout: pongTimeout,
	}
}

// 接收骑手客户端发送的 JSON
type incomingMessage struct {
	Type string `json:"type"`
	Data struct {
		OrderID   uint64 `json:"orderId"`
		RequestID string `json:"requestId"`
	} `json:"data"`
}

// ServeHTTP 处理骑手 WebSocket 连接请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 从request中获取riderUID
	riderUID, err := h.identify(r)
	if err != nil {
		http.Error(w, "unauthorized rider", http.StatusUnauthorized)
		return
	}

	// 升级 HTTP 为 WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// 限制单条 WebSocket 消息的最大读取大小
	conn.SetReadLimit(h.readLimitBytes)
	// 注册骑手连接
	h.connections.Register(riderUID, conn)
	defer func() {
		h.connections.Remove(riderUID, conn)
		conn.Close()
	}()

	// WebSocket 心跳检测（Heartbeat）
	if err := conn.SetReadDeadline(time.Now().Add(h.pongTimeout)); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(h.pongTimeout))
	})

	done := make(chan struct{})
	defer close(done)

	go func() {
		// 定期触发 Ping
		ticker := time.NewTicker(h.pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				deadline := time.Now().Add(h.writeTimeout)
				// 发送 Ping
				if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()

	// 持续读取骑手消息
	for {
		var message incomingMessage
		if err := conn.ReadJSON(&message); err != nil {
			// 发生错误，结束读取循环，清理连接
			return
		}
		if message.Data.RequestID == "" {
			continue
		}

		// 将接受或拒绝 转交给 派单service
		switch message.Type {
		case realtime.MessageOrderAccept:
			h.dispatcher.Respond(message.Data.OrderID, riderUID, 
				message.Data.RequestID, true)
		case realtime.MessageOrderDecline:
			h.dispatcher.Respond(message.Data.OrderID, riderUID, 
				message.Data.RequestID, false)
		}
	}

}
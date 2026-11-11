package rider

import (
	"context"
	"errors"
	"log"
	"net/http"
	"ride-sharing/internal/adapter/realtime"
	"ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/domain/geo"
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
	ledger *dispatch.LiveLedger
	scheduled  scheduledRiderStore
	gatewayID string
}

func NewHandler(
	connections *realtime.Manager,
	dispatcher *dispatch.Service,
	identify Identify,
	readLimitBytes int64,
	writeTimeout time.Duration,
	pingInterval time.Duration,
	pongTimeout time.Duration,
	ledger *dispatch.LiveLedger,
) *Handler {
	return &Handler{
		connections: connections,
		dispatcher:  dispatcher,
		identify:    identify,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				return origin == "" || origin == "http://localhost:3000"  ||
        			   origin == "http://localhost:13000"
			},
		},
		readLimitBytes: readLimitBytes,
		writeTimeout: writeTimeout,
		pingInterval: pingInterval,
		pongTimeout: pongTimeout,
		ledger: ledger,
	}
}

// 接收骑手客户端发送的 JSON
type incomingMessage struct {
	Type string `json:"type"`
	Data struct {
		OrderID   uint64 `json:"orderId"`
		RequestID string `json:"requestId"`
		Location *struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"location"`
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
	defer conn.Close()

	var route dispatch.RiderSession
	if h.scheduled == nil {
		h.connections.Register(riderUID, conn)
		defer h.connections.Remove(riderUID, conn)
	} else {
		var cleanup func()
		route, cleanup, err = h.registerScheduledSession(r.Context(), riderUID, conn)
		defer cleanup()
		if err != nil {
			log.Printf("register scheduled rider %d: %v", riderUID, err)
			return
		}
	}

	// WebSocket 心跳检测（Heartbeat）
	if err := conn.SetReadDeadline(time.Now().Add(h.pongTimeout)); err != nil {
		return
	}
	conn.SetPongHandler(func(string) error {
		validUntil := time.Now().Add(h.pongTimeout)
		if h.scheduled != nil {
			ctx, cancel := context.WithTimeout(r.Context(), presenceWriteTimeout)
			defer cancel()

			current, err := h.scheduled.RenewRiderSession(ctx, route, validUntil)
			if err != nil {
				log.Printf("renew scheduled rider %d: %v", riderUID, err)
				return err
			}
			if !current {
				return errors.New("rider session has been replaced")
			}
		}
		return conn.SetReadDeadline(validUntil)
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

		// 将 骑手位置更新 / 接单 / 拒单 转交给 派单service
		switch message.Type {
		case realtime.MessageRiderLocation:
			if message.Data.Location == nil {
				continue
			}
			location := geo.GeoPoint{
				Latitude:  message.Data.Location.Latitude,
				Longitude: message.Data.Location.Longitude,
			}

			if h.scheduled != nil {
				if err := geo.ValidatePoint(location); err != nil {
					continue
				}

				ctx, cancel := context.WithTimeout(r.Context(), presenceWriteTimeout)
				current, err := h.scheduled.SaveRiderLocation(ctx, route, location)
				cancel()
				if err != nil {
					log.Printf("save scheduled rider %d location: %v", riderUID, err)
					return
				}
				if !current {
					return
				}
				continue
			}

			if err := h.connections.UpdateLocation(riderUID, conn, location); err != nil {
				continue
			}
		case realtime.MessageOrderAccept, 
			 realtime.MessageOrderDecline:
			if message.Data.RequestID == "" {
				continue
			}

			if h.scheduled != nil {
				orderID := message.Data.OrderID
				if orderID == 0 {
					continue
				}
				requestID := message.Data.RequestID
				accepted := message.Type == realtime.MessageOrderAccept

				ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				processed, err := h.scheduled.RespondToOffer(ctx, route, orderID, requestID, accepted)
				cancel()
				if err != nil {
					log.Printf("respond to scheduled order %d: %v", orderID, err)
					return
				}

				if err := h.connections.SendScheduledReply(
					r.Context(), route.SessionID, orderID, requestID, processed, accepted,
				); err != nil {
					return
				}
				continue
			}

			processed := h.connections.RespondFromCurrent(
				riderUID,
				conn,
				h.dispatcher,
				message.Data.OrderID,
				message.Data.RequestID,
				message.Type == realtime.MessageOrderAccept,
			)

			_ = h.connections.SendResponseAck(
				riderUID,
				conn,
				message.Data.OrderID,
				message.Data.RequestID,
				processed,
			)

		case realtime.MessageOrderComplete:
			if message.Data.OrderID == 0 {
				continue
			}
			if err := h.completeOrder(r.Context(), conn, route, riderUID, message.Data.OrderID); err != nil {
				return
			}
		case realtime.MessageOrderStatusQuery:
			if message.Data.OrderID == 0 {
				continue
			}

			if h.scheduled != nil {
				orderID := message.Data.OrderID
				ctx, cancel := context.WithTimeout(r.Context(), presenceWriteTimeout)
				status, err := h.scheduled.ScheduledStatusForRider(ctx, route, orderID)
				cancel()
				if err != nil {
					log.Printf("query scheduled order %d: %v", orderID, err)
					return
				}

				if err := h.connections.SendScheduledOrderStatus(
					r.Context(), route.SessionID, orderID, status, "",
				); err != nil {
					return
				}
				continue
			}

			status := h.ledger.StatusForRider(message.Data.OrderID, riderUID)
			_ = h.connections.SendOrderStatus(
				riderUID,
				conn,
				message.Data.OrderID,
				status,
				"",
			)
		}
	}

}

func (h *Handler) completeOrder(
	ctx context.Context, conn *websocket.Conn, route dispatch.RiderSession, riderUID, orderID uint64,
) error {
	var err error
	if h.scheduled != nil {
		writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = h.scheduled.CompleteScheduledOrderForSession(writeCtx, route, orderID)
		cancel()
	} else {
		err = h.connections.CompleteFromCurrent(riderUID, conn, h.ledger, orderID)
	}

	status := dispatch.RiderOrderCompleted
	detail := ""
	if err != nil {
		status = dispatch.RiderOrderUnknown
		if errors.Is(err, dispatch.ErrOrderNotAssigned) || errors.Is(err, dispatch.ErrOrderRiderMismatch) {
			detail = "订单未分配给当前骑手"
		} else {
			log.Printf("complete order %d for rider %d: %v", orderID, riderUID, err)
			detail = "完成订单失败，请重试"
		}
	}
	if h.scheduled != nil {
		return h.connections.SendScheduledOrderStatus(ctx, route.SessionID, orderID, status, detail)
	}
	return h.connections.SendOrderStatus(riderUID, conn, orderID, status, detail)
}

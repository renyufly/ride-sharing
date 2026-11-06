package realtime

import (
	"context"
	"errors"
	"sync"
	"time"

	appdispatch "ride-sharing/internal/app/dispatch"

	"github.com/gorilla/websocket"
)

type client struct{
	connect *websocket.Conn
	writeMu sync.Mutex
}

// 管理所有骑手的在线连接
type Manager struct {
	mu sync.RWMutex
	clients map[uint64]*client  // riderUID -> 骑手当前的 WebSocket client
	writeTimeout time.Duration
}

func NewManager(writeTimeout time.Duration) *Manager {
	return &Manager{
		clients: make(map[uint64]*client),
		writeTimeout: writeTimeout,
	}
}

// 同一个骑手只维护一个当前有效连接
func (m *Manager) Register(riderUID uint64, connect *websocket.Conn) {
	m.mu.Lock()
	old := m.clients[riderUID]
	m.clients[riderUID] = &client{
		connect: connect,
	}
	m.mu.Unlock()

	if old != nil {
		// 关闭旧 websocket 连接
		old.connect.Close()
	}
}

// 删除指定的 websocket 连接 
func (m *Manager) Remove(riderUID uint64, connect *websocket.Conn) {
	m.mu.Lock()
	if current := m.clients[riderUID]; current != nil && current.connect == connect {
		delete(m.clients, riderUID)
	}
	m.mu.Unlock()
}

// WebSocket 消息格式
type pickupPayload struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type offerPayload struct {
	OrderID   uint64        `json:"orderId"`
	RequestID string        `json:"requestId"`
	Pickup    pickupPayload `json:"pickup"`
}

type offerMessage struct {
	Type string       `json:"type"`
	Data offerPayload `json:"data"`
}

const (
	MessageOrderRequest = "matcher.order.request"
	MessageOrderAccept  = "matcher.order.accept"
	MessageOrderDecline = "matcher.order.decline"
)

// SendOffer 向指定骑手发送派单邀请
func (m *Manager) SendOffer(ctx context.Context, riderUID uint64, offer appdispatch.Offer) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// 查找存在的骑手连接
	m.mu.RLock()
	current := m.clients[riderUID]
	m.mu.RUnlock()
	if current == nil {
		return errors.New("rider is offline")
	}

	// 设置写入操作的超时时间
	deadline := time.Now().Add(m.writeTimeout)
	
	current.writeMu.Lock()
	defer current.writeMu.Unlock()

	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	if err := current.connect.SetWriteDeadline(deadline); err != nil {
		return err
	}

	// 将订单邀请序列化为 JSON 并通过 WebSocket 发送
	return current.connect.WriteJSON(offerMessage{
		Type: MessageOrderRequest,
		Data: offerPayload{
			OrderID:   offer.Order.ID,
			RequestID: offer.RequestID,
			Pickup: pickupPayload{
				Latitude:  offer.Order.Pickup.Latitude,
				Longitude: offer.Order.Pickup.Longitude,
			},
		},
	})
}
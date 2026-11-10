package realtime

import (
	"context"
	"errors"
	"sync"
	"time"

	appdispatch "ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/domain/geo"

	"github.com/gorilla/websocket"
)

type client struct{
	connect *websocket.Conn
	writeMu sync.Mutex
	location    geo.GeoPoint
	hasLocation bool
}

// 管理所有骑手的在线连接
type Manager struct {
	mu sync.RWMutex
	clients map[uint64]*client  // riderUID -> 骑手当前的 WebSocket client
	writeTimeout time.Duration
	sessions map[string]*client
}

func NewManager(writeTimeout time.Duration) *Manager {
	return &Manager{
		clients: make(map[uint64]*client),
		writeTimeout: writeTimeout,
		sessions: make(map[string]*client),
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

// 骑手位置更新
func (m *Manager) UpdateLocation(riderUID uint64,
	conn *websocket.Conn,
	location geo.GeoPoint,
) error {
	if err := geo.ValidatePoint(location); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.clients[riderUID]
	if current == nil || current.connect != conn {
		return errors.New("rider connection is no longer current")
	}

	current.location = location
	current.hasLocation = true

	return nil
}

// 验证骑手的接单respond 是否来自该骑手当前有效的 WebSocket 连接
func (m *Manager) RespondFromCurrent(
	riderUID uint64,
	conn *websocket.Conn,
	dispatcher *appdispatch.Service,
	orderID uint64,
	requestID string,
	accepted bool,
) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	current := m.clients[riderUID]
	if current == nil || current.connect != conn {
		return false
	}

	return dispatcher.Respond(orderID, riderUID, requestID, accepted)
}

func (m *Manager) CompleteFromCurrent(
	riderUID uint64, conn *websocket.Conn, ledger *appdispatch.LiveLedger, orderID uint64,
) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	current := m.clients[riderUID]
	if current == nil || current.connect != conn {
		return errors.New("rider connection is no longer current")
	}
	return ledger.Complete(orderID, riderUID)
}

// 获取所有已登记位置的骑手在线client, 返回位置的snapshot
func (m *Manager) SnapshotLocations() map[uint64]geo.GeoPoint {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// (riderUID -> location)
	locations := make(map[uint64]geo.GeoPoint, len(m.clients))
	for uid, current := range m.clients {
		if current.hasLocation {
			locations[uid] = current.location
		}
	}
	return locations
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

// processed: false-请求已超时、已处理过或不再有效
type responseAckMessage struct {
	Type string `json:"type"`
	Data struct {
		OrderID   uint64 `json:"orderId"`
		RequestID string `json:"requestId"`
		Processed bool   `json:"processed"`
	} `json:"data"`
}

type assignmentConfirmedMessage struct {
    Type string `json:"type"`
    Data struct {
        OrderID   uint64 `json:"orderId"`
        RequestID string `json:"requestId"`
    } `json:"data"`
}

type orderStatusMessage struct {
    Type string `json:"type"`
    Data struct {
        OrderID uint64                       `json:"orderId"`
        Status  appdispatch.RiderOrderStatus `json:"status"`
        Error   string                       `json:"error,omitempty"`
    } `json:"data"`
}

const (
	MessageOrderRequest = "matcher.order.request"
	MessageOrderAccept  = "matcher.order.accept"
	MessageOrderDecline = "matcher.order.decline"
	MessageOrderComplete = "matcher.order.complete"
	MessageRiderLocation = "matcher.rider.location"
	MessageResponseAck = "matcher.order.response_ack"
	MessageAssignmentConfirmed = "matcher.order.assigned"
	MessageOrderStatusQuery = "matcher.order.status_query"
    MessageOrderStatus      = "matcher.order.status"
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
	current.writeMu.Lock()
	defer current.writeMu.Unlock()

	deadline := time.Now().Add(m.writeTimeout)

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


func (m *Manager) SendResponseAck(
	riderUID uint64,
	conn *websocket.Conn,
	orderID uint64,
	requestID string,
	processed bool,
) error {
	m.mu.RLock()
	current := m.clients[riderUID]
	if current == nil || current.connect != conn {
		m.mu.RUnlock()
		return errors.New("rider connection is no longer current")
	}
	m.mu.RUnlock()

	current.writeMu.Lock()
	defer current.writeMu.Unlock()

	m.mu.RLock()
	stillCurrent := m.clients[riderUID] == current
	m.mu.RUnlock()
	if !stillCurrent {
		return errors.New("rider connection is no longer current")
	}

	if err := conn.SetWriteDeadline(time.Now().Add(m.writeTimeout)); err != nil {
		return err
	}

	message := responseAckMessage{Type: MessageResponseAck}
	message.Data.OrderID = orderID
	message.Data.RequestID = requestID
	message.Data.Processed = processed
	return conn.WriteJSON(message)
}

func (m *Manager) SendAssignmentConfirmed(
    ctx context.Context,
    riderUID, orderID uint64,
    requestID string,
) error {
    if err := ctx.Err(); err != nil {
        return err
    }

    m.mu.RLock()
    current := m.clients[riderUID]
    m.mu.RUnlock()
    if current == nil {
        return errors.New("rider is offline")
    }

    current.writeMu.Lock()
    defer current.writeMu.Unlock()

    m.mu.RLock()
    stillCurrent := m.clients[riderUID] == current
    m.mu.RUnlock()
    if !stillCurrent {
        return errors.New("rider connection is no longer current")
    }

    deadline := time.Now().Add(m.writeTimeout)
    if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
        deadline = ctxDeadline
    }
    if err := current.connect.SetWriteDeadline(deadline); err != nil {
        return err
    }

    message := assignmentConfirmedMessage{Type: MessageAssignmentConfirmed}
    message.Data.OrderID = orderID
    message.Data.RequestID = requestID
    return current.connect.WriteJSON(message)
}

func (m *Manager) SendOrderStatus(
    riderUID uint64,
    conn *websocket.Conn,
    orderID uint64,
    status appdispatch.RiderOrderStatus,
    detail string,
) error {
    m.mu.RLock()
    current := m.clients[riderUID]
    if current == nil || current.connect != conn {
        m.mu.RUnlock()
        return errors.New("rider connection is no longer current")
    }
    m.mu.RUnlock()

    current.writeMu.Lock()
    defer current.writeMu.Unlock()

    m.mu.RLock()
    stillCurrent := m.clients[riderUID] == current
    m.mu.RUnlock()
    if !stillCurrent {
        return errors.New("rider connection is no longer current")
    }

    if err := conn.SetWriteDeadline(time.Now().Add(m.writeTimeout)); err != nil {
        return err
    }

    message := orderStatusMessage{Type: MessageOrderStatus}
    message.Data.OrderID = orderID
    message.Data.Status = status
    message.Data.Error = detail
    return conn.WriteJSON(message)
}

func (m *Manager) RegisterSession(sessionID string, conn *websocket.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[sessionID] = &client{connect: conn}
}

func (m *Manager) RemoveSession(sessionID string, conn *websocket.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if current := m.sessions[sessionID]; current != nil && current.connect == conn {
		delete(m.sessions, sessionID)
	}
}

func (m *Manager) writeSessionJSON(
	ctx context.Context, sessionID string, tryLock bool, messages ...any,
) error {
	m.mu.RLock()
	current := m.sessions[sessionID]
	m.mu.RUnlock()
	if current == nil {
		return errors.New("rider session is offline")
	}

	if tryLock {
		if !current.writeMu.TryLock() {
			return errors.New("rider session writer is busy")
		}
	} else {
		current.writeMu.Lock()
	}
	defer current.writeMu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.RLock()
	registered := m.sessions[sessionID] == current
	m.mu.RUnlock()
	if !registered {
		return errors.New("rider session is no longer registered")
	}

	deadline := time.Now().Add(m.writeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := current.connect.SetWriteDeadline(deadline); err != nil {
		return err
	}

	for _, message := range messages {
		if err := current.connect.WriteJSON(message); err != nil {
			_ = current.connect.Close()
			return err
		}
	}
	return nil
}

func (m *Manager) SendScheduledReply(
	ctx context.Context, sessionID string, orderID uint64,
	requestID string, processed, accepted bool,
) error {
	ack := responseAckMessage{Type: MessageResponseAck}
	ack.Data.OrderID = orderID
	ack.Data.RequestID = requestID
	ack.Data.Processed = processed

	if !processed || !accepted {
		return m.writeSessionJSON(ctx, sessionID, false, ack)
	}

	confirmed := assignmentConfirmedMessage{Type: MessageAssignmentConfirmed}
	confirmed.Data.OrderID = orderID
	confirmed.Data.RequestID = requestID
	return m.writeSessionJSON(ctx, sessionID, false, ack, confirmed)
}

func (m *Manager) SendScheduledOrderStatus(
	ctx context.Context, sessionID string, orderID uint64,
	status appdispatch.RiderOrderStatus, detail string,
) error {
	message := orderStatusMessage{Type: MessageOrderStatus}
	message.Data.OrderID = orderID
	message.Data.Status = status
	message.Data.Error = detail
	return m.writeSessionJSON(ctx, sessionID, false, message)
}

func (m *Manager) SendScheduledOffer(
	ctx context.Context, sessionID string, offer appdispatch.Offer,
) error {
	return m.writeSessionJSON(ctx, sessionID, true, offerMessage{
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

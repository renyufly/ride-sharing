package config

import (
	"errors"
	"time"
)

type RealtimeConfig struct {
	WriteTimeout time.Duration  // websocket写入操作的超时时间
	ReadLimitBytes int64  // 单条 WebSocket 消息的最大读取大小
	PingInterval time.Duration
	PongTimeout  time.Duration
	ResponseTimeout time.Duration
}

func DefaultRealtime() RealtimeConfig {
	return RealtimeConfig{
		WriteTimeout: 5 * time.Second,  // 5 s
		ReadLimitBytes: 4 << 10,  // 4 KiB
		PingInterval: 20*time.Second,  // 20 s
		PongTimeout: 60 * time.Second, // 60 s
		ResponseTimeout: 15 * time.Second, // 15 s
	}
	
}

func (c RealtimeConfig) Validate() error {
	if c.WriteTimeout <= 0 {
		return errors.New("realtime write timeout must be positive")
	}

	if c.ReadLimitBytes <= 0 {
		return errors.New("realtime read limit must be positive")
	}

	if c.PingInterval <= 0 {
		return errors.New("ping interval must be positive")
	}

	if c.PongTimeout <= 0 {
		return errors.New("pong timeout must be positive")
	}

	if c.ResponseTimeout <= 0 {
		return errors.New("response timeout must be positive")
	}

	return nil
}
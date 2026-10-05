package deferred

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"ride-sharing/internal/pipeline"
)

// FileSink 实现了 DeferredOrderSink 接口，负责将延迟订单以 JSONL 形式落盘
type FileSink struct {
	file           *os.File
	bufferedWriter *bufio.Writer
	jsonEncoder    *json.Encoder
	closed         bool
}



func NewFileSink(filePath string) (*FileSink, error) {
	if filePath == "" {
		return nil, errors.New("file path cannot be empty")
	}

	dir := filepath.Dir(filePath)

	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("file already exists, refusing to overwrite: %s", filePath)
		}
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}

	bufWriter := bufio.NewWriter(file)
	encode := json.NewEncoder(bufWriter)

	return &FileSink{
		file: file,
		bufferedWriter: bufWriter,
		jsonEncoder: encode,
	}, nil

}

// Store 写入单笔 DeferredOrder
// 内部将数据转为 DTO 并编码为单行 JSON (JSONL)
func (s *FileSink) Store(ctx context.Context, deferredOrder pipeline.DeferredOrder) error {
	if s.closed {
		return errors.New("sink is closed")
	}

	select {
	case <- ctx.Done():
		return ctx.Err()
	default:
	}

	record := newDeferredRecord(deferredOrder)

	if err:= s.jsonEncoder.Encode(record); err != nil {
		return fmt.Errorf("failed to encode deferred order json: %w", err)
	}

	return nil 
}

// Close 刷盘并关闭底层文件资源
// 处理逻辑遵循：即使 Flush 失败，也必须继续尝试关闭底层文件，防止文件handle泄漏
func (s *FileSink) Close() error {
	if s.closed {
		return nil
	}

	s.closed = true

	flushErr := s.bufferedWriter.Flush()

	// 无论 flush 是否成功，都必须关闭底层文件资源
	closeErr := s.file.Close()

	if flushErr != nil {
		return fmt.Errorf("failed to flush buffer before closing: %w", flushErr)
	}

	if closeErr != nil {
		return fmt.Errorf("failed to close underlying file: %w", closeErr)
	}

	return nil
}
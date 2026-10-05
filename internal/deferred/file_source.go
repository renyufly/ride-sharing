package deferred

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"ride-sharing/internal/model"
)

// 只保存当前扫描位置
type FileSource struct {
	file             *os.File
	scanner          *bufio.Scanner
	filePath         string
	nextSequence     uint64
	lineNumber       uint64
	expectedAttempt  uint32
	expectedCount    uint64
	closed           bool
}

const (
	InitBufferSize = 64*1024  // 64 KB
	MaxLineSize = 1024*1024  // 1MB
)

func newJSONLScanner(reader io.Reader) *bufio.Scanner {
	// 默认的 ScanLines 正好适用于一行一个 JSON 对象的 JSONL
	scanner := bufio.NewScanner(reader)

	scanner.Buffer(
		make([]byte, InitBufferSize),
		MaxLineSize,
	)

	return scanner
}

// 输入文件预检查
// 第一次提前验证全部输入并得到订单数
func inspectFile(filePath string, expectedAttempt uint32) (recordCount uint64, resultErr error) {
	if filePath == "" {
		return 0, fmt.Errorf("deferred input path cannot be empty")
	}
	
	if expectedAttempt == 0 {
		return 0, fmt.Errorf("expected input attempt must be greater than zero")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("open deferred input %s: %s", filePath, err)
	}

	defer func() {
		closeErr := file.Close()

		if closeErr != nil {
			wrappedCloseErr := fmt.Errorf("failed to close file %s: %w", filePath, closeErr)
			resultErr = errors.Join(resultErr, wrappedCloseErr)
		}
	}()

	scanner := newJSONLScanner(file)

	var lineNumber uint64 = 0

	for scanner.Scan() {
		lineNumber++  // 行数

		// scanner.Bytes() 返回的内容只在下一次 Scan() 前有效
		line := bytes.TrimSpace(scanner.Bytes())

		if len(line) == 0 {
			return 0, fmt.Errorf("error in %s, line number: %d, empty JSONL record", filePath, lineNumber)
		}

		record := deferredRecord{}

		if err:= json.Unmarshal(line, &record); err != nil {
			return 0, fmt.Errorf("decode deferred input %s, line %d, %s", filePath, lineNumber, err)
		}

		// 检测版本号
		if record.Version != 1 {
			return 0, fmt.Errorf("expected version=1, original version=%d, in file %s, line %d", record.Version, filePath, lineNumber)
		}

		if record.Attempt != expectedAttempt {
			return 0, fmt.Errorf("expected attempt=%d, original attempt=%d, line:%d", expectedAttempt, record.Attempt, lineNumber)
		}

		recordCount++
	}

	if scanner.Err() != nil {
		return 0, fmt.Errorf("error in file: %s, line:%d", filePath, lineNumber)
	}

	if recordCount == 0 {
		return 0, fmt.Errorf("empty file")
	}

	return recordCount, nil
}

func OpenFileSource(filePath string, expectedAttempt uint32) (*FileSource, uint64, error) {
	if filePath == "" {
		return nil, 0, fmt.Errorf("empty file")
	}

	if expectedAttempt == 0 {
		return nil, 0, fmt.Errorf("wrong attempt")
	}

	count, err := inspectFile(filePath, expectedAttempt)

	if err != nil {
		return nil, 0, fmt.Errorf("inspect error: %s", err)
	}

	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0, fmt.Errorf("open deferred input %s: %s", filePath, err)
	}

	scanner := newJSONLScanner(file)

	fileSource := FileSource{
		file: file,
		scanner: scanner,
		filePath: filePath,
		expectedAttempt: expectedAttempt,
		expectedCount: count,
	}

	return &fileSource, count, nil

}

// 实现 pipeline.OrderSource interface
func (s *FileSource) Next() (model.Order, bool, error) {
	if s.closed {
		return model.Order{}, false, fmt.Errorf("wrong closed file")
	}

	if s.scanner.Scan() == false {
		if s.scanner.Err() != nil {
			return model.Order{}, false, fmt.Errorf("wrong read")
		}

		if s.nextSequence != s.expectedCount {
			return model.Order{}, false, fmt.Errorf("wrong order count")
		}

		return model.Order{}, false, nil
	}

	s.lineNumber++

	record := deferredRecord{}

	line := bytes.TrimSpace(s.scanner.Bytes())
	if err:= json.Unmarshal(line, &record); err != nil {
		return model.Order{}, false, fmt.Errorf("decode deferred input %s, line %d, %s", s.filePath, s.lineNumber, err)
	}

	order := toModelOrder(record, s.nextSequence)

	s.nextSequence++

	return order, true, nil
	
}

func (s *FileSource) Close() error {
	if s.closed {
		return nil
	}

	s.closed = true

	if err:= s.file.Close(); err != nil {
		return fmt.Errorf("error in closing %s, %s", s.filePath, err)
	}

	return nil
}
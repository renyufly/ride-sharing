package report

// 索引内存统计 (性能统计)
type IndexStats struct {
	Kind           string `json:"kind"`
	EntryCount     int    `json:"entryCount"`
	EntrySizeBytes uint64 `json:"entrySizeBytes"`
	EstimatedBytes uint64 `json:"estimatedBytes"`
}

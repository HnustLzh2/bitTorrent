package main

import (
	"os"
	"path/filepath"
	"sync"
)

var PieceFilesDir = filepath.Join(os.TempDir(), "pieces")

type BlockInfo struct {
	Index  int // piece index
	Begin  int // 字节偏移（0, 16384, 32768, ...）
	Length int // block 长度（通常是 16384，最后一个可能更小）
}

type Address struct {
	IP   string
	Port int
}

type WorkQueue struct {
	mu         sync.RWMutex
	queue      []int       // piece索引队列
	retryCount map[int]int // piece索引 -> 重试次数
	maxRetries int         // 最大重试次数
}

func (wq *WorkQueue) Add(piece int) {
	wq.mu.Lock()
	defer wq.mu.Unlock()
	// 检查重试次数
	if wq.retryCount == nil {
		wq.retryCount = make(map[int]int)
		wq.maxRetries = 3 // 默认最大重试3次
	}
	retryCount := wq.retryCount[piece]
	if retryCount >= wq.maxRetries {
		// 超过最大重试次数，不再添加
		return
	}
	wq.retryCount[piece] = retryCount + 1
	wq.queue = append(wq.queue, piece)
}

// 获取队列第一个元素并推出
func (wq *WorkQueue) Get() (int, bool) {
	wq.mu.Lock()
	defer wq.mu.Unlock()
	if len(wq.queue) == 0 {
		return 0, false
	}
	piece := wq.queue[0]
	wq.queue = wq.queue[1:]
	return piece, true
}

func (wq *WorkQueue) IsEmpty() bool {
	wq.mu.RLock()
	defer wq.mu.RUnlock()
	return len(wq.queue) == 0
}

func (wq *WorkQueue) Size() int {
	wq.mu.RLock()
	defer wq.mu.RUnlock()
	return len(wq.queue)
}

// piece缓冲区
type PieceBuffer struct {
	mu     sync.RWMutex
	pieces map[int][]byte // piece index -> piece data
}

func (pb *PieceBuffer) Set(pieceIndex int, data []byte) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.pieces[pieceIndex] = data
}

func (pb *PieceBuffer) Get(pieceIndex int) ([]byte, bool) {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	data, ok := pb.pieces[pieceIndex]
	return data, ok
}

func (pb *PieceBuffer) HasAll(totalPieces int) bool {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return len(pb.pieces) == totalPieces
}

func (pb *PieceBuffer) Size() int {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return len(pb.pieces)
}

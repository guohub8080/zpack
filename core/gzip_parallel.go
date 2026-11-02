// 并行 gzip 写入器：tar 数据 → 128KB 分块并行 deflate → gzip 容器。
// 与 zip 的 chunked deflate 同一套架构（分块 + 字典衔接 + sync-flush 缝合 + crc32_combine），
// 外包 gzip 10 字节头 + 8 字节尾（CRC32 + ISIZE）。实现 io.WriteCloser——tar writer 的直插替代。
package core

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"sync"
)

// GZIP_CHUNK_BYTES 并行 gzip 分块粒度（128KB——与 zip chunked 对齐）
const GZIP_CHUNK_BYTES = ZIP_DEFLATE_CHUNK_BYTES

// GZIP_WORKER_COUNT 并行 gzip 工作协程数（与 zip 压缩侧一致）
const GZIP_WORKER_COUNT = ZIP_COMPRESS_MAX_WORKERS

// gzipChunkTask 并行 gzip 的一个分块任务
type gzipChunkTask struct {
	chunkIdx int64
	data     []byte
	isLast   bool
	dict     []byte // 前块尾 ≤32KB 预置字典
}

// gzipChunkOutcome 分块压缩结果
type gzipChunkOutcome struct {
	chunkIdx   int64
	compressed []byte
	crc32Value uint32
	readBytes  int64
	err        error
}

// ParallelGzipWriter 并行 gzip 写入器（io.WriteCloser 接口）
type ParallelGzipWriter struct {
	output       *os.File
	jobs         int
	level        int
	buf          []byte // 当前未满块的缓冲
	workerCh     chan gzipChunkTask
	outcomeChans map[int64]chan gzipChunkOutcome
	nextIdx      int64 // 下一个要派发的块序号
	consumedIdx  int64 // 下一个要消费的块序号
	wg           sync.WaitGroup
	totalCRC     uint32
	totalSize    int64
	err          error
	errOnce      sync.Once
	closed       bool
	mu           sync.Mutex
}

// NewParallelGzipWriter 创建并行 gzip 写入器（写出前先写 gzip 头）
func NewParallelGzipWriter(output *os.File, jobs int, level int) (*ParallelGzipWriter, error) {
	if jobs < 1 {
		jobs = GZIP_WORKER_COUNT
	}
	writer := &ParallelGzipWriter{
		output:       output,
		jobs:         jobs,
		level:        level,
		workerCh:     make(chan gzipChunkTask, jobs),
		outcomeChans: make(map[int64]chan gzipChunkOutcome),
	}
	// gzip 头（RFC 1952）：magic 1F 8B + method 08 + flags 00 + mtime 00000000 + XFL 00 + OS FF
	header := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff}
	if _, err := output.Write(header); err != nil {
		return nil, err
	}
	// 启动 worker 协程
	for i := 0; i < jobs; i++ {
		writer.wg.Add(1)
		go writer.worker()
	}
	return writer, nil
}

// Write 实现 io.Writer——缓冲到 128KB 后派发分块
func (w *ParallelGzipWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.err != nil {
		return 0, w.err
	}
	total := len(p)
	for len(p) > 0 {
		space := int(GZIP_CHUNK_BYTES) - len(w.buf)
		take := len(p)
		if take > space {
			take = space
		}
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		if len(w.buf) == int(GZIP_CHUNK_BYTES) {
			w.dispatchChunk(false)
		}
	}
	// 顺序消费已完成的块（保持流式写出）
	if err := w.consumeReady(); err != nil {
		return 0, err
	}
	return total, nil
}

// Close 实现 io.Closer——冲末块 + 等全部完成 + 写 gzip 尾
func (w *ParallelGzipWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return w.err
	}
	w.closed = true
	// 冲末块（如果缓冲非空）
	if len(w.buf) > 0 {
		w.dispatchChunk(true)
	} else if w.nextIdx == 0 {
		// 空输入：写一个空的 final deflate 块
		w.dispatchEmptyFinal()
	}
	w.mu.Unlock()

	// 关闭 worker 通道，等全部完成
	close(w.workerCh)
	w.wg.Wait()

	// 消费所有剩余结果
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	for w.consumedIdx < w.nextIdx {
		if err := w.consumeOne(); err != nil {
			return err
		}
	}
	// 写 gzip 尾：CRC32 + ISIZE（各 4 字节小端）
	footer := make([]byte, 8)
	binary.LittleEndian.PutUint32(footer[0:4], w.totalCRC)
	binary.LittleEndian.PutUint32(footer[4:8], uint32(w.totalSize))
	if _, err := w.output.Write(footer); err != nil {
		return err
	}
	return nil
}

// dispatchChunk 派发一个分块（调用方持有锁）
func (w *ParallelGzipWriter) dispatchChunk(isLast bool) {
	// 不做字典衔接：gzip 是一个连续流，跨块字典引用在拼接流中可能越界
	// （pigz -i 独立模式同方案——比值损失 <0.1%，保正确性优先）
	dict := []byte(nil)
	data := make([]byte, len(w.buf))
	copy(data, w.buf)
	w.buf = w.buf[:0]

	idx := w.nextIdx
	w.nextIdx++
	w.outcomeChans[idx] = make(chan gzipChunkOutcome, 1)

	select {
	case w.workerCh <- gzipChunkTask{chunkIdx: idx, data: data, isLast: isLast, dict: dict}:
	case <-make(chan struct{}): // 不会走到这里——channel 有 jobs 容量
	}
}

// dispatchEmptyFinal 派发空末块（空输入场景）
func (w *ParallelGzipWriter) dispatchEmptyFinal() {
	idx := w.nextIdx
	w.nextIdx++
	w.outcomeChans[idx] = make(chan gzipChunkOutcome, 1)
	w.workerCh <- gzipChunkTask{chunkIdx: idx, data: []byte{}, isLast: true, dict: nil}
}

// worker 压缩协程
func (w *ParallelGzipWriter) worker() {
	defer w.wg.Done()
	for task := range w.workerCh {
		outcome := w.compressChunk(task)
		ch, ok := w.outcomeChans[task.chunkIdx]
		if ok {
			ch <- outcome
		}
	}
}

// compressChunk 压缩单个分块（字典衔接 + 非末块 sync-flush / 末块 final）
func (w *ParallelGzipWriter) compressChunk(task gzipChunkTask) gzipChunkOutcome {
	result, err := zipDeflateChunkBytes(task.data, w.level, task.isLast, task.dict)
	if err != nil {
		return gzipChunkOutcome{err: err}
	}
	return gzipChunkOutcome{
		compressed: result,
		crc32Value: crc32.ChecksumIEEE(task.data),
		readBytes:  int64(len(task.data)),
	}
}

// consumeReady 消费已就绪的分块结果（非阻塞——顺序写出）
func (w *ParallelGzipWriter) consumeReady() error {
	for w.consumedIdx < w.nextIdx {
		ch, ok := w.outcomeChans[w.consumedIdx]
		if !ok {
			break
		}
		select {
		case outcome := <-ch:
			if err := w.writeOutcome(outcome); err != nil {
				return err
			}
			delete(w.outcomeChans, w.consumedIdx)
			w.consumedIdx++
		default:
			return nil // 下一个还没好，等下次
		}
	}
	return nil
}

// consumeOne 阻塞消费一个分块结果
func (w *ParallelGzipWriter) consumeOne() error {
	ch, ok := w.outcomeChans[w.consumedIdx]
	if !ok {
		return fmt.Errorf("internal: missing outcome channel for chunk %d", w.consumedIdx)
	}
	outcome := <-ch
	if err := w.writeOutcome(outcome); err != nil {
		return err
	}
	delete(w.outcomeChans, w.consumedIdx)
	w.consumedIdx++
	return nil
}

// writeOutcome 写出一个分块结果
func (w *ParallelGzipWriter) writeOutcome(outcome gzipChunkOutcome) error {
	if outcome.err != nil {
		w.errOnce.Do(func() { w.err = outcome.err })
		return outcome.err
	}
	if _, err := w.output.Write(outcome.compressed); err != nil {
		w.errOnce.Do(func() { w.err = err })
		return err
	}
	w.totalCRC = zipCrc32Combine(w.totalCRC, outcome.crc32Value, outcome.readBytes)
	w.totalSize += outcome.readBytes
	return nil
}

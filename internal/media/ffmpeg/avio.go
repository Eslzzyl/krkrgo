package ffmpeg

import (
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

type memoryReader struct {
	mu     sync.Mutex
	data   []byte
	pos    int64
	closed bool
}

func (r *memoryReader) read(dst []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return int(avErrorEOF)
	}
	if r.pos >= int64(len(r.data)) {
		return int(avErrorEOF)
	}
	available := len(r.data) - int(r.pos)
	if len(dst) > available {
		dst = dst[:available]
	}
	copy(dst, r.data[r.pos:r.pos+int64(len(dst))])
	r.pos += int64(len(dst))
	return len(dst)
}

func (r *memoryReader) seek(offset int64, whence int) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return -1
	}
	if whence&avSeekSize != 0 {
		return int64(len(r.data))
	}
	var base int64
	switch whence & 0xffff {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = r.pos
	case io.SeekEnd:
		base = int64(len(r.data))
	default:
		return -1
	}
	position := base + offset
	if position < 0 || position > int64(len(r.data)) {
		return -1
	}
	r.pos = position
	return position
}

func (r *memoryReader) close() {
	r.mu.Lock()
	r.closed = true
	r.data = nil
	r.mu.Unlock()
}

var callbackState = struct {
	sync.RWMutex
	next uint64
	read map[uint64]*memoryReader
}{read: make(map[uint64]*memoryReader)}

var (
	avioReadCallback = purego.NewCallback(func(handle uintptr, buffer uintptr, size int32) int32 {
		callbackState.RLock()
		reader := callbackState.read[uint64(handle)]
		callbackState.RUnlock()
		if reader == nil || buffer == 0 || size <= 0 {
			return avErrorEOF
		}
		data := unsafe.Slice((*byte)(unsafe.Pointer(buffer)), int(size))
		return int32(reader.read(data))
	})
	avioSeekCallback = purego.NewCallback(func(handle uintptr, offset int64, whence int32) int64 {
		callbackState.RLock()
		reader := callbackState.read[uint64(handle)]
		callbackState.RUnlock()
		if reader == nil {
			return -1
		}
		return reader.seek(offset, int(whence))
	})
)

func addReader(reader *memoryReader) uintptr {
	id := atomic.AddUint64(&callbackState.next, 1)
	callbackState.Lock()
	callbackState.read[id] = reader
	callbackState.Unlock()
	return uintptr(id)
}

func removeReader(id uintptr) {
	callbackState.Lock()
	delete(callbackState.read, uint64(id))
	callbackState.Unlock()
}

type avioState struct {
	api    *api
	reader *memoryReader
	handle uintptr
	ctx    uintptr
}

func newAVIO(a *api, data []byte) (*avioState, error) {
	reader := &memoryReader{data: append([]byte(nil), data...)}
	handle := addReader(reader)
	buffer := a.avMalloc(32 * 1024)
	if buffer == 0 {
		removeReader(handle)
		return nil, ErrUnavailable
	}
	ctx := a.avioAllocContext((*byte)(unsafe.Pointer(buffer)), 32*1024, 0, handle, avioReadCallback, 0, avioSeekCallback)
	if ctx == 0 {
		a.avFree(buffer)
		removeReader(handle)
		return nil, ErrUnavailable
	}
	runtime.KeepAlive(reader)
	return &avioState{api: a, reader: reader, handle: handle, ctx: ctx}, nil
}

func (s *avioState) close() {
	if s == nil {
		return
	}
	if s.ctx != 0 {
		ctx := s.ctx
		s.api.avioContextFree(&ctx)
		s.ctx = 0
	}
	removeReader(s.handle)
	s.reader.close()
	s.handle = 0
}

package host

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

const (
	// DefaultAudioBufferFrames is the amount of output queued by default. The
	// runtime produces 48 kHz stereo output, so this is 100 ms of audio.
	DefaultAudioBufferFrames = runtime.OutputRate / 10
	// MaxAudioBufferFrames keeps an embedding application's requested queue
	// bounded even when it comes from configuration.
	MaxAudioBufferFrames = runtime.OutputRate
)

var (
	ErrAudioOutputClosed       = errors.New("audio output is closed")
	ErrAudioOutputBackpressure = errors.New("audio output buffer is full")
	ErrAudioOutputFormat       = errors.New("audio output requires interleaved stereo float32")
)

// AudioOutputOptions configures the opt-in Ebitengine audio adapter.
//
// Context must be created by the caller with audio.NewContext(runtime.OutputRate).
// BufferFrames is a bounded queue capacity; zero selects
// DefaultAudioBufferFrames.
type AudioOutputOptions struct {
	BufferFrames int
	Diagnostics  bool
}

// AudioOutputStats summarizes accepted PCM and queue wait diagnostics.
type AudioOutputStats struct {
	Blocks            uint64
	Frames            uint64
	NonZeroSamples    uint64
	Peak              float32
	QueueWaits        uint64
	QueueWaitNanos    uint64
	MaxQueueWaitNanos uint64
}

// AudioOutput feeds runtime.Session's fixed-rate stereo float32 output into an
// Ebitengine audio player. The output owns the player and queue, but not the
// Context supplied to NewAudioOutput.
type AudioOutput struct {
	queue       *audioQueue
	player      *audio.Player
	diagnostics bool
	stats       AudioOutputStats

	mu      sync.Mutex
	closed  bool
	started bool
}

// NewAudioOutput creates a stopped output adapter for an explicitly created
// Ebitengine audio context. The context sample rate must be runtime.OutputRate;
// Ebitengine contexts use one process-wide sample rate.
func NewAudioOutput(context *audio.Context, options AudioOutputOptions) (*AudioOutput, error) {
	if context == nil {
		return nil, fmt.Errorf("nil audio context")
	}
	if context.SampleRate() != runtime.OutputRate {
		return nil, fmt.Errorf("audio context sample rate %d, want %d", context.SampleRate(), runtime.OutputRate)
	}
	frames := options.BufferFrames
	if frames == 0 {
		frames = DefaultAudioBufferFrames
	}
	if frames < 1 || frames > MaxAudioBufferFrames {
		return nil, fmt.Errorf("audio buffer frames %d outside [1,%d]", frames, MaxAudioBufferFrames)
	}
	queue := newAudioQueue(frames)
	player, err := context.NewPlayerF32(queue)
	if err != nil {
		queue.close()
		return nil, fmt.Errorf("create audio player: %w", err)
	}
	// Keep the device-side buffer close to the adapter's bounded queue. This
	// does not affect the runtime's deterministic mixer or its clock.
	bufferDuration := time.Duration((int64(frames)*int64(time.Second) + runtime.OutputRate - 1) / runtime.OutputRate)
	player.SetBufferSize(bufferDuration)
	return &AudioOutput{queue: queue, player: player, diagnostics: options.Diagnostics}, nil
}

// Start begins playback. AttachAudioOutput calls Start automatically; this is
// exposed for embedders that want to start an already-created output first.
func (o *AudioOutput) Start() error {
	if o == nil {
		return ErrAudioOutputClosed
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return ErrAudioOutputClosed
	}
	if !o.started {
		o.player.Play()
		o.started = true
	}
	return nil
}

// Pause suspends playback while retaining queued samples for a later resume.
func (o *AudioOutput) Pause() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || !o.started || o.player == nil {
		return
	}
	o.player.Pause()
}

// Resume continues playback after Pause.
func (o *AudioOutput) Resume() error {
	if o == nil {
		return ErrAudioOutputClosed
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.player == nil {
		return ErrAudioOutputClosed
	}
	if !o.started {
		o.started = true
	}
	o.player.Play()
	return nil
}

// Write queues one complete interleaved stereo float32 block. It copies the
// samples into the bounded queue. A full queue returns
// ErrAudioOutputBackpressure without partially accepting the block.
func (o *AudioOutput) Write(samples []float32) error {
	if o == nil || o.queue == nil {
		return ErrAudioOutputClosed
	}
	if len(samples)%2 != 0 {
		return ErrAudioOutputFormat
	}
	if err := o.queue.write(samples); err != nil {
		return err
	}
	if o.diagnostics {
		o.mu.Lock()
		o.stats.Blocks++
		o.stats.Frames += uint64(len(samples) / 2)
		for _, sample := range samples {
			if sample != 0 {
				o.stats.NonZeroSamples++
			}
			peak := float32(math.Abs(float64(sample)))
			if peak > o.stats.Peak {
				o.stats.Peak = peak
			}
		}
		o.mu.Unlock()
	}
	return nil
}

// Diagnostics reports cumulative accepted PCM and queue wait statistics when enabled.
func (o *AudioOutput) Diagnostics() AudioOutputStats {
	if o == nil || !o.diagnostics {
		return AudioOutputStats{}
	}
	o.mu.Lock()
	stats := o.stats
	o.mu.Unlock()
	if o.queue != nil {
		waits, waitNanos, maxWaitNanos := o.queue.waitStats()
		stats.QueueWaits = waits
		stats.QueueWaitNanos = waitNanos
		stats.MaxQueueWaitNanos = maxWaitNanos
	}
	return stats
}

// PendingFrames reports the number of queued stereo frames.
func (o *AudioOutput) PendingFrames() int {
	if o == nil || o.queue == nil {
		return 0
	}
	return o.queue.pendingFrames()
}

// CapacityFrames reports the fixed queue capacity.
func (o *AudioOutput) CapacityFrames() int {
	if o == nil || o.queue == nil {
		return 0
	}
	return o.queue.capacityFrames()
}

// Close stops the Ebitengine player and wakes any reader blocked on the
// adapter queue. Close is idempotent; queued samples are discarded.
func (o *AudioOutput) Close() error {
	if o == nil || o.queue == nil {
		return nil
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	o.mu.Unlock()

	o.queue.close()
	if o.player != nil {
		o.player.PauseAndStopReading()
	}
	return nil
}

func (o *AudioOutput) isClosed() bool {
	if o == nil {
		return true
	}
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	return closed
}

// AttachAudioOutput opts a Game into device output. The Game does not create
// or own the Context and does not close the output; callers retain lifecycle
// ownership and should close it after RunGame returns.
func (g *Game) AttachAudioOutput(output *AudioOutput) error {
	if g == nil {
		return fmt.Errorf("nil host game")
	}
	if output == nil || output.queue == nil || output.isClosed() {
		return ErrAudioOutputClosed
	}
	if g.audioOutput != nil && g.audioOutput != output {
		return fmt.Errorf("audio output already attached")
	}
	if err := output.Start(); err != nil {
		return err
	}
	g.audioOutput = output
	g.audioClock = time.Time{}
	g.audioClockRemainder = 0
	return nil
}

// DetachAudioOutput removes the output from subsequent updates. It does not
// close the output, because its context and player are caller-owned.
func (g *Game) DetachAudioOutput() {
	if g != nil {
		g.audioOutput = nil
		g.audioClock = time.Time{}
		g.audioClockRemainder = 0
	}
}

const maxWallAudioDelta = time.Second

func (g *Game) audioDeltaMilliseconds(now time.Time) uint64 {
	if g == nil {
		return fixedTickMS
	}
	if g.audioClock.IsZero() {
		g.audioClock = now
		return fixedTickMS
	}
	elapsed := now.Sub(g.audioClock)
	g.audioClock = now
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > maxWallAudioDelta {
		elapsed = maxWallAudioDelta
		g.audioClockRemainder = 0
	}
	total := elapsed + g.audioClockRemainder
	milliseconds := total / time.Millisecond
	g.audioClockRemainder = total % time.Millisecond
	return uint64(milliseconds)
}

func (g *Game) limitAudioDeltaMilliseconds(milliseconds uint64) uint64 {
	if g == nil || g.audioOutput == nil || milliseconds == 0 {
		return milliseconds
	}
	capacity := g.audioOutput.CapacityFrames()
	pending := g.audioOutput.PendingFrames()
	if pending >= capacity {
		return 0
	}
	headroom := capacity - pending
	maxMilliseconds := uint64(headroom / (runtime.OutputRate / 1000))
	if milliseconds > maxMilliseconds {
		return maxMilliseconds
	}
	return milliseconds
}

func (g *Game) deliverAudio() error {
	if g == nil || g.audioOutput == nil || g.session == nil {
		return nil
	}
	if err := g.audioOutput.Write(g.session.AudioOutput); err != nil {
		return err
	}
	// AudioOutput is public for headless consumers. Once the adapter has
	// accepted the block, transfer ownership by clearing it without another
	// copy; TakeAudio remains the copying API for other callers.
	g.session.AudioOutput = nil
	return nil
}

type audioQueue struct {
	mu                sync.Mutex
	ready             *sync.Cond
	data              []float32
	head              int
	frames            int
	readByte          int
	closed            bool
	written           bool
	queueWaits        uint64
	queueWaitNanos    uint64
	maxQueueWaitNanos uint64
}

func newAudioQueue(capacityFrames int) *audioQueue {
	q := &audioQueue{data: make([]float32, capacityFrames*2)}
	q.ready = sync.NewCond(&q.mu)
	return q
}

func (q *audioQueue) capacityFrames() int {
	if q == nil {
		return 0
	}
	return len(q.data) / 2
}

func (q *audioQueue) pendingFrames() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	frames := q.frames
	q.mu.Unlock()
	return frames
}
func (q *audioQueue) waitStats() (uint64, uint64, uint64) {
	if q == nil {
		return 0, 0, 0
	}
	q.mu.Lock()
	waits, waitNanos, maxWaitNanos := q.queueWaits, q.queueWaitNanos, q.maxQueueWaitNanos
	q.mu.Unlock()
	return waits, waitNanos, maxWaitNanos
}

func (q *audioQueue) write(samples []float32) error {
	if q == nil {
		return ErrAudioOutputClosed
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrAudioOutputClosed
	}
	frames := len(samples) / 2
	if frames == 0 {
		return nil
	}
	if frames > len(q.data)/2-q.frames {
		return ErrAudioOutputBackpressure
	}
	capacity := len(q.data) / 2
	for frame := range frames {
		at := (q.head + q.frames + frame) % capacity
		copy(q.data[at*2:at*2+2], samples[frame*2:frame*2+2])
	}
	q.frames += frames
	q.written = true
	q.ready.Signal()
	return nil
}

func (q *audioQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.frames = 0
		q.head = 0
		q.readByte = 0
		q.ready.Broadcast()
	}
	q.mu.Unlock()
}

// Read emits little-endian float32 stereo bytes and preserves partial sample
// reads. A closed queue returns EOF after any in-progress read has stopped.
func (q *audioQueue) Read(p []byte) (int, error) {
	if q == nil {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var waitStarted time.Time
	if q.frames == 0 && !q.closed && q.written {
		waitStarted = time.Now()
	}
	for q.frames == 0 && !q.closed {
		q.ready.Wait()
	}
	if !waitStarted.IsZero() && !q.closed {
		waitNanos := uint64(time.Since(waitStarted).Nanoseconds())
		q.queueWaits++
		q.queueWaitNanos += waitNanos
		if waitNanos > q.maxQueueWaitNanos {
			q.maxQueueWaitNanos = waitNanos
		}
	}
	if q.frames == 0 {
		return 0, io.EOF
	}

	n := 0
	capacity := len(q.data) / 2
	for n < len(p) && q.frames > 0 {
		frame := q.data[q.head*2 : q.head*2+2]
		bits := math.Float32bits(frame[q.readByte/4])
		shift := uint((q.readByte % 4) * 8)
		p[n] = byte(bits >> shift)
		n++
		q.readByte++
		if q.readByte == 8 {
			q.readByte = 0
			frame[0], frame[1] = 0, 0
			q.head = (q.head + 1) % capacity
			q.frames--
		}
	}
	return n, nil
}

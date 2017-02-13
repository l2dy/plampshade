package lampshade

import (
	"io"
	"math"
	"sync"
	"time"
)

// receiveBuffer buffers incoming frames. It queues up available frames in a
// channel and makes sure that those are read in order when filling reader's
// buffers via the read() method. It also makes sure to send an ack whenever a
// queued frame has been fully read.
//
// In order to bound memory usage, the channel holds only <windowSize> frames,
// after which it starts back-pressuring. The sender knows not to send more
// than <windowSize> frames so as to prevent this. Once the sender receives an
// ACK from the receiver, it sends a subsequent frame and so on.
type receiveBuffer struct {
	defaultHeader []byte
	windowSize    int
	ackInterval   int
	unacked       int
	in            chan []byte
	ack           chan []byte
	pool          BufferPool
	poolable      []byte
	current       []byte
	closed        bool
	mx            sync.RWMutex
}

func newReceiveBuffer(defaultHeader []byte, ack chan []byte, pool BufferPool, windowSize int) *receiveBuffer {
	ackInterval := int(math.Ceil(float64(windowSize) / 10))
	return &receiveBuffer{
		defaultHeader: defaultHeader,
		windowSize:    windowSize,
		ackInterval:   ackInterval,
		in:            make(chan []byte, windowSize),
		ack:           ack,
		pool:          pool,
	}
}

// submit allows the session to submit a new frame to the receiveBuffer. If the
// receiveBuffer has been closed, this is a noop.
func (buf *receiveBuffer) submit(frame []byte) {
	buf.mx.RLock()
	closed := buf.closed
	if closed {
		buf.mx.RUnlock()
		return
	}
	buf.in <- frame
	buf.mx.RUnlock()
}

// reads available data into the given buffer. If no data is queued, read will
// wait up to deadline to receive some data. If deadline is Zero, read will wait
// indefinitely for new data.
//
// As long as some data was already queued, read will not wait for more data
// even if b has not yet been filled.
func (buf *receiveBuffer) read(b []byte, deadline time.Time) (totalN int, err error) {
	defer func() {
		if buf.unacked >= buf.ackInterval {
			buf.ack <- ackWithFrames(buf.defaultHeader, int16(buf.unacked))
			buf.unacked = 0
		}
	}()

	for {
		n := copy(b, buf.current)
		buf.current = buf.current[n:]
		totalN += n
		if n == len(b) {
			// nothing more to copy
			return
		}

		// b can hold more than we had in the current slice, try to read more if
		// immediately available.
		b = b[n:]
		select {
		case frame, open := <-buf.in:
			// Read next frame, continue loop
			if !open && frame == nil {
				// we've hit the end
				err = io.EOF
				return
			}
			buf.onFrame(frame)
			continue
		default:
			// nothing immediately available
			if totalN > 0 {
				// we've read something, return what we have
				return
			}

			// We haven't ready anything, wait up till deadline to read
			now := time.Now()
			if deadline.IsZero() {
				// Default deadline to something really large so that we effectively
				// don't time out.
				deadline = largeDeadline
			} else if deadline.Before(now) {
				// Deadline already past, don't bother doing anything
				return
			}
			timer := time.NewTimer(deadline.Sub(now))
			select {
			case <-timer.C:
				// Nothing read within deadline
				err = ErrTimeout
				timer.Stop()
				return
			case frame, open := <-buf.in:
				// Read next frame, continue loop
				timer.Stop()
				if !open && frame == nil {
					// we've hit the end
					err = io.EOF
					return
				}
				buf.onFrame(frame)
				continue
			}
		}
	}
}

func (buf *receiveBuffer) onFrame(frame []byte) {
	if buf.poolable != nil {
		// Return previous frame to pool
		buf.pool.Put(buf.poolable[:maxFrameSize])
	}
	buf.poolable = frame
	buf.current = frame[dataHeaderSize:]
	buf.unacked++
}

func (buf *receiveBuffer) close() {
	buf.mx.Lock()
	if !buf.closed {
		buf.closed = true
		close(buf.in)
	}
	buf.mx.Unlock()
}

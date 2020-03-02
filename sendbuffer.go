package lampshade

import (
	"sync"
	"syscall"
	"time"

	"github.com/getlantern/ops"
)

var (
	closeTimeout = 30 * time.Second
)

// sendBuffer buffers outgoing frames. It holds up to <windowSize> frames,
// after which it starts back-pressuring.
//
// It sends an initial <windowSize> frames. After that, in order to avoid
// filling the receiver's receiveBuffer, it waits for ACKs from the receiver
// before sending new frames.
//
// When closed normally it sends an RST frame to the receiver to indicate that
// the connection is closed. We handle this from sendBuffer so that we can
// ensure buffered frames are sent before sending the RST.
type sendBuffer struct {
	defaultHeader  []byte
	window         *window
	in             chan []byte
	closeRequested chan bool
	muClosing      sync.RWMutex
	closing        bool
	closed         sync.WaitGroup
	writeTimer     *time.Timer
}

func newSendBuffer(defaultHeader []byte, out chan []byte, windowSize int) *sendBuffer {
	buf := &sendBuffer{
		defaultHeader:  defaultHeader,
		window:         newWindow(windowSize),
		in:             make(chan []byte, windowSize),
		closeRequested: make(chan bool, 1),
		writeTimer:     time.NewTimer(largeTimeout),
	}
	buf.closed.Add(1)
	ops.Go(func() { buf.sendLoop(out) })
	return buf
}

func (buf *sendBuffer) sendLoop(out chan []byte) {
	closeTimedOut := make(chan interface{})

	write := func(b []byte) {
		select {
		case out <- b:
			// okay
		case <-closeTimedOut:
			// closed before data could be sent
		}
	}

	sendRST := false
	defer func() {
		if sendRST {
			// Send an RST frame with the streamID
			write(withFrameType(buf.defaultHeader, frameTypeRST))
		}
		buf.closed.Done()
	}()

	var closeOnce sync.Once
	signalClose := func() {
		closeOnce.Do(func() {
			go func() {
				buf.muClosing.Lock()
				buf.closing = true
				close(buf.in)
				buf.muClosing.Unlock()
				time.Sleep(closeTimeout)
				close(closeTimedOut)
			}()
		})
	}

	for {
		select {
		case frame, open := <-buf.in:
			if frame != nil {
				windowAvailable := buf.window.sub(1)
				select {
				case <-windowAvailable:
					// send allowed
					write(append(frame, buf.defaultHeader...))
				case sendRST = <-buf.closeRequested:
					// close requested before window available
					signalClose()
					select {
					case <-windowAvailable:
						// send allowed
						write(append(frame, buf.defaultHeader...))
					case <-closeTimedOut:
						// closed before window available
						return
					}
				}
			}
			if !open {
				// We've closed
				return
			}
		case sendRST = <-buf.closeRequested:
			signalClose()
		case <-closeTimedOut:
			// We had queued writes, but we haven't gotten any acks within
			// closeTimeout of closing, don't wait any longer
			return
		}
	}
}

func (buf *sendBuffer) send(b []byte, writeDeadline time.Time) (int, error) {
	buf.muClosing.RLock()
	n, err := buf.doSend(b, writeDeadline)
	buf.muClosing.RUnlock()
	return n, err
}

func (buf *sendBuffer) doSend(b []byte, writeDeadline time.Time) (int, error) {
	if buf.closing {
		return 0, syscall.EPIPE
	}

	if writeDeadline.IsZero() {
		// Don't bother implementing a timeout
		buf.in <- b
		return len(b), nil
	}

	now := time.Now()
	if writeDeadline.Before(now) {
		return 0, ErrTimeout
	}
	if !buf.writeTimer.Stop() {
		<-buf.writeTimer.C
	}
	buf.writeTimer.Reset(writeDeadline.Sub(now))
	select {
	case buf.in <- b:
		return len(b), nil
	case <-buf.writeTimer.C:
		return 0, ErrTimeout
	}
}

func (buf *sendBuffer) close(sendRST bool) {
	select {
	case buf.closeRequested <- sendRST:
		// okay
	default:
		// close already requested, ignore
	}
	if !buf.writeTimer.Stop() {
		<-buf.writeTimer.C
	}
	buf.closed.Wait()
}

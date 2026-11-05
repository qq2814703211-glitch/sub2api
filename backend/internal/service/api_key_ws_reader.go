package service

import (
	"context"
	"errors"
	coderws "github.com/coder/websocket"
)

type apiKeyWSReaderKey struct{}
type apiKeyWSReader struct {
	conn     *coderws.Conn
	messages chan openAIWSClientReadResult
}

// WithAPIKeyWSReader keeps the sole socket reader running while a turn waits
// for key admission. Peer closure cancels the wait even after HTTP hijacking.
// Bounded prefetch preserves later frames without allowing unbounded buffering.
func WithAPIKeyWSReader(parent context.Context, conn *coderws.Conn) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	reader := &apiKeyWSReader{conn: conn, messages: make(chan openAIWSClientReadResult, 2)}
	ctx = context.WithValue(ctx, apiKeyWSReaderKey{}, reader)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(reader.messages)
		for {
			typ, payload, err := conn.Read(ctx)
			if err != nil {
				cancel(err)
				return
			}
			select {
			case reader.messages <- openAIWSClientReadResult{messageType: typ, payload: payload}:
			default:
				cancel(errors.New("too many pending websocket requests"))
				_ = conn.CloseNow()
				return
			}
		}
	}()
	return ctx, func() { cancel(context.Canceled); _ = conn.CloseNow(); <-done }
}

func readAPIKeyWSMessage(ctx context.Context, conn *coderws.Conn) (coderws.MessageType, []byte, error) {
	if reader, ok := ctx.Value(apiKeyWSReaderKey{}).(*apiKeyWSReader); ok && reader.conn == conn {
		select {
		case <-ctx.Done():
			return 0, nil, context.Cause(ctx)
		case result, ok := <-reader.messages:
			if !ok {
				return 0, nil, context.Cause(ctx)
			}
			return result.messageType, result.payload, result.err
		}
	}
	return conn.Read(context.Background())
}

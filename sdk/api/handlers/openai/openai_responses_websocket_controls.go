package openai

import (
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

type responsesWebsocketControlSender interface {
	SendExecutionSessionControl(sessionID string, payload []byte) error
}

// Keep a single downstream reader running during generation so interrupt controls
// reach the current upstream response instead of becoming a later create request.
func readResponsesWebsocketMessages(conn *websocket.Conn, done <-chan struct{}) (<-chan []byte, <-chan []byte, <-chan error) {
	requests := make(chan []byte, 1)
	controls := make(chan []byte)
	readErrors := make(chan error, 1)
	go func() {
		for {
			msgType, payload, errRead := conn.ReadMessage()
			if errRead != nil {
				readErrors <- errRead
				return
			}
			if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
				continue
			}
			target := requests
			if gjson.ValidBytes(payload) && gjson.GetBytes(payload, "type").String() == wsRequestTypeInterrupt {
				target = controls
			}
			select {
			case target <- payload:
			case <-done:
				return
			}
		}
	}()
	return requests, controls, readErrors
}

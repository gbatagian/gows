package httpsrv

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"goapp/internal/pkg/watcher"

	"github.com/gorilla/websocket"
)

func (s *Server) handlerWebSocket(w http.ResponseWriter, r *http.Request) {
	// Create and start a watcher.
	s.activeWsSem <- true              // Block once channel capacity is reached
	defer func() { <-s.activeWsSem }() // Release a slot once a web socket is closed

	var watch = watcher.New()
	if err := watch.Start(); err != nil {
		s.error(w, http.StatusInternalServerError, fmt.Errorf("failed to start watcher: %w", err))
		return
	}
	defer watch.Stop()

	s.addWatcher(watch)
	defer s.removeWatcher(watch)

	// Start WS.
	var upgrader = websocket.Upgrader{
		CheckOrigin: s.checkOrigin,
	}

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.error(w, http.StatusInternalServerError, fmt.Errorf("failed to upgrade connection: %w", err))
		return
	}
	defer func() { _ = c.Close() }()

	log.Printf("websocket started for watcher %s\n", watch.GetWatcherId())
	defer func() {
		log.Printf("websocket stopped for watcher %s\n", watch.GetWatcherId())
	}()

	// Read done.
	readDoneCh := make(chan struct{})

	// All done.
	doneCh := make(chan struct{})
	defer close(doneCh)

	// Read messages from client
	go func() {
		defer close(readDoneCh)
		for {
			select {
			default:
				_, p, err := c.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
						log.Printf("failed to read message: %v\n", err)
					}
					return
				}
				var m watcher.CounterReset
				if err := json.Unmarshal(p, &m); err != nil {
					log.Printf("failed to unmarshal message: %v\n", err)
					continue
				}
				watch.ResetCounter()
			case <-doneCh:
				return
			case <-s.quitChannel:
				return
			}
		}
	}()

	// Write messages to client
	for {
		select {
		case cv := <-watch.Recv():
			data, _ := json.Marshal(cv)
			err = c.WriteMessage(websocket.TextMessage, data)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("failed to write message: %v\n", err)
				}
				return
			}
		case <-readDoneCh:
			return
		case <-s.quitChannel:
			return
		}
	}
}

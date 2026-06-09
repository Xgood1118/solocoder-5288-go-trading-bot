package web

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
)

func (ws *WebServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	ws.mu.Lock()
	ws.clients[conn] = true
	ws.mu.Unlock()

	defer func() {
		ws.mu.Lock()
		delete(ws.clients, conn)
		ws.mu.Unlock()
		conn.Close()
	}()

	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	ws.sendInitialState(conn)

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
			}
			break
		}
	}
}

func (ws *WebServer) sendInitialState(conn *websocket.Conn) {
	bestBid, _ := ws.market.GetBestBid()
	bestAsk, _ := ws.market.GetBestAsk()
	currentPrice := int64(0)
	if bestBid > 0 && bestAsk > 0 {
		currentPrice = (bestBid + bestAsk) / 2
	}

	account := map[string]interface{}{
		"balance": ws.liveEngine.GetBalance(),
		"equity":  ws.liveEngine.GetEquity(currentPrice),
		"price":   currentPrice,
	}
	conn.WriteJSON(WSMessage{Type: "account", Data: account})

	positions := ws.positionMgr.GetAllPositions()
	posList := make([]map[string]interface{}, 0, len(positions))
	for _, pos := range positions {
		unrealizedPnL := ws.positionMgr.GetUnrealizedPnL(pos.Symbol, bestBid)
		posList = append(posList, map[string]interface{}{
			"symbol":        pos.Symbol,
			"quantity":      pos.Quantity,
			"avgPrice":      pos.AvgPrice,
			"realizedPnL":   pos.RealizedPnL,
			"unrealizedPnL": unrealizedPnL,
		})
	}
	conn.WriteJSON(WSMessage{Type: "position", Data: posList})

	ob := ws.market.GetDepth(10)
	conn.WriteJSON(WSMessage{Type: "orderbook", Data: ob})

	strategies := ws.liveEngine.GetStrategies()
	running := ws.liveEngine.IsRunning()
	stratList := make([]map[string]interface{}, 0, len(strategies))
	for _, s := range strategies {
		stratList = append(stratList, map[string]interface{}{
			"id":      s.GetID(),
			"symbol":  s.GetSymbol(),
			"running": running,
		})
	}
	conn.WriteJSON(WSMessage{Type: "strategy", Data: stratList})

	bestBid, _ = ws.market.GetBestBid()
	prices := map[string]int64{
		ws.market.Symbol(): bestBid,
	}
	riskInfo := map[string]interface{}{
		"dailyPnL":      ws.riskMgr.GetDailyPnL(),
		"circuitBroken": ws.riskMgr.IsCircuitBroken(),
		"totalExposure": ws.riskMgr.GetTotalExposure(prices),
	}
	conn.WriteJSON(WSMessage{Type: "risk", Data: riskInfo})

	trades := ws.reportGen.ListTradeDetails(20, 0)
	conn.WriteJSON(WSMessage{Type: "trade", Data: trades})
}

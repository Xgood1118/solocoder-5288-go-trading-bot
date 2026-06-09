package web

import (
	"context"
	"embed"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"trading-bot/internal/live"
	"trading-bot/internal/market"
	"trading-bot/internal/order"
	"trading-bot/internal/position"
	"trading-bot/internal/report"
	"trading-bot/internal/risk"
	"trading-bot/pkg/types"
)

//go:embed templates/*
var templatesFS embed.FS

type WebServer struct {
	router      *chi.Mux
	addr        string
	server      *http.Server
	liveEngine  *live.Engine
	market      *market.Market
	positionMgr *position.Manager
	orderMgr    *order.Manager
	riskMgr     *risk.Manager
	reportGen   *report.ReportGenerator
	upgrader    websocket.Upgrader
	clients     map[*websocket.Conn]bool
	broadcast   chan WSMessage
	mu          sync.RWMutex
	templates   *template.Template
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

type WSMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func NewServer(addr string, liveEngine *live.Engine, market *market.Market, posMgr *position.Manager, orderMgr *order.Manager, riskMgr *risk.Manager, reportGen *report.ReportGenerator) *WebServer {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		panic(err)
	}

	ws := &WebServer{
		router:      chi.NewRouter(),
		addr:        addr,
		liveEngine:  liveEngine,
		market:      market,
		positionMgr: posMgr,
		orderMgr:    orderMgr,
		riskMgr:     riskMgr,
		reportGen:   reportGen,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan WSMessage, 256),
		templates: tmpl,
		stopChan:  make(chan struct{}),
	}

	ws.setupRoutes()
	return ws
}

func (ws *WebServer) setupRoutes() {
	ws.router.Get("/", ws.handleIndex)
	ws.router.Get("/ws", ws.handleWebSocket)

	ws.router.Route("/api", func(r chi.Router) {
		r.Get("/account", ws.handleGetAccount)
		r.Get("/positions", ws.handleGetPositions)
		r.Get("/orders", ws.handleGetOrders)
		r.Get("/trades", ws.handleGetTrades)
		r.Get("/strategies", ws.handleGetStrategies)
		r.Post("/strategies/{id}/start", ws.handleStartStrategy)
		r.Post("/strategies/{id}/stop", ws.handleStopStrategy)
		r.Get("/risk", ws.handleGetRisk)
		r.Get("/orderbook", ws.handleGetOrderBook)
		r.Get("/metrics", ws.handleGetMetrics)
	})
}

func (ws *WebServer) Start() error {
	ws.server = &http.Server{
		Addr:    ws.addr,
		Handler: ws.router,
	}

	ws.wg.Add(2)
	go ws.broadcastLoop()
	go ws.pushLoop()

	errChan := make(chan error, 1)
	go func() {
		if err := ws.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func (ws *WebServer) Stop() error {
	close(ws.stopChan)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := ws.server.Shutdown(ctx); err != nil {
		return err
	}

	ws.wg.Wait()

	ws.mu.Lock()
	for conn := range ws.clients {
		conn.Close()
		delete(ws.clients, conn)
	}
	ws.mu.Unlock()

	return nil
}

func (ws *WebServer) Address() string {
	return ws.addr
}

func (ws *WebServer) broadcastLoop() {
	defer ws.wg.Done()

	for {
		select {
		case <-ws.stopChan:
			return
		case msg, ok := <-ws.broadcast:
			if !ok {
				return
			}
			ws.sendToAllClients(msg)
		}
	}
}

func (ws *WebServer) pushLoop() {
	defer ws.wg.Done()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	tradeChan := ws.liveEngine.TradeChan()

	for {
		select {
		case <-ws.stopChan:
			return
		case <-ticker.C:
			ws.pushAccountUpdate()
			ws.pushPositionUpdate()
			ws.pushOrderBookUpdate()
			ws.pushStrategyUpdate()
			ws.pushRiskUpdate()
		case trade, ok := <-tradeChan:
			if ok {
				ws.reportGen.AddTrade(trade)
				ws.pushTradeUpdate(trade)
			}
		}
	}
}

func (ws *WebServer) pushAccountUpdate() {
	bestBid, _ := ws.market.GetBestBid()
	bestAsk, _ := ws.market.GetBestAsk()
	currentPrice := int64(0)
	if bestBid > 0 && bestAsk > 0 {
		currentPrice = (bestBid + bestAsk) / 2
	}

	balance := ws.liveEngine.GetBalance()
	equity := ws.liveEngine.GetEquity(currentPrice)

	ws.reportGen.SetBalance(balance)
	ws.reportGen.SetEquity(equity)

	account := map[string]interface{}{
		"balance": balance,
		"equity":  equity,
		"price":   currentPrice,
	}

	select {
	case ws.broadcast <- WSMessage{Type: "account", Data: account}:
	default:
	}
}

func (ws *WebServer) pushPositionUpdate() {
	positions := ws.positionMgr.GetAllPositions()

	result := make([]map[string]interface{}, 0, len(positions))
	for _, pos := range positions {
		bestBid, _ := ws.market.GetBestBid()
		unrealizedPnL := ws.positionMgr.GetUnrealizedPnL(pos.Symbol, bestBid)
		result = append(result, map[string]interface{}{
			"symbol":        pos.Symbol,
			"quantity":      pos.Quantity,
			"avgPrice":      pos.AvgPrice,
			"realizedPnL":   pos.RealizedPnL,
			"unrealizedPnL": unrealizedPnL,
		})
	}

	select {
	case ws.broadcast <- WSMessage{Type: "position", Data: result}:
	default:
	}
}

func (ws *WebServer) pushOrderBookUpdate() {
	ob := ws.market.GetDepth(10)
	select {
	case ws.broadcast <- WSMessage{Type: "orderbook", Data: ob}:
	default:
	}
}

func (ws *WebServer) pushStrategyUpdate() {
	strategies := ws.liveEngine.GetStrategies()
	running := ws.liveEngine.IsRunning()

	result := make([]map[string]interface{}, 0, len(strategies))
	for _, s := range strategies {
		result = append(result, map[string]interface{}{
			"id":      s.GetID(),
			"symbol":  s.GetSymbol(),
			"running": running,
		})
	}

	select {
	case ws.broadcast <- WSMessage{Type: "strategy", Data: result}:
	default:
	}
}

func (ws *WebServer) pushTradeUpdate(trade types.Trade) {
	select {
	case ws.broadcast <- WSMessage{Type: "trade", Data: trade}:
	default:
	}
}

func (ws *WebServer) pushRiskUpdate() {
	bestBid, _ := ws.market.GetBestBid()
	prices := map[string]int64{
		ws.market.Symbol(): bestBid,
	}

	riskInfo := map[string]interface{}{
		"dailyPnL":      ws.riskMgr.GetDailyPnL(),
		"circuitBroken": ws.riskMgr.IsCircuitBroken(),
		"totalExposure": ws.riskMgr.GetTotalExposure(prices),
	}

	select {
	case ws.broadcast <- WSMessage{Type: "risk", Data: riskInfo}:
	default:
	}
}

func (ws *WebServer) sendToAllClients(msg WSMessage) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	var failed []*websocket.Conn
	for conn := range ws.clients {
		if err := conn.WriteJSON(msg); err != nil {
			conn.Close()
			failed = append(failed, conn)
		}
	}
	for _, conn := range failed {
		delete(ws.clients, conn)
	}
}

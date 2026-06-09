package types

import (
	"math/big"
	"time"
)

const (
	SatoshiPerBTC int64 = 100000000
	SatoshiScale  int64 = 100000000
)

func CalcValue(quantity, price int64) int64 {
	return MulDiv(quantity, price, SatoshiScale)
}

func CalcQuantity(value, price int64) int64 {
	if price <= 0 {
		return 0
	}
	return MulDiv(value, SatoshiScale, price)
}

func MulDiv(a, b, div int64) int64 {
	if div == 0 {
		return 0
	}

	bigA := big.NewInt(a)
	bigB := big.NewInt(b)
	bigDiv := big.NewInt(div)

	bigResult := new(big.Int).Mul(bigA, bigB)
	bigResult.Div(bigResult, bigDiv)

	return bigResult.Int64()
}

type SignalType int

const (
	SignalNone SignalType = iota
	SignalBuy
	SignalSell
)

func (s SignalType) String() string {
	switch s {
	case SignalBuy:
		return "BUY"
	case SignalSell:
		return "SELL"
	default:
		return "NONE"
	}
	return "NONE"
}

type OrderType int

const (
	OrderTypeLimit OrderType = iota
	OrderTypeMarket
	OrderTypeStopLoss
	OrderTypeTakeProfit
)

func (o OrderType) String() string {
	switch o {
	case OrderTypeLimit:
		return "LIMIT"
	case OrderTypeMarket:
		return "MARKET"
	case OrderTypeStopLoss:
		return "STOP_LOSS"
	case OrderTypeTakeProfit:
		return "TAKE_PROFIT"
	default:
		return "UNKNOWN"
	}
}

type OrderSide int

const (
	SideBuy OrderSide = iota
	SideSell
)

func (s OrderSide) String() string {
	if s == SideBuy {
		return "BUY"
	}
	return "SELL"
}

type OrderStatus int

const (
	StatusPending OrderStatus = iota
	StatusOpen
	StatusFilled
	StatusPartiallyFilled
	StatusCancelled
	StatusRejected
	StatusExpired
)

func (s OrderStatus) String() string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusOpen:
		return "OPEN"
	case StatusFilled:
		return "FILLED"
	case StatusPartiallyFilled:
		return "PARTIALLY_FILLED"
	case StatusCancelled:
		return "CANCELLED"
	case StatusRejected:
		return "REJECTED"
	case StatusExpired:
		return "EXPIRED"
	default:
		return "UNKNOWN"
	}
}

type Order struct {
	ID            string
	StrategyID    string
	Symbol        string
	Type          OrderType
	Side          OrderSide
	Price         int64
	Quantity      int64
	FilledQty     int64
	Status        OrderStatus
	StopPrice     int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ExchangeTS      int64
}

type Trade struct {
	ID         string
	OrderID    string
	Symbol     string
	Side       OrderSide
	Price      int64
	Quantity   int64
	Fee        int64
	Timestamp  time.Time
	ExchangeTS int64
}

type Position struct {
	Symbol       string
	Quantity     int64
	AvgPrice   int64
	RealizedPnL int64
	UpdatedAt  time.Time
}

type Tick struct {
	Symbol     string
	Price      int64
	Volume     int64
	Timestamp  time.Time
	ExchangeTS int64
}

type Candle struct {
	Symbol     string
	Open       int64
	High       int64
	Low        int64
	Close      int64
	Volume     int64
	Timestamp  time.Time
	ExchangeTS int64
}

type OrderBookLevel struct {
	Price    int64
	Quantity int64
}

type OrderBook struct {
	Symbol    string
	Bids      []OrderBookLevel
	Asks      []OrderBookLevel
	Timestamp time.Time
}

type StrategyConfig struct {
	ID       string
	Name     string
	Type     string
	Symbol   string
	Enabled  bool
	Params   map[string]interface{}
}

type Account struct {
	Balances map[string]int64
}

type RiskConfig struct {
	MaxSingleOrderValue int64
	MaxPositionValue   int64
	DailyLossLimit  int64
	DisabledSymbols  []string
	CircuitBreakerPct float64
}

type BacktestResult struct {
	TotalReturn    float64
	SharpeRatio    float64
	MaxDrawdown    float64
	WinRate        float64
	TotalTrades    int
	WinningTrades  int
	LosingTrades   int
	MaxDrawdownStart time.Time
	MaxDrawdownEnd   time.Time
	FinalValue       int64
	InitialValue    int64
	Trades         []Trade
	DailyPnL       []DailyPnL
}

type DailyPnL struct {
	Date   time.Time
	PnL    int64
	Value  int64
}

type Strategy interface {
	OnInit()
	OnTick(price Tick) SignalType
	OnStop()
	GetID() string
	GetSymbol() string
}

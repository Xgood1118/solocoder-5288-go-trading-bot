package market

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"trading-bot/pkg/types"
)

type FeederConfig struct {
	Symbol      string
	InitialPrice int64
	Amplitude    int64
	Period       time.Duration
	Frequency    time.Duration
	NoiseStdDev  float64
}

type MockFeeder struct {
	config   FeederConfig
	tickChan chan types.Tick
	stopChan chan struct{}
	running  bool
	mu       sync.Mutex
	startTime time.Time
	rand     *rand.Rand
}

func NewMockFeeder(config FeederConfig) *MockFeeder {
	if config.Frequency <= 0 {
		config.Frequency = time.Second
	}
	if config.InitialPrice <= 0 {
		config.InitialPrice = 100 * types.SatoshiPerBTC
	}
	if config.Amplitude <= 0 {
		config.Amplitude = 10 * types.SatoshiPerBTC
	}
	if config.Period <= 0 {
		config.Period = 60 * time.Second
	}
	if config.NoiseStdDev <= 0 {
		config.NoiseStdDev = float64(config.Amplitude) * 0.05
	}

	return &MockFeeder{
		config:   config,
		tickChan: make(chan types.Tick, 1024),
		stopChan: make(chan struct{}),
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (f *MockFeeder) Start() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.running {
		return
	}

	f.running = true
	f.startTime = time.Now()
	f.stopChan = make(chan struct{})

	go f.feedLoop()
}

func (f *MockFeeder) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.running {
		return
	}

	f.running = false
	close(f.stopChan)
}

func (f *MockFeeder) TickChan() <-chan types.Tick {
	return f.tickChan
}

func (f *MockFeeder) feedLoop() {
	ticker := time.NewTicker(f.config.Frequency)
	defer ticker.Stop()

	for {
		select {
		case <-f.stopChan:
			return
		case t := <-ticker.C:
			tick := f.generateTick(t)
			select {
			case f.tickChan <- tick:
			default:
			}
		}
	}
}

func (f *MockFeeder) generateTick(t time.Time) types.Tick {
	elapsed := t.Sub(f.startTime)

	phase := 2 * math.Pi * float64(elapsed) / float64(f.config.Period)
	sineValue := math.Sin(phase)

	basePrice := float64(f.config.InitialPrice) + sineValue*float64(f.config.Amplitude)

	noise := f.gaussianNoise() * f.config.NoiseStdDev
	price := basePrice + noise

	if price < 0 {
		price = 1
	}

	intPrice := int64(math.Round(price))

	volume := f.rand.Int63n(int64(float64(f.config.InitialPrice)*0.01)) + 1

	return types.Tick{
		Symbol:     f.config.Symbol,
		Price:      intPrice,
		Volume:     volume,
		Timestamp:  t,
		ExchangeTS: t.UnixNano(),
	}
}

func (f *MockFeeder) gaussianNoise() float64 {
	u1 := f.rand.Float64()
	u2 := f.rand.Float64()

	if u1 == 0 {
		u1 = 0.0001
	}

	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

func (f *MockFeeder) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

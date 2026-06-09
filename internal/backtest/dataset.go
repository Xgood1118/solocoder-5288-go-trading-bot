package backtest

import (
	"math"
	"math/rand"
	"time"

	"trading-bot/pkg/types"
)

type DatasetConfig struct {
	Symbol      string
	InitialPrice int64
	Amplitude    int64
	Period       time.Duration
	NoiseStdDev  float64
	TimeStep     time.Duration
	Duration     time.Duration
}

func GenerateMockDataset(config DatasetConfig) []types.Tick {
	if config.TimeStep <= 0 {
		config.TimeStep = time.Second
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
	if config.Duration <= 0 {
		config.Duration = 24 * time.Hour
	}

	r := rand.New(rand.NewSource(42))

	numTicks := int(config.Duration / config.TimeStep)
	ticks := make([]types.Tick, 0, numTicks)

	startTime := time.Now().Truncate(config.TimeStep).Add(-config.Duration)

	for i := 0; i < numTicks; i++ {
		elapsed := time.Duration(i) * config.TimeStep
		t := startTime.Add(elapsed)

		phase := 2 * math.Pi * float64(elapsed) / float64(config.Period)
		sineValue := math.Sin(phase)

		basePrice := float64(config.InitialPrice) + sineValue*float64(config.Amplitude)

		noise := gaussianNoise(r) * config.NoiseStdDev
		price := basePrice + noise

		if price < 0 {
			price = 1
		}

		intPrice := int64(math.Round(price))

		volume := r.Int63n(int64(float64(config.InitialPrice)*0.01)) + 1

		ticks = append(ticks, types.Tick{
			Symbol:     config.Symbol,
			Price:      intPrice,
			Volume:     volume,
			Timestamp:  t,
			ExchangeTS: t.UnixNano(),
		})
	}

	return ticks
}

func gaussianNoise(r *rand.Rand) float64 {
	u1 := r.Float64()
	u2 := r.Float64()

	if u1 == 0 {
		u1 = 0.0001
	}

	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

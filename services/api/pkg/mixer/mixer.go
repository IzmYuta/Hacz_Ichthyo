package mixer

import (
	"context"
	"log"
	"sync"
	"time"
)

type MixerState string

const (
	MixerStateNormal MixerState = "normal"
	MixerStateDucked MixerState = "ducked"
)

type Mixer struct {
	mu           sync.RWMutex
	state        MixerState
	duckLevel    float64 // -12dB to -18dB
	duckDuration time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewMixer() *Mixer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Mixer{
		state:        MixerStateNormal,
		duckLevel:    -15.0,            // -15dB
		duckDuration: 30 * time.Second, // 30秒間ダッキング
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (m *Mixer) DuckOn() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == MixerStateDucked {
		return // 既にダッキング中
	}

	m.state = MixerStateDucked
	log.Printf("Mixer: Ducking ON (%.1fdB)", m.duckLevel)

	// 自動的にダッキング解除するタイマーを設定
	go func() {
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(m.duckDuration):
			m.DuckOff()
		}
	}()
}

func (m *Mixer) DuckOff() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == MixerStateNormal {
		return // 既に通常状態
	}

	m.state = MixerStateNormal
	log.Printf("Mixer: Ducking OFF (normal level)")
}

func (m *Mixer) GetState() MixerState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *Mixer) IsDucked() bool {
	return m.GetState() == MixerStateDucked
}

func (m *Mixer) SetDuckLevel(level float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// -30dB から 0dB の範囲に制限
	if level < -30.0 {
		level = -30.0
	} else if level > 0.0 {
		level = 0.0
	}

	m.duckLevel = level
	log.Printf("Mixer: Duck level set to %.1fdB", level)
}

func (m *Mixer) SetDuckDuration(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.duckDuration = duration
	log.Printf("Mixer: Duck duration set to %v", duration)
}

func (m *Mixer) GetDuckLevel() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.duckLevel
}

func (m *Mixer) GetDuckDuration() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.duckDuration
}

func (m *Mixer) Stop() {
	m.cancel()
	log.Println("Mixer stopped")
}

// 実際の音声処理では、この関数でLiveKitの音声トラックの音量を調整
func (m *Mixer) ProcessAudio(audioData []byte) []byte {
	m.mu.RLock()
	state := m.state
	duckLevel := m.duckLevel
	m.mu.RUnlock()

	if state == MixerStateDucked {
		// ダッキング適用（実際の実装では音声データの音量を調整）
		// ここでは簡略化
		log.Printf("Applying ducking: %.1fdB", duckLevel)
	}

	return audioData
}

package director

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type Segment string

const (
	SegmentOP     Segment = "OP"
	SegmentTOPIC  Segment = "TOPIC_A"
	SegmentQANDA  Segment = "QANDA"
	SegmentJINGLE Segment = "JINGLE"
	SegmentNEWS   Segment = "NEWS"
)

type Theme struct {
	Title string `json:"title"`
	Color string `json:"color"`
}

type NowPlaying struct {
	Theme     string    `json:"theme"`
	Segment   string    `json:"segment"`
	NextTick  time.Time `json:"nextTickAt"`
	Listeners int       `json:"listeners"`
}

type Director struct {
	mu           sync.RWMutex
	currentTheme Theme
	currentSeg   Segment
	nextTick     time.Time
	listeners    int
	themes       []Theme
	hostChannel  chan string
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewDirector(hostChannel chan string) *Director {
	ctx, cancel := context.WithCancel(context.Background())

	themes := []Theme{
		{Title: "深夜の音楽", Color: "#1a1a2e"},
		{Title: "朝のニュース", Color: "#16213e"},
		{Title: "午後のトーク", Color: "#0f3460"},
		{Title: "夜の物語", Color: "#533483"},
	}

	now := time.Now()
	hour := now.Hour()
	themeIndex := hour % len(themes)

	return &Director{
		currentTheme: themes[themeIndex],
		currentSeg:   SegmentOP,
		nextTick:     now.Add(15 * time.Minute),
		themes:       themes,
		hostChannel:  hostChannel,
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (d *Director) Start() {
	go d.tickLoop()
	log.Println("Program Director started")
}

func (d *Director) Stop() {
	d.cancel()
	log.Println("Program Director stopped")
}

func (d *Director) tickLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.tick()
		}
	}
}

func (d *Director) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// 正時チェック（テーマ切替）
	if d.shouldSwitchTheme(now) {
		d.switchTheme(now)
	}

	// 15分刻みセグメント進行
	if now.After(d.nextTick) {
		d.advanceSegment()
	}
}

func (d *Director) shouldSwitchTheme(now time.Time) bool {
	// 毎正時にテーマ切替
	return now.Minute() == 0 && now.Second() < 5
}

func (d *Director) switchTheme(now time.Time) {
	hour := now.Hour()
	themeIndex := hour % len(d.themes)
	d.currentTheme = d.themes[themeIndex]

	// Hostにテーマ変更を通知
	message := fmt.Sprintf("テーマが「%s」に変更されました。", d.currentTheme.Title)
	d.sendToHost(message)

	log.Printf("Theme switched to: %s", d.currentTheme.Title)
}

func (d *Director) advanceSegment() {
	// セグメント進行ロジック
	switch d.currentSeg {
	case SegmentOP:
		d.currentSeg = SegmentTOPIC
	case SegmentTOPIC:
		d.currentSeg = SegmentQANDA
	case SegmentQANDA:
		d.currentSeg = SegmentJINGLE
	case SegmentJINGLE:
		d.currentSeg = SegmentNEWS
	case SegmentNEWS:
		d.currentSeg = SegmentOP
	}

	// 次のティック時刻を設定
	d.nextTick = time.Now().Add(15 * time.Minute)

	// Hostにセグメント変更を通知
	message := fmt.Sprintf("セグメントが「%s」に変更されました。", d.currentSeg)
	d.sendToHost(message)

	log.Printf("Segment advanced to: %s", d.currentSeg)
}

func (d *Director) sendToHost(message string) {
	select {
	case d.hostChannel <- message:
	default:
		log.Printf("Host channel full, dropping message: %s", message)
	}
}

func (d *Director) GetNowPlaying() NowPlaying {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return NowPlaying{
		Theme:     d.currentTheme.Title,
		Segment:   string(d.currentSeg),
		NextTick:  d.nextTick,
		Listeners: d.listeners,
	}
}

func (d *Director) SetListeners(count int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listeners = count
}

func (d *Director) AdvanceSegment() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.advanceSegment()
}

func (d *Director) GetCurrentSegment() Segment {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentSeg
}

func (d *Director) IsQANDA() bool {
	return d.GetCurrentSegment() == SegmentQANDA
}

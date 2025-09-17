package director

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/radio24/director/pkg/host"
	"github.com/radio24/director/pkg/mcp"
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
	Theme      string    `json:"theme"`
	Segment    string    `json:"segment"`
	NextTick   time.Time `json:"nextTickAt"`
	Listeners  int       `json:"listeners"`
	Prompt     string    `json:"prompt"`
	QueueCount int       `json:"queueCount"`
	TopQueue   []string  `json:"topQueue"`
}

type Status struct {
	IsRunning    bool      `json:"isRunning"`
	CurrentTheme string    `json:"currentTheme"`
	CurrentSeg   string    `json:"currentSegment"`
	Uptime       time.Time `json:"uptime"`
	LastUpdate   time.Time `json:"lastUpdate"`
}

type Director struct {
	mu            sync.RWMutex
	currentTheme  Theme
	currentSeg    Segment
	nextTick      time.Time
	listeners     int
	themes        []Theme
	ctx           context.Context
	cancel        context.CancelFunc
	db            *sql.DB
	currentPrompt string
	queueCount    int
	topQueue      []string
	mcpClient     *mcp.MCPClient
	hostClient    *host.HostClient
	startTime     time.Time
	lastUpdate    time.Time
}

func NewDirector(db *sql.DB, hostClient *host.HostClient) *Director {
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

	director := &Director{
		currentTheme:  themes[themeIndex],
		currentSeg:    SegmentOP,
		nextTick:      now.Add(15 * time.Minute),
		themes:        themes,
		ctx:           ctx,
		cancel:        cancel,
		db:            db,
		currentPrompt: "24時間ラジオのメインパーソナリティとして、常にリスナーとつながりを持ちながら放送を続けましょう。",
		queueCount:    0,
		topQueue:      []string{},
		mcpClient:     mcp.NewMCPClient(),
		hostClient:    hostClient,
		startTime:     now,
		lastUpdate:    now,
	}

	// 初期スケジュール読み込み
	director.loadScheduleForHour(hour)

	return director
}

func (d *Director) Start() {
	// 初期プロンプトを設定
	if d.hostClient != nil {
		initialPrompt := d.GenerateHostPrompt()
		if err := d.hostClient.UpdatePrompt(initialPrompt); err != nil {
			log.Printf("Failed to set initial host prompt: %v", err)
		} else {
			log.Printf("Set initial host prompt")
		}
	}

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
	d.lastUpdate = now

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

	// スケジュール読み込み
	d.loadScheduleForHour(hour)

	// Hostにテーマ変更を通知
	message := fmt.Sprintf("テーマが「%s」に変更されました。", d.currentTheme.Title)
	d.sendInstructionToHost(message)

	// Hostのプロンプトを更新
	if d.hostClient != nil {
		newPrompt := d.GenerateHostPrompt()
		if err := d.hostClient.UpdatePrompt(newPrompt); err != nil {
			log.Printf("Failed to update host prompt: %v", err)
		} else {
			log.Printf("Updated host prompt for theme: %s", d.currentTheme.Title)
		}
	}

	log.Printf("Theme switched to: %s", d.currentTheme.Title)
}

func (d *Director) advanceSegment() {
	log.Printf("Starting segment advance from: %s", d.currentSeg)

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

	log.Printf("Segment changed to: %s", d.currentSeg)

	// 次のティック時刻を設定（セグメント時間を30分に延長）
	d.nextTick = time.Now().Add(30 * time.Minute)

	// Hostにセグメント変更を通知
	log.Printf("Sending instruction to host...")
	message := fmt.Sprintf("セグメントが「%s」に変更されました。", d.currentSeg)
	d.sendInstructionToHost(message)
	log.Printf("Instruction sent to host")

	// Hostのプロンプトを更新
	log.Printf("Updating host prompt...")
	if d.hostClient != nil {
		newPrompt := d.GenerateHostPrompt()
		log.Printf("Generated new prompt, length: %d", len(newPrompt))
		if err := d.hostClient.UpdatePrompt(newPrompt); err != nil {
			log.Printf("Failed to update host prompt: %v", err)
		} else {
			log.Printf("Updated host prompt for segment: %s", d.currentSeg)
		}
	} else {
		log.Printf("Host client is nil")
	}

	log.Printf("Segment advanced to: %s", d.currentSeg)
}

func (d *Director) sendInstructionToHost(message string) {
	if d.hostClient != nil {
		if err := d.hostClient.SendInstruction(message); err != nil {
			log.Printf("Failed to send instruction to host: %v", err)
		}
	}
}

func (d *Director) GetNowPlaying() NowPlaying {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return NowPlaying{
		Theme:      d.currentTheme.Title,
		Segment:    string(d.currentSeg),
		NextTick:   d.nextTick,
		Listeners:  d.listeners,
		Prompt:     d.currentPrompt,
		QueueCount: d.queueCount,
		TopQueue:   d.topQueue,
	}
}

func (d *Director) GetStatus() Status {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return Status{
		IsRunning:    d.ctx.Err() == nil,
		CurrentTheme: d.currentTheme.Title,
		CurrentSeg:   string(d.currentSeg),
		Uptime:       d.startTime,
		LastUpdate:   d.lastUpdate,
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

func (d *Director) SetTheme(themeTitle string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// テーマを検索して設定
	for _, theme := range d.themes {
		if theme.Title == themeTitle {
			d.currentTheme = theme
			d.loadScheduleForHour(time.Now().Hour())
			break
		}
	}
}

func (d *Director) GetCurrentSegment() Segment {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentSeg
}

func (d *Director) IsQANDA() bool {
	return d.GetCurrentSegment() == SegmentQANDA
}

// loadScheduleForHour 指定時間のスケジュールを読み込み
func (d *Director) loadScheduleForHour(hour int) {
	if d.db == nil {
		return
	}

	var prompt string
	err := d.db.QueryRow(`
		SELECT s.prompt 
		FROM schedule s 
		JOIN channel c ON s.channel_id = c.id 
		WHERE c.name = 'Radio-24' AND s.hour = $1
	`, hour).Scan(&prompt)

	if err != nil {
		log.Printf("Failed to load schedule for hour %d: %v", hour, err)
		return
	}

	d.currentPrompt = prompt
	log.Printf("Loaded schedule for hour %d: %s", hour, prompt)
}

// UpdateQueueInfo キュー情報を更新
func (d *Director) UpdateQueueInfo(count int, topItems []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queueCount = count
	d.topQueue = topItems
}

// GenerateHostPrompt 現在の状況に基づいてHost用プロンプトを生成
func (d *Director) GenerateHostPrompt() string {
	log.Printf("GenerateHostPrompt: Starting")
	// ロックは既に取得されている前提（呼び出し元で管理）
	log.Printf("GenerateHostPrompt: Using existing lock")

	now := time.Now()
	log.Printf("GenerateHostPrompt: Time calculated")
	remaining := d.nextTick.Sub(now)
	log.Printf("GenerateHostPrompt: Remaining time calculated")
	remainingStr := fmt.Sprintf("%02d:%02d", int(remaining.Minutes()), int(remaining.Seconds())%60)
	log.Printf("GenerateHostPrompt: Remaining string: %s", remainingStr)

	// キュー情報を文字列に変換
	queueInfo := ""
	if len(d.topQueue) > 0 {
		queueInfo = fmt.Sprintf("投稿キュー: %s", fmt.Sprintf("%v", d.topQueue))
	}
	log.Printf("GenerateHostPrompt: Queue info: %s", queueInfo)

	// MCPから文脈情報を取得（一時的に無効化）
	contextualInfo := ""
	log.Printf("GenerateHostPrompt: Contextual info: %s", contextualInfo)

	log.Printf("GenerateHostPrompt: Creating prompt...")
	prompt := fmt.Sprintf(`システム：あなたは24時間ラジオのメインパーソナリティ「マリン」。放送は切れ目なく続く。

いまのテーマ：%s、このセグメント：%s（残り%s）。
%s%s

【重要】ラジオパーソナリティとしての話し方：
* 1つの話題を深く掘り下げて、延々と話し続ける
* リスナーとの会話を想像しながら、自然な語りかけをする
* 体験談、エピソード、感想を織り交ぜて話す
* 「そうそう、そういえば...」「あ、そうそう...」「実はね...」など自然な接続詞を使う
* リスナーの反応を想像して「みなさんもそう思いますよね？」「きっと共感してくれると思います」など話しかける
* 話題が尽きそうになったら、関連する別の角度から話を続ける

【話し方のルール】：
* 無音を作らない。15秒以上の沈黙は禁止。
* 1つの話題を最低3-5分は話し続ける
* 音声は1度に30秒ぶん生成する
* セグメント終了5分前にクロージング、時報で次テーマ宣言。
* NGワード/個人情報は読み上げない。
* エラー時は「機材トラブル」と一言入れてから復旧。

現在の進行ガイダンス：%s`,
		d.currentTheme.Title,
		string(d.currentSeg),
		remainingStr,
		queueInfo,
		contextualInfo,
		d.currentPrompt)

	log.Printf("GenerateHostPrompt: Prompt created, length: %d", len(prompt))
	return prompt
}

// SendInstructionToHost Hostに指示を送信
func (d *Director) SendInstructionToHost(instruction string) error {
	if d.hostClient == nil {
		return fmt.Errorf("host client not initialized")
	}
	return d.hostClient.SendInstruction(instruction)
}

// UpdateHostPrompt Hostのプロンプトを更新
func (d *Director) UpdateHostPrompt(prompt string) error {
	if d.hostClient == nil {
		return fmt.Errorf("host client not initialized")
	}
	return d.hostClient.UpdatePrompt(prompt)
}

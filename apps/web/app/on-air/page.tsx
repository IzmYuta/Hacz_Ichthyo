'use client';

import { useEffect, useState, useRef } from 'react';
import { Room, RoomEvent, RemoteTrackPublication, RemoteAudioTrack } from 'livekit-client';

interface NowPlaying {
  theme: string;
  segment: string;
  nextTickAt: string;
  listeners: number;
}

export default function OnAir() {
  const [joined, setJoined] = useState(false);
  const [now, setNow] = useState<NowPlaying | null>(null);
  const [isPTTActive, setIsPTTActive] = useState(false);
  const [ws, setWs] = useState<WebSocket | null>(null);
  const roomRef = useRef<Room | null>(null);

  const API_BASE = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080';

  useEffect(() => {
    // WebSocket接続
    const wsUrl = API_BASE.replace('http', 'ws') + '/ws/ptt';
    const websocket = new WebSocket(wsUrl);
    
    websocket.onopen = () => {
      console.log('PTT WebSocket connected');
    };
    
    websocket.onmessage = (event) => {
      const data = JSON.parse(event.data);
      if (data.type === 'ptt_queued') {
        console.log('PTT queued:', data.id);
      }
    };
    
    setWs(websocket);
    
    return () => {
      websocket.close();
    };
  }, [API_BASE]);

  async function join() {
    try {
      const res = await fetch(`${API_BASE}/v1/room/join`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ identity: crypto.randomUUID() })
      });
      
      const { url, token } = await res.json();
      
      const room = new Room();
      await room.connect(url, token);
      
      roomRef.current = room;
      
      room.on(RoomEvent.TrackSubscribed, (_track, publication, _participant) => {
        const track = (publication as RemoteTrackPublication).track as RemoteAudioTrack;
        if (track) {
          track.attach(); // 自動再生
        }
      });
      
      room.on(RoomEvent.TrackUnsubscribed, (track, publication, _participant) => {
        const audioTrack = (publication as RemoteTrackPublication).track as RemoteAudioTrack;
        if (audioTrack) {
          audioTrack.detach();
        }
      });
      
      setJoined(true);
      pollNowPlaying();
    } catch (error) {
      console.error('Failed to join room:', error);
    }
  }

  async function pollNowPlaying() {
    try {
      const res = await fetch(`${API_BASE}/v1/now`);
      const data = await res.json();
      setNow(data);
    } catch (error) {
      console.error('Failed to fetch now playing:', error);
    }
    
    // 5秒ごとに更新
    setTimeout(pollNowPlaying, 5000);
  }

  function pttDown() {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    
    setIsPTTActive(true);
    
    // テキストPTTを送信（MVP版）
    const message = {
      type: 'ptt',
      kind: 'text',
      text: 'PTT投稿です'
    };
    
    ws.send(JSON.stringify(message));
  }

  function pttUp() {
    setIsPTTActive(false);
  }

  function leave() {
    if (roomRef.current) {
      roomRef.current.disconnect();
      roomRef.current = null;
    }
    setJoined(false);
    setNow(null);
  }

  return (
    <main className="min-h-screen bg-gradient-to-br from-purple-900 via-blue-900 to-indigo-900 text-white p-6">
      <div className="max-w-4xl mx-auto">
        <h1 className="text-4xl font-bold mb-8 text-center">
          Radio-24 — ON AIR
        </h1>
        
        {!joined ? (
          <div className="text-center">
            <button 
              onClick={join}
              className="bg-red-600 hover:bg-red-700 text-white font-bold py-4 px-8 rounded-full text-xl transition-colors"
            >
              🎧 放送に合流
            </button>
          </div>
        ) : (
          <div className="space-y-6">
            {/* 現在の放送情報 */}
            <div className="bg-black/30 backdrop-blur-sm rounded-lg p-6">
              <h2 className="text-2xl font-semibold mb-4">Now Playing</h2>
              {now ? (
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  <div>
                    <span className="text-gray-300">テーマ:</span>
                    <div className="text-xl font-bold" style={{ color: now.theme }}>
                      {now.theme}
                    </div>
                  </div>
                  <div>
                    <span className="text-gray-300">セグメント:</span>
                    <div className="text-xl font-bold">{now.segment}</div>
                  </div>
                  <div>
                    <span className="text-gray-300">リスナー数:</span>
                    <div className="text-xl font-bold">{now.listeners}</div>
                  </div>
                </div>
              ) : (
                <div className="text-gray-400">読み込み中...</div>
              )}
            </div>

            {/* PTTコントロール */}
            <div className="text-center">
              <button
                onMouseDown={pttDown}
                onMouseUp={pttUp}
                onTouchStart={pttDown}
                onTouchEnd={pttUp}
                className={`w-32 h-32 rounded-full text-4xl font-bold transition-all transform ${
                  isPTTActive 
                    ? 'bg-red-600 scale-110 shadow-lg shadow-red-500/50' 
                    : 'bg-gray-700 hover:bg-gray-600 hover:scale-105'
                }`}
              >
                🎙️
              </button>
              <div className="mt-4 text-sm text-gray-300">
                {isPTTActive ? '送信中...' : 'PTTボタンを押して話す'}
              </div>
            </div>

            {/* 離脱ボタン */}
            <div className="text-center">
              <button
                onClick={leave}
                className="bg-gray-600 hover:bg-gray-700 text-white font-bold py-2 px-6 rounded-full transition-colors"
              >
                放送から離脱
              </button>
            </div>

            {/* 字幕エリア */}
            <div className="bg-black/20 backdrop-blur-sm rounded-lg p-4 min-h-16">
              <div className="text-sm text-gray-300">
                字幕がここに表示されます...
              </div>
            </div>
          </div>
        )}
      </div>
    </main>
  );
}

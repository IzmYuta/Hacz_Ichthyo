'use client';

import { useRef, useState, useEffect } from 'react';
import { Box, Button, VStack, Text, HStack } from '@chakra-ui/react';
import { Room, RoomEvent, RemoteTrackPublication, RemoteAudioTrack } from 'livekit-client';

export default function OnAir() {
  const [connected, setConnected] = useState(false);
  const [subtitles, setSubtitles] = useState('');
  const [theme, setTheme] = useState({ title: 'Radio-24', color: '#1a1a2e' });
  const [ws, setWs] = useState<WebSocket | null>(null);
  const [broadcastWs, setBroadcastWs] = useState<WebSocket | null>(null);
  const [dialogueRequested, setDialogueRequested] = useState(false);
  const [dialogueActive, setDialogueActive] = useState(false);
  const [isRecording, setIsRecording] = useState(false);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  const roomRef = useRef<Room | null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement>(null);
  const subtitleTimeoutRef = useRef<NodeJS.Timeout | null>(null);

  const API_BASE = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080';

  useEffect(() => {
    // PTT WebSocket接続
    const wsUrl = API_BASE.replace('http', 'ws') + '/ws/ptt';
    const websocket = new WebSocket(wsUrl);
    
    websocket.onopen = () => {
      console.log('PTT WebSocket connected');
      // 接続時に現在の対話状態を確認
      checkDialogueStatus();
    };
    
    websocket.onmessage = (event) => {
      const data = JSON.parse(event.data);
      if (data.type === 'ptt_queued') {
        console.log('PTT queued:', data.id);
      } else if (data.type === 'dialogue_queued') {
        console.log('Dialogue queued:', data.id);
        setDialogueRequested(true);
      } else if (data.type === 'dialogue_end_ack') {
        console.log('Dialogue end acknowledged');
        setDialogueActive(false);
        setDialogueRequested(false);
      }
    };
    
    websocket.onclose = (event) => {
      console.log('PTT WebSocket closed:', event.code, event.reason);
      // WebSocket切断時に状態をリセット
      setDialogueActive(false);
      setDialogueRequested(false);
    };
    
    websocket.onerror = (error) => {
      console.error('PTT WebSocket error:', error);
      // エラー時も状態をリセット
      setDialogueActive(false);
      setDialogueRequested(false);
    };
    
    setWs(websocket);
    
    // ブロードキャストWebSocket接続
    const broadcastWsUrl = API_BASE.replace('http', 'ws') + '/ws/broadcast';
    const broadcastWebsocket = new WebSocket(broadcastWsUrl);
    
    broadcastWebsocket.onopen = () => {
      console.log('Broadcast WebSocket connected');
    };
    
    broadcastWebsocket.onmessage = (event) => {
      const data = JSON.parse(event.data);
      console.log('Broadcast message received:', data);
      
      if (data.type === 'dialogue_ready') {
        console.log('Dialogue ready:', data.id);
        setDialogueActive(true);
        setDialogueRequested(false);
      } else if (data.type === 'dialogue_ended') {
        console.log('Dialogue ended');
        setDialogueActive(false);
        setDialogueRequested(false);
      } else if (data.type === 'subtitle') {
        console.log('Subtitle received:', data.data.text);
        setSubtitles(data.data.text);
        
        // 既存のタイムアウトをクリア
        if (subtitleTimeoutRef.current) {
          clearTimeout(subtitleTimeoutRef.current);
        }
        
        // 10秒後に字幕をクリア
        subtitleTimeoutRef.current = setTimeout(() => {
          setSubtitles('');
        }, 10000);
      }
    };
    
    broadcastWebsocket.onerror = (error) => {
      console.error('Broadcast WebSocket error:', error);
    };
    
    broadcastWebsocket.onclose = (event) => {
      console.log('Broadcast WebSocket closed:', event.code, event.reason);
      // ブロードキャストWebSocket切断時も状態をリセット
      setDialogueActive(false);
      setDialogueRequested(false);
    };
    
    setBroadcastWs(broadcastWebsocket);
    
    return () => {
      websocket.close();
      broadcastWebsocket.close();
      // タイムアウトをクリア
      if (subtitleTimeoutRef.current) {
        clearTimeout(subtitleTimeoutRef.current);
      }
    };
  }, [API_BASE]);

  // ページ離脱時のクリーンアップ処理
  useEffect(() => {
    const handleBeforeUnload = () => {
      if (dialogueActive && ws && ws.readyState === WebSocket.OPEN) {
        // 対話モード中なら終了リクエストを送信
        const message = {
          type: 'dialogue_end',
          kind: 'dialogue'
        };
        ws.send(JSON.stringify(message));
        console.log('Dialogue end request sent on page unload');
      }
    };

    const handleVisibilityChange = () => {
      if (document.visibilityState === 'hidden' && dialogueActive && ws && ws.readyState === WebSocket.OPEN) {
        // ページが非表示になった時も対話を終了
        const message = {
          type: 'dialogue_end',
          kind: 'dialogue'
        };
        ws.send(JSON.stringify(message));
        console.log('Dialogue end request sent on visibility change');
      }
    };

    window.addEventListener('beforeunload', handleBeforeUnload);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    
    return () => {
      window.removeEventListener('beforeunload', handleBeforeUnload);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [dialogueActive, ws]);

  // 対話状態確認関数
  const checkDialogueStatus = async () => {
    try {
      const response = await fetch(`${API_BASE}/v1/dialogue/status`);
      const data = await response.json();
      setDialogueActive(data.active || false);
      setDialogueRequested(data.requested || false);
      console.log('Dialogue status checked:', data);
    } catch (error) {
      console.error('Failed to check dialogue status:', error);
      // エラー時は状態をリセット
      setDialogueActive(false);
      setDialogueRequested(false);
    }
  };

  async function connect() {
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
      
      // 参加者情報をログに出力
      console.log('Connected to room:', room.name);
      console.log('Participants:', room.numParticipants);
      room.remoteParticipants.forEach((participant) => {
        console.log('Participant:', participant.identity, 'tracks:', participant.audioTrackPublications.size);
      });
      
      room.on(RoomEvent.TrackSubscribed, (_track, publication, _participant) => {
        const track = (publication as RemoteTrackPublication).track as RemoteAudioTrack;
        console.log('Track subscribed:', track?.kind, publication.source);
        if (track) {
          track.attach(); // 自動再生
          console.log('Audio track attached');
        }
      });
      
      room.on(RoomEvent.TrackUnsubscribed, (track, publication, _participant) => {
        const audioTrack = (publication as RemoteTrackPublication).track as RemoteAudioTrack;
        if (audioTrack) {
          audioTrack.detach();
        }
      });
      
      setConnected(true);
    } catch (error) {
      console.error('Failed to join room:', error);
    }
  }

  function disconnect() {
    if (roomRef.current) {
      roomRef.current.disconnect();
      roomRef.current = null;
    }
    setConnected(false);
    setSubtitles('');
    setDialogueRequested(false);
    setDialogueActive(false);
  }

  async function requestDialogue() {
    if (!ws) return;
    
    try {
      const message = {
        type: 'dialogue_request',
        kind: 'dialogue'
      };
      ws.send(JSON.stringify(message));
      setDialogueRequested(true);
      console.log('Dialogue request sent');
    } catch (error) {
      console.error('Failed to send dialogue request:', error);
    }
  }

  async function endDialogue() {
    if (!ws) return;
    
    try {
      const message = {
        type: 'dialogue_end',
        kind: 'dialogue'
      };
      ws.send(JSON.stringify(message));
      setDialogueActive(false);
      console.log('Dialogue end request sent');
    } catch (error) {
      console.error('Failed to send dialogue end request:', error);
    }
  }


  return (
    <Box 
      minH="100vh" 
      bg={theme.color} 
      color="white" 
      p={6}
      transition="background-color 0.3s ease"
    >
      <VStack gap={6} align="stretch">
        <Box textAlign="center">
          <Text fontSize="4xl" fontWeight="bold" mb={2}>
            Radio‑24 — ON AIR
          </Text>
          <Text fontSize="xl" color="gray.300">
            {theme.title}
          </Text>
        </Box>

        <HStack gap={4} justify="center">
          {!connected ? (
            <Button 
              onClick={connect} 
              colorScheme="blue" 
              size="lg"
              bg="blue.500"
              _hover={{ bg: "blue.600" }}
            >
              接続
            </Button>
          ) : (
            <Button 
              onClick={disconnect} 
              colorScheme="red" 
              size="lg"
              bg="red.500"
              _hover={{ bg: "red.600" }}
            >
              切断
            </Button>
          )}

          {!dialogueRequested && !dialogueActive ? (
            <Button 
              onClick={requestDialogue}
              disabled={!connected}
              colorScheme="yellow"
              size="lg"
              bg="yellow.500"
              _hover={{ bg: "yellow.600" }}
              _disabled={{ bg: "gray.500" }}
            >
              💬 対話リクエスト
            </Button>
          ) : dialogueRequested ? (
            <Button 
              disabled
              colorScheme="orange"
              size="lg"
              bg="orange.500"
            >
              ⏳ 対話待機中...
            </Button>
          ) : (
            <Button 
              onClick={endDialogue}
              colorScheme="red"
              size="lg"
              bg="red.500"
              _hover={{ bg: "red.600" }}
            >
              🔚 対話終了
            </Button>
          )}

        </HStack>

        <Box 
          bg="blackAlpha.600" 
          p={6} 
          borderRadius="md"
          minH="200px"
          border="1px solid"
          borderColor="whiteAlpha.200"
        >
          <Text fontSize="lg" fontWeight="bold" mb={4} color="blue.200">
            📺 字幕:
          </Text>
          <Text 
            whiteSpace="pre-wrap" 
            fontSize="lg"
            lineHeight="1.8"
            color={subtitles ? "white" : "gray.400"}
            fontFamily="mono"
            p={subtitles ? 4 : 0}
            bg={subtitles ? "whiteAlpha.100" : "transparent"}
            borderRadius={subtitles ? "md" : "none"}
            transition="all 0.3s ease"
          >
            {subtitles || '字幕がここに表示されます...'}
          </Text>
        </Box>

        {dialogueActive && (
          <VStack gap={6} align="stretch">
            {/* 対話状態の通知 */}
            <Box 
              bg="yellow.900" 
              p={4} 
              borderRadius="md"
              border="2px solid"
              borderColor="yellow.500"
              animation="pulse 2s infinite"
            >
              <Text fontSize="lg" fontWeight="bold" color="yellow.200" mb={2}>
                🎙️ 対話モード中
              </Text>
              <Text fontSize="md" color="yellow.100" mb={2}>
                AI DJと対話できます。下のボタンを押して話してください。
              </Text>
              <Text fontSize="sm" color="yellow.300">
                💡 ボタンが{isRecording ? "赤色（録音中）" : "黄色（対話中）"}になっています。
                {isRecording ? "話し終わったらボタンを離してください。" : "押し続けて話しかけてください。"}
              </Text>
            </Box>

            {/* PTTボタン - 対話状態の時のみ表示 */}
            <Box textAlign="center">
              <Button
                onMouseDown={startPTT}
                onMouseUp={stopPTT}
                onTouchStart={startPTT}
                onTouchEnd={stopPTT}
                disabled={!connected}
                size="xl"
                height="120px"
                width="120px"
                borderRadius="full"
                fontSize="4xl"
                fontWeight="bold"
                colorScheme={isRecording ? "red" : "yellow"}
                bg={isRecording ? "red.500" : "yellow.500"}
                _hover={{ 
                  bg: isRecording ? "red.600" : "yellow.600",
                  transform: "scale(1.05)"
                }}
                _active={{ 
                  bg: isRecording ? "red.700" : "yellow.700",
                  transform: "scale(0.95)"
                }}
                _disabled={{ bg: "gray.500" }}
                boxShadow="0 8px 32px rgba(0,0,0,0.3)"
                transition="all 0.2s ease"
              >
                🎙️
              </Button>
              <Text fontSize="lg" fontWeight="bold" mt={4} color="yellow.200">
                {isRecording ? "🎤 録音中 - 話してください" : "🎤 話すボタン"}
              </Text>
              <Text fontSize="sm" color="yellow.300" mt={2}>
                {isRecording 
                  ? "話し終わったらボタンを離してください" 
                  : "ボタンを押し続けて話しかけてください"
                }
              </Text>
            </Box>
          </VStack>
        )}

        <audio ref={remoteAudioRef} autoPlay style={{ display: 'none' }} />
      </VStack>
    </Box>
  );

  async function startPTT() {
    if (!ws) return;
    
    if (dialogueActive) {
      // 対話モード中は音声録音を開始
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ 
          audio: {
            sampleRate: 24000,
            channelCount: 1,
            echoCancellation: true,
            noiseSuppression: true
          } 
        });
        
        const mediaRecorder = new MediaRecorder(stream, {
          mimeType: 'audio/webm;codecs=opus',
          audioBitsPerSecond: 64000 // より高品質な音声録音（32kbps → 64kbps）
        });
        
        mediaRecorderRef.current = mediaRecorder;
        audioChunksRef.current = [];
        
        mediaRecorder.ondataavailable = (event) => {
          if (event.data.size > 0) {
            audioChunksRef.current.push(event.data);
          }
        };
        
        // 録音開始（タイムスライスを削除して連続録音を有効化）
        mediaRecorder.start(); // タイムスライスなしで連続録音
        setIsRecording(true);
        console.log('PTT started for dialogue - recording audio for AI DJ');
      } catch (error) {
        console.error('Failed to start audio recording:', error);
        alert('マイクへのアクセスが拒否されました。ブラウザの設定を確認してください。');
      }
    } else {
      // 通常のPTT
      console.log('PTT started - normal mode');
    }
  }

  async function stopPTT() {
    if (!ws) return;
    
    if (dialogueActive && mediaRecorderRef.current && isRecording) {
      // 対話モード中は音声録音を停止して送信
      return new Promise<void>((resolve) => {
        // 既存のonstopハンドラーをクリアしてから新しいものを設定
        if (mediaRecorderRef.current) {
          mediaRecorderRef.current.onstop = null;
          
          mediaRecorderRef.current.onstop = async () => {
            try {
              const audioBlob = new Blob(audioChunksRef.current, { type: 'audio/webm' });
              
              if (audioBlob.size > 0) {
                // WebM音声をPCM16に変換（効率的な処理）
                const AudioContextClass = window.AudioContext || (window as typeof window & { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
                const audioContext = new AudioContextClass();
                const arrayBuffer = await audioBlob.arrayBuffer();
                const audioBuffer = await audioContext.decodeAudioData(arrayBuffer);
                
                // モノラル、24kHz、PCM16に変換
                const sampleRate = 24000;
                const length = Math.floor(audioBuffer.length * sampleRate / audioBuffer.sampleRate);
                const pcm16Data = new Int16Array(length);
                
                // 効率的なリサンプリングとPCM16変換
                const sourceData = audioBuffer.getChannelData(0); // モノラル
                const ratio = audioBuffer.sampleRate / sampleRate;
                for (let i = 0; i < length; i++) {
                  const sourceIndex = Math.floor(i * ratio);
                  const sample = sourceData[sourceIndex] || 0;
                  pcm16Data[i] = Math.round(sample * 32767); // 簡素化された変換
                }
                
                // 音声の長さをチェック（25ms = 600サンプル）
                const durationMs = (pcm16Data.length / sampleRate) * 1000;
                console.log(`Audio duration: ${durationMs.toFixed(2)} ms (${pcm16Data.length} samples)`);
                
                if (durationMs < 25) {
                  console.log('Audio too short (< 25ms), skipping send to avoid buffer errors');
                  setIsRecording(false);
                  resolve();
                  return;
                }
                
                // Base64エンコード（スタックオーバーフローを防ぐため安全な方法を使用）
                const uint8Array = new Uint8Array(pcm16Data.buffer);
                const base64Audio = btoa(Array.from(uint8Array, byte => String.fromCharCode(byte)).join(''));
                
                const message = {
                  type: 'input_audio_buffer.append',
                  audio: base64Audio
                };
                ws.send(JSON.stringify(message));
                
                // 音声をコミット
                const commitMessage = {
                  type: 'input_audio_buffer.commit'
                };
                ws.send(JSON.stringify(commitMessage));
                
                console.log(`PTT stopped for dialogue - audio sent to AI DJ (${pcm16Data.length} samples, ${durationMs.toFixed(2)}ms, ${(audioBlob.size / 1024).toFixed(2)}KB, efficient mode)`);
              } else {
                console.log('No audio data recorded');
              }
              
              // ストリームを停止
              if (mediaRecorderRef.current && mediaRecorderRef.current.stream) {
                mediaRecorderRef.current.stream.getTracks().forEach(track => track.stop());
              }
              
              setIsRecording(false);
              resolve();
            } catch (error) {
              console.error('Failed to process audio:', error);
              setIsRecording(false);
              resolve();
            }
          };
          
          mediaRecorderRef.current.stop();
        } else {
          setIsRecording(false);
          resolve();
        }
      });
    } else {
      // 通常のPTT
      console.log('PTT stopped - normal mode');
    }
  }
}
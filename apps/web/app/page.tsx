'use client';

import { useRef, useState, useEffect } from 'react';
import { Box, Button, VStack, Text, HStack } from '@chakra-ui/react';
import { Room, RoomEvent, RemoteTrackPublication, RemoteAudioTrack } from 'livekit-client';

export default function OnAir() {
  const [connected, setConnected] = useState(false);
  const [subtitles, setSubtitles] = useState('');
  const [displayedSubtitles, setDisplayedSubtitles] = useState('');
  const [subtitleStack, setSubtitleStack] = useState<Array<{text: string, id: string, timestamp: Date}>>([]);
  const [theme, setTheme] = useState({ title: 'Radio-24', color: '#1a1a2e' });
  const [ws, setWs] = useState<WebSocket | null>(null);
  const [broadcastWs, setBroadcastWs] = useState<WebSocket | null>(null);
  const [dialogueRequested, setDialogueRequested] = useState(false);
  const [dialogueActive, setDialogueActive] = useState(false);
  const [isRecording, setIsRecording] = useState(false);
  const [myClientId, setMyClientId] = useState<string>('');
  const [dialogueRequester, setDialogueRequester] = useState<string>('');
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  const roomRef = useRef<Room | null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement>(null);
  const subtitleTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const typewriterTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const currentSubtitleIdRef = useRef<string>('');

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
        // クライアントIDを保存
        if (data.client_id) {
          setMyClientId(data.client_id);
        }
      } else if (data.type === 'dialogue_end_ack') {
        console.log('Dialogue end acknowledged');
        setDialogueActive(false);
        setDialogueRequested(false);
      } else if (data.type === 'dialogue_ended') {
        console.log('Dialogue ended by server');
        setDialogueActive(false);
        setDialogueRequested(false);
        setDialogueRequester('');
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
      setDialogueRequester('');
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
        console.log('Dialogue ready:', data);
        // ブロードキャストメッセージの構造に合わせて修正
        const messageData = data.data || data;
        console.log('Request ID:', messageData.id);
        console.log('Client ID:', messageData.client_id);
        setDialogueActive(true);
        setDialogueRequested(false);
        // 対話をリクエストしたクライアントを記録
        if (messageData.client_id) {
          setDialogueRequester(messageData.client_id);
          console.log('Set dialogue requester to:', messageData.client_id);
        } else {
          console.log('No client_id in dialogue_ready message');
        }
      } else if (data.type === 'dialogue_ended') {
        console.log('Dialogue ended via broadcast');
        setDialogueActive(false);
        setDialogueRequested(false);
        setDialogueRequester('');
      } else if (data.type === 'subtitle') {
        console.log('Subtitle received:', data.data.text);
        
        // 既存のタイムアウトをクリア
        if (subtitleTimeoutRef.current) {
          clearTimeout(subtitleTimeoutRef.current);
        }
        if (typewriterTimeoutRef.current) {
          clearTimeout(typewriterTimeoutRef.current);
        }
        
        // 現在表示中の字幕がある場合は、それをスタックに追加
        if (currentSubtitleIdRef.current && subtitles) {
          setSubtitleStack(prev => {
            const newStack = [...prev, {
              text: subtitles,
              id: currentSubtitleIdRef.current,
              timestamp: new Date()
            }];
            // 最大3つに制限（古いものから削除）
            return newStack.slice(-3);
          });
        }
        
        // 新しい字幕を設定
        setSubtitles(data.data.text);
        
        // 新しい字幕IDを生成
        const subtitleId = `subtitle-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
        currentSubtitleIdRef.current = subtitleId;
        
        // タイプライター効果で字幕を表示
        startTypewriterEffect(data.data.text, subtitleId);
        
        // 50秒後に現在の字幕をクリア（フォールバック用）
        subtitleTimeoutRef.current = setTimeout(() => {
          setSubtitles('');
          setDisplayedSubtitles('');
          currentSubtitleIdRef.current = '';
        }, 50000);
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
      setDialogueRequester('');
    };
    
    setBroadcastWs(broadcastWebsocket);
    
    return () => {
      websocket.close();
      broadcastWebsocket.close();
      // タイムアウトをクリア
      if (subtitleTimeoutRef.current) {
        clearTimeout(subtitleTimeoutRef.current);
      }
      if (typewriterTimeoutRef.current) {
        clearTimeout(typewriterTimeoutRef.current);
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
        
        // 対話状態を即座に無効化
        setDialogueActive(false);
      }
    };

    const handleVisibilityChange = () => {
      // visibilitychangeでは対話を終了しない
      // タブ切り替えや他のアプリへのフォーカス移動で対話が終了するのを防ぐ
      if (document.visibilityState === 'hidden' && dialogueActive) {
        console.log('Page became hidden during dialogue - keeping dialogue active');
      }
    };

    window.addEventListener('beforeunload', handleBeforeUnload);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    
    return () => {
      window.removeEventListener('beforeunload', handleBeforeUnload);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [dialogueActive, ws]);

  // タイプライター効果の実装
  const startTypewriterEffect = (text: string, subtitleId: string) => {
    setDisplayedSubtitles('');
    let index = 0;
    
    const typeNextChar = () => {
      // 字幕IDが変わった場合は停止
      if (currentSubtitleIdRef.current !== subtitleId) {
        return;
      }
      
      if (index < text.length) {
        setDisplayedSubtitles(text.slice(0, index + 1));
        index++;
        // 日本語の場合は少し遅め、英数字・記号は速めに設定
        const char = text[index - 1];
        const delay = /[ひらがなカタカナ漢字]/.test(char) ? 80 : 40;
        typewriterTimeoutRef.current = setTimeout(typeNextChar, delay);
      }
    };
    
    typeNextChar();
  };

  // 対話状態確認関数
  const checkDialogueStatus = async () => {
    try {
      const response = await fetch(`${API_BASE}/v1/dialogue/status`);
      const data = await response.json();
      setDialogueActive(data.active || false);
      setDialogueRequested(data.requested || false);
      setDialogueRequester(data.requested_by || '');
      console.log('Dialogue status checked:', data);
    } catch (error) {
      console.error('Failed to check dialogue status:', error);
      // エラー時は状態をリセット
      setDialogueActive(false);
      setDialogueRequested(false);
      setDialogueRequester('');
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
    setDisplayedSubtitles('');
    setSubtitleStack([]);
    setDialogueRequested(false);
    setDialogueActive(false);
    setDialogueRequester('');
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
    if (!ws || !dialogueActive) return;
    
    try {
      const message = {
        type: 'dialogue_end',
        kind: 'dialogue'
      };
      ws.send(JSON.stringify(message));
      // 状態はサーバーからの応答で更新される
      console.log('Dialogue end request sent');
    } catch (error) {
      console.error('Failed to send dialogue end request:', error);
      // エラー時は手動で状態をリセット
      setDialogueActive(false);
      setDialogueRequested(false);
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
          minH="400px"
          maxH="600px"
          border="1px solid"
          borderColor="whiteAlpha.200"
          overflowY="auto"
        >
          <Text fontSize="lg" fontWeight="bold" mb={4} color="blue.200">
            📺 字幕履歴:
          </Text>
          
          {/* スタックされた字幕 */}
          <VStack gap={3} align="stretch" mb={4}>
            {subtitleStack.map((subtitle) => (
              <Box
                key={subtitle.id}
                bg="whiteAlpha.100"
                p={3}
                borderRadius="md"
                border="1px solid"
                borderColor="whiteAlpha.200"
                opacity={0.8}
              >
                <Text 
                  fontSize="sm" 
                  color="gray.400" 
                  mb={1}
                >
                  {subtitle.timestamp.toLocaleTimeString()}
                </Text>
                <Text 
                  whiteSpace="pre-wrap"
                  fontSize="md"
                  lineHeight="1.6"
                  color="gray.200"
                  fontFamily="mono"
                >
                  {subtitle.text}
                </Text>
              </Box>
            ))}
          </VStack>
          
          {/* 現在の字幕 */}
          {displayedSubtitles && (
            <Box
              bg="yellow.900"
              p={4}
              borderRadius="md"
              border="2px solid"
              borderColor="yellow.500"
              position="relative"
            >
              <Text 
                whiteSpace="pre-wrap" 
                fontSize="lg"
                lineHeight="1.8"
                color="yellow.100"
                fontFamily="mono"
              >
                {displayedSubtitles}
                {displayedSubtitles.length < subtitles.length && (
                  <Text as="span" color="yellow.300" animation="blink 1s infinite">
                    |
                  </Text>
                )}
              </Text>
            </Box>
          )}
          
          {/* 字幕がない場合のメッセージ */}
          {!displayedSubtitles && subtitleStack.length === 0 && (
            <Text 
              color="gray.400"
              fontStyle="italic"
              textAlign="center"
              py={8}
            >
              字幕がここに表示されます...
            </Text>
          )}
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
                {dialogueRequester === myClientId 
                  ? "AI DJと対話できます。下のボタンを押して話してください。"
                  : "他のリスナーが対話中です。しばらくお待ちください。"
                }
              </Text>
              <Text fontSize="xs" color="gray.400" mb={2}>
                Debug: dialogueRequester={dialogueRequester}, myClientId={myClientId}
              </Text>
              <Text fontSize="sm" color="yellow.300">
                {dialogueRequester === myClientId ? (
                  <>
                    💡 ボタンが{isRecording ? "赤色（録音中）" : "黄色（対話中）"}になっています。
                    {isRecording ? "話し終わったらボタンを離してください。" : "押し続けて話しかけてください。"}
                  </>
                ) : (
                  "💡 対話をリクエストしたリスナーにのみ話す権限があります。"
                )}
              </Text>
            </Box>

            {/* PTTボタン - 対話状態でかつ自分のリクエストの場合のみ表示 */}
            {dialogueRequester === myClientId && (
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
            )}
          </VStack>
        )}

        <audio ref={remoteAudioRef} autoPlay style={{ display: 'none' }} />
      </VStack>
    </Box>
  );

  async function startPTT() {
    if (!ws) return;
    
    // 対話モード中でかつ自分のリクエストの場合のみ音声録音を許可
    if (dialogueActive && dialogueRequester === myClientId) {
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
    
    // 対話モード中でかつ自分のリクエストの場合のみ音声録音を停止
    if (dialogueActive && dialogueRequester === myClientId && mediaRecorderRef.current && isRecording) {
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
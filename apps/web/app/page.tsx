'use client';

import { useRef, useState, useEffect } from 'react';
import Link from 'next/link';
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
  const roomRef = useRef<Room | null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement>(null);

  const API_BASE = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080';

  useEffect(() => {
    // PTT WebSocket接続
    const wsUrl = API_BASE.replace('http', 'ws') + '/ws/ptt';
    const websocket = new WebSocket(wsUrl);
    
    websocket.onopen = () => {
      console.log('PTT WebSocket connected');
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
      }
    };
    
    broadcastWebsocket.onerror = (error) => {
      console.error('Broadcast WebSocket error:', error);
    };
    
    broadcastWebsocket.onclose = (event) => {
      console.log('Broadcast WebSocket closed:', event.code, event.reason);
    };
    
    setBroadcastWs(broadcastWebsocket);
    
    return () => {
      websocket.close();
      broadcastWebsocket.close();
    };
  }, [API_BASE]);

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

  async function rotateTheme() {
    try {
      const response = await fetch(`${API_BASE}/v1/theme/rotate`, { method: 'POST' });
      const newTheme = await response.json();
      setTheme(newTheme);
    } catch (error) {
      console.error('Theme rotation error:', error);
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
          
          <Button 
            onMouseDown={startPTT} 
            onMouseUp={stopPTT} 
            disabled={!connected}
            colorScheme={dialogueActive ? "yellow" : "green"}
            size="lg"
            bg={dialogueActive ? "yellow.500" : "green.500"}
            _hover={{ bg: dialogueActive ? "yellow.600" : "green.600" }}
            _disabled={{ bg: "gray.500" }}
          >
            🎙️ PTT {dialogueActive && "(対話中)"}
          </Button>

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

          <Button 
            onClick={rotateTheme}
            colorScheme="purple"
            size="lg"
            bg="purple.500"
            _hover={{ bg: "purple.600" }}
          >
            テーマ切替
          </Button>

          <Button 
            asChild
            colorScheme="orange"
            size="lg"
            bg="orange.500"
            _hover={{ bg: "orange.600" }}
          >
            <Link href="/submit">投稿する</Link>
          </Button>
        </HStack>

        <Box 
          bg="blackAlpha.500" 
          p={6} 
          borderRadius="md"
          minH="200px"
        >
          <Text fontSize="lg" fontWeight="bold" mb={4}>
            字幕:
          </Text>
          <Text 
            whiteSpace="pre-wrap" 
            fontSize="md"
            lineHeight="1.6"
          >
            {subtitles || '字幕がここに表示されます...'}
          </Text>
        </Box>

        {dialogueActive && (
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
              AI DJと対話できます。PTTボタンを押して話してください。
            </Text>
            <Text fontSize="sm" color="yellow.300">
              💡 PTTボタンが黄色になっています。押し続けて話しかけてください。
            </Text>
          </Box>
        )}

        <audio ref={remoteAudioRef} autoPlay style={{ display: 'none' }} />
      </VStack>
    </Box>
  );

  function startPTT() {
    if (!ws) return;
    
    if (dialogueActive) {
      // 対話モード中は音声入力を開始
      const message = {
        type: 'input_audio_buffer.append',
        audio: '' // 実際の実装では音声データを送信
      };
      ws.send(JSON.stringify(message));
      console.log('PTT started for dialogue - speaking to AI DJ');
    } else {
      // 通常のPTT
      console.log('PTT started - normal mode');
    }
  }

  function stopPTT() {
    if (!ws) return;
    
    if (dialogueActive) {
      // 対話モード中は音声入力を終了
      const message = {
        type: 'input_audio_buffer.commit'
      };
      ws.send(JSON.stringify(message));
      console.log('PTT stopped for dialogue - finished speaking to AI DJ');
    } else {
      // 通常のPTT
      console.log('PTT stopped - normal mode');
    }
  }
}
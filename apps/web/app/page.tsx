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
  const roomRef = useRef<Room | null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement>(null);

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
            colorScheme="green"
            size="lg"
            bg="green.500"
            _hover={{ bg: "green.600" }}
            _disabled={{ bg: "gray.500" }}
          >
            🎙️ PTT
          </Button>

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

        <audio ref={remoteAudioRef} autoPlay style={{ display: 'none' }} />
      </VStack>
    </Box>
  );

  function startPTT() {
    // 任意：input_audio_buffer.append を使う場合の実装。MVPはserver_vadに任せてOK
    console.log('PTT started');
  }

  function stopPTT() {
    // 任意
    console.log('PTT stopped');
  }
}
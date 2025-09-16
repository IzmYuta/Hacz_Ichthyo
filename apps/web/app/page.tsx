'use client';

import { useRef, useState } from 'react';
import Link from 'next/link';
import { Box, Button, VStack, Text, HStack } from '@chakra-ui/react';

export default function OnAir() {
  const [connected, setConnected] = useState(false);
  const [subtitles, setSubtitles] = useState('');
  const [theme, setTheme] = useState({ title: 'Radio-24', color: '#1a1a2e' });
  const pcRef = useRef<RTCPeerConnection|null>(null);
  const dataRef = useRef<RTCDataChannel|null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement>(null);

  async function connect() {
    try {
      // 1) サーバから短命クライアントシークレットを取得
      const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080';
      const eph = await fetch(`${apiBase}/v1/realtime/ephemeral`, { method:'POST' }).then(r=>r.json());
      const token = eph.client_secret || eph.value; // shape差異に対応

      if (!token) {
        console.error('Failed to get ephemeral token');
        return;
      }

      // 2) WebRTCピア接続
      const pc = new RTCPeerConnection();
      pcRef.current = pc;

      // 受信音声を再生
      pc.ontrack = (e) => {
        if (remoteAudioRef.current) {
          remoteAudioRef.current.srcObject = e.streams[0];
          remoteAudioRef.current.play().catch(()=>{});
        }
      };

      // イベント受信用データチャネル
      const dc = pc.createDataChannel('oai-events');
      dataRef.current = dc;
      dc.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          // 例: 字幕用（response.output_text.delta）
          if (msg.type === 'response.output_text.delta') {
            setSubtitles((s) => s + msg.delta);
          }
        } catch {}
      };

      // マイクを送信
      const ms = await navigator.mediaDevices.getUserMedia({ audio: true });
      for (const track of ms.getTracks()) pc.addTrack(track, ms);

      // SDPオファ生成
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);

      // 3) RealtimeへSDPをPOST（Bearer: ephemeral）
      const model = process.env.NEXT_PUBLIC_OPENAI_REALTIME_MODEL || 'gpt-realtime';
      const sdpResp = await fetch(`https://api.openai.com/v1/realtime?model=${model}`, {
        method: 'POST',
        body: offer.sdp!,
        headers: {
          'Authorization': `Bearer ${token}`,
          'Content-Type': 'application/sdp'
        }
      });

      const answer = { type: 'answer', sdp: await sdpResp.text() } as RTCSessionDescriptionInit;
      await pc.setRemoteDescription(answer);

      // 4) セッション初期化（声・VAD・トーンなど）
      const init = {
        type: 'session.update',
        session: {
          type: 'realtime',
          instructions: 'あなたは深夜ラジオのDJ。短く・テンポ良く・ポジティブに。固有名詞ははっきり復唱。',
          voice: 'marin',
          audio: { input: { turn_detection: { type: 'server_vad', idle_timeout_ms: 6000 } } }
        }
      };
      dc.send(JSON.stringify(init));

      // 最初の挨拶
      dc.send(JSON.stringify({
        type: 'response.create',
        response: { modalities: ['audio','text'], instructions: 'ハックツラジオ、Radio-24へようこそ。30秒だけ投稿どうぞ！' }
      }));

      setConnected(true);
    } catch (error) {
      console.error('Connection error:', error);
    }
  }

  function disconnect() {
    dataRef.current?.close();
    pcRef.current?.close();
    setConnected(false);
    setSubtitles('');
  }

  async function rotateTheme() {
    try {
      const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080';
      const response = await fetch(`${apiBase}/v1/theme/rotate`, { method: 'POST' });
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
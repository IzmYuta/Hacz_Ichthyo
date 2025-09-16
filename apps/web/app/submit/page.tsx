'use client';

import { useState } from 'react';
import Link from 'next/link';
import { Box, Button, VStack, Text, HStack, Textarea, SimpleGrid } from '@chakra-ui/react';

interface Recommendation {
  id: string;
  text: string;
  created_at: string;
  similarity: number;
}

export default function SubmitPage() {
  const [text, setText] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [recommendations, setRecommendations] = useState<Recommendation[]>([]);
  const [message, setMessage] = useState('');
  const [messageType, setMessageType] = useState<'success' | 'error' | ''>('');

  async function handleSubmit() {
    if (!text.trim()) {
      setMessage('テキストを入力してください');
      setMessageType('error');
      return;
    }

    setIsSubmitting(true);
    setMessage('');
    setRecommendations([]);

    try {
      const apiBase = process.env.NEXT_PUBLIC_API_BASE || 'http://localhost:8080';
      const response = await fetch(`${apiBase}/v1/submission`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          text: text,
          type: 'text'
        })
      });

      if (!response.ok) {
        throw new Error('投稿に失敗しました');
      }

      const result = await response.json();
      setMessage('投稿が保存されました！');
      setMessageType('success');
      setRecommendations(result.recommendations || []);
      setText('');

    } catch (error) {
      console.error('Submission error:', error);
      setMessage('投稿に失敗しました。しばらくしてから再試行してください。');
      setMessageType('error');
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <Box 
      minH="100vh" 
      bg="gray.900" 
      color="white" 
      p={6}
    >
      <VStack gap={6} align="stretch" maxW="4xl" mx="auto">
        <Box textAlign="center">
          <Text fontSize="4xl" fontWeight="bold" mb={2}>
            Radio-24 投稿
          </Text>
          <Text fontSize="lg" color="gray.300">
            あなたの投稿をAIラジオに送信しましょう
          </Text>
        </Box>

        <VStack gap={4} align="stretch">
          <Text fontSize="lg" fontWeight="bold">
            投稿内容:
          </Text>
          <Textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="ここにあなたの投稿を入力してください..."
            size="lg"
            rows={6}
            bg="gray.800"
            borderColor="gray.600"
            color="white"
            _placeholder={{ color: "gray.400" }}
            _focus={{ borderColor: "blue.400" }}
          />
          
          <HStack gap={4}>
            <Button
              onClick={handleSubmit}
              loading={isSubmitting}
              loadingText="送信中..."
              colorScheme="blue"
              size="lg"
              bg="blue.500"
              _hover={{ bg: "blue.600" }}
            >
              投稿する
            </Button>
            
            <Button
              onClick={() => {
                setText('');
                setMessage('');
                setRecommendations([]);
              }}
              variant="outline"
              size="lg"
              borderColor="gray.600"
              color="white"
              _hover={{ bg: "gray.700" }}
            >
              クリア
            </Button>
          </HStack>
        </VStack>

        {message && (
          <Box 
            p={4} 
            borderRadius="md" 
            bg={messageType === 'success' ? 'green.500' : 'red.500'}
            color="white"
          >
            {message}
          </Box>
        )}

        {recommendations.length > 0 && (
          <Box>
            <Text fontSize="lg" fontWeight="bold" mb={4}>
              類似した投稿:
            </Text>
            <SimpleGrid columns={{ base: 1, md: 2, lg: 3 }} gap={4}>
              {recommendations.map((rec) => (
                <Box
                  key={rec.id}
                  bg="gray.800"
                  p={4}
                  borderRadius="md"
                  border="1px solid"
                  borderColor="gray.600"
                >
                  <Text fontSize="sm" color="gray.400" mb={2}>
                    類似度: {(rec.similarity * 100).toFixed(1)}%
                  </Text>
                  <Text fontSize="sm" mb={2}>
                    {rec.text}
                  </Text>
                  <Text fontSize="xs" color="gray.500">
                    {new Date(rec.created_at).toLocaleString('ja-JP')}
                  </Text>
                </Box>
              ))}
            </SimpleGrid>
          </Box>
        )}

        <Box mt={8} textAlign="center">
          <Button
            asChild
            colorScheme="purple"
            size="lg"
            bg="purple.500"
            _hover={{ bg: "purple.600" }}
          >
            <Link href="/">ON AIR に戻る</Link>
          </Button>
        </Box>
      </VStack>
    </Box>
  );
}

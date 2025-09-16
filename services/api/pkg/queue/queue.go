package queue

import (
	"sync"
	"time"
)

type PTTKind string

const (
	PTTKindText  PTTKind = "text"
	PTTKindAudio PTTKind = "audio"
	PTTKindPhone PTTKind = "phone"
)

type PTTStatus string

const (
	PTTStatusQueued     PTTStatus = "queued"
	PTTStatusProcessing PTTStatus = "processing"
	PTTStatusCompleted  PTTStatus = "completed"
	PTTStatusFailed     PTTStatus = "failed"
)

type PTTItem struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Kind       PTTKind   `json:"kind"`
	Text       string    `json:"text"`
	Priority   int       `json:"priority"`
	Status     PTTStatus `json:"status"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

type Queue struct {
	mu    sync.RWMutex
	items []PTTItem
}

func NewQueue() *Queue {
	return &Queue{
		items: make([]PTTItem, 0),
	}
}

func (q *Queue) Enqueue(item PTTItem) {
	q.mu.Lock()
	defer q.mu.Unlock()

	item.Status = PTTStatusQueued
	item.EnqueuedAt = time.Now()

	// 優先度に基づいて挿入位置を決定
	insertIndex := len(q.items)
	for i, existingItem := range q.items {
		if item.Priority > existingItem.Priority {
			insertIndex = i
			break
		}
	}

	// スライスに挿入
	if insertIndex == len(q.items) {
		q.items = append(q.items, item)
	} else {
		q.items = append(q.items[:insertIndex+1], q.items[insertIndex:]...)
		q.items[insertIndex] = item
	}
}

func (q *Queue) Dequeue() *PTTItem {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return nil
	}

	item := q.items[0]
	q.items = q.items[1:]

	item.Status = PTTStatusProcessing
	return &item
}

func (q *Queue) Peek() *PTTItem {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if len(q.items) == 0 {
		return nil
	}

	item := q.items[0]
	return &item
}

func (q *Queue) Size() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.items)
}

func (q *Queue) GetTopN(n int) []PTTItem {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if n > len(q.items) {
		n = len(q.items)
	}

	result := make([]PTTItem, n)
	copy(result, q.items[:n])
	return result
}

func (q *Queue) UpdateStatus(id string, status PTTStatus) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.ID == id {
			q.items[i].Status = status
			return true
		}
	}
	return false
}

func (q *Queue) Remove(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return true
		}
	}
	return false
}

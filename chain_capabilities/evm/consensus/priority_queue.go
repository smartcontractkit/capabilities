package consensus

import (
	"container/heap"
)

type priorityQueue struct {
	queue *internalPriorityQueue
}

func newPriorityQueue() *priorityQueue {
	return &priorityQueue{queue: &internalPriorityQueue{idToIndex: make(map[string]int)}}
}

func (q *priorityQueue) Push(x *requestCtx) {
	heap.Push(q.queue, x)
}

func (q *priorityQueue) Pop() *requestCtx {
	return heap.Pop(q.queue).(*requestCtx)
}

func (q *priorityQueue) Len() int {
	return q.queue.Len()
}

func (q *priorityQueue) Peek() *requestCtx {
	return q.queue.values[0]
}

func (q *priorityQueue) IncreaseAttempt(id string) {
	index, ok := q.queue.idToIndex[id]
	if !ok {
		return
	}

	q.queue.values[index].Attempt++
	heap.Fix(q.queue, index)
}

func (q *priorityQueue) GetByID(id string) (*requestCtx, bool) {
	index, ok := q.queue.idToIndex[id]
	if !ok {
		return nil, false
	}

	return q.queue.values[index], true
}

func (q *priorityQueue) Remove(id string) (*requestCtx, bool) {
	index, ok := q.queue.idToIndex[id]
	if !ok {
		return nil, false
	}

	request := q.queue.values[index]
	heap.Remove(q.queue, index)
	delete(q.queue.idToIndex, id)
	return request, true
}

type internalPriorityQueue struct {
	values    []*requestCtx
	idToIndex map[string]int
}

func (q *internalPriorityQueue) Len() int {
	return len(q.values)
}

func (q *internalPriorityQueue) Less(i, j int) bool {
	return q.values[i].Attempt < q.values[j].Attempt
}

func (q *internalPriorityQueue) Swap(i, j int) {
	q.values[i], q.values[j] = q.values[j], q.values[i]
	q.idToIndex[q.values[i].ID()] = i
	q.idToIndex[q.values[j].ID()] = j
}

func (q *internalPriorityQueue) Push(x any) {
	request := x.(*requestCtx)
	q.values = append(q.values, request)
	q.idToIndex[request.ID()] = len(q.values) - 1
}

func (q *internalPriorityQueue) Pop() any {
	n := len(q.values)
	if n == 0 {
		return nil
	}

	result := q.values[n-1]
	q.values = q.values[:n-1]
	delete(q.idToIndex, result.ID())
	return result
}

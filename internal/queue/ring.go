package queue

import (
	"runtime"
	"sync/atomic"

	"github.com/rueian/rueidis/internal/proto"
)

const RingSize = 8192

func NewRing() *Ring {
	r := &Ring{}
	r.mask = uint64(len(r.store) - 1)
	for i := range r.store {
		r.store[i].ch = make(chan proto.Result, 1)
		r.store[i].cmds = make([][]string, 1)
	}
	return r
}

type Ring struct {
	_     [8]uint64
	write uint64
	_     [7]uint64
	read1 uint64
	_     [7]uint64
	read2 uint64
	_     [7]uint64
	mask  uint64
	_     [7]uint64
	store [RingSize]node // store's size must be 2^N to work with the mask
}

type node struct {
	r    uint64
	cmds [][]string
	ch   chan proto.Result
}

func (r *Ring) PutOne(m []string) chan proto.Result {
	n := r.acquire(atomic.AddUint64(&r.write, 1) & r.mask)
	n.cmds[0] = m
	n.cmds = n.cmds[:1]
	atomic.StoreUint64(&n.r, 2)
	return n.ch
}

func (r *Ring) PutMulti(m [][]string) chan proto.Result {
	n := r.acquire(atomic.AddUint64(&r.write, 1) & r.mask)
	n.cmds = m
	atomic.StoreUint64(&n.r, 2)
	return n.ch
}

func (r *Ring) acquire(position uint64) *node {
	n := &r.store[position]
	for !atomic.CompareAndSwapUint64(&n.r, 0, 1) {
		runtime.Gosched()
	}
	return n
}

// TryNextCmd should be only called by one dedicated thread
func (r *Ring) TryNextCmd() [][]string {
	r.read1++
	p := r.read1 & r.mask
	n := &r.store[p]
	if !atomic.CompareAndSwapUint64(&n.r, 2, 3) {
		r.read1--
		return nil
	}
	return n.cmds
}

// NextCmd should be only called by one dedicated thread
func (r *Ring) NextCmd() [][]string {
	r.read1 = (r.read1 + 1) & r.mask
	n := &r.store[r.read1]
	for !atomic.CompareAndSwapUint64(&n.r, 2, 3) {
		runtime.Gosched()
	}
	return n.cmds
}

// NextResultCh should be only called by one dedicated thread
func (r *Ring) NextResultCh() (cmds [][]string, ch chan proto.Result) {
	r.read2++
	p := r.read2 & r.mask
	n := &r.store[p]
	cmds, ch = n.cmds, n.ch
	if atomic.CompareAndSwapUint64(&n.r, 3, 0) {
		return
	}
	panic("unexpected NextResultCh call on ring")
}
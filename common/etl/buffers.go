package etl

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"github.com/c2h5oh/datasize"
	"github.com/ledgerwatch/erigon/common/dbutils"
)

const (
	//SliceBuffer - just simple slice w
	SortableSliceBuffer = iota
	//SortableAppendBuffer - map[k] [v1 v2 v3]
	SortableAppendBuffer
	// SortableOldestAppearedBuffer - buffer that keeps only the oldest entries.
	// if first v1 was added under key K, then v2; only v1 will stay
	SortableOldestAppearedBuffer
	SortableNewestAppearedBuffer

	BufIOSize = 64 * 4096 // 64 pages | default is 1 page | increasing further doesn't show speedup on SSD
)

var BufferOptimalSize = 256 * datasize.MB /*  var because we want to sometimes change it from tests or command-line flags */

type Buffer interface {
	Put(k, v []byte)
	Get(i int) sortableBufferEntry
	Len() int
	Reset()
	GetEntries() []sortableBufferEntry
	Sort()
	CheckFlushSize() bool
	SetComparator(cmp dbutils.CmpFunc)
}

type sortableBufferEntry struct {
	key   []byte
	value []byte
}

var (
	_ Buffer = &sortableBuffer{}
	_ Buffer = &appendSortableBuffer{}
	_ Buffer = &oldestEntrySortableBuffer{}
	_ Buffer = &newestEntrySortableBuffer{}
)

func NewSortableBuffer(bufferOptimalSize datasize.ByteSize) *sortableBuffer {
	return &sortableBuffer{
		entries:     make([]sortableBufferEntry, 0),
		size:        0,
		optimalSize: int(bufferOptimalSize.Bytes()),
	}
}

type sortableBuffer struct {
	entries     []sortableBufferEntry
	size        int
	optimalSize int
	comparator  dbutils.CmpFunc
}

func (b *sortableBuffer) Put(k, v []byte) {
	b.size += len(k)
	b.size += len(v)
	b.entries = append(b.entries, sortableBufferEntry{k, v})
}

func (b *sortableBuffer) Size() int {
	return b.size
}

func (b *sortableBuffer) Len() int {
	return len(b.entries)
}

func (b *sortableBuffer) SetComparator(cmp dbutils.CmpFunc) {
	b.comparator = cmp
}

func (b *sortableBuffer) Less(i, j int) bool {
	if b.comparator != nil {
		return b.comparator(b.entries[i].key, b.entries[j].key, b.entries[i].value, b.entries[j].value) < 0
	}
	return bytes.Compare(b.entries[i].key, b.entries[j].key) < 0
}

func (b *sortableBuffer) Swap(i, j int) {
	b.entries[i], b.entries[j] = b.entries[j], b.entries[i]
}

func (b *sortableBuffer) Get(i int) sortableBufferEntry {
	return b.entries[i]
}

func (b *sortableBuffer) Reset() {
	b.entries = nil
	b.size = 0
}
func (b *sortableBuffer) Sort() {
	sort.Stable(b)
}

func (b *sortableBuffer) GetEntries() []sortableBufferEntry {
	return b.entries
}

func (b *sortableBuffer) CheckFlushSize() bool {
	return b.size >= b.optimalSize
}

func NewAppendBuffer(bufferOptimalSize datasize.ByteSize) *appendSortableBuffer {
	return &appendSortableBuffer{
		entries:     make(map[string][]byte),
		size:        0,
		optimalSize: int(bufferOptimalSize.Bytes()),
	}
}

type appendSortableBuffer struct {
	entries     map[string][]byte
	size        int
	optimalSize int
	sortedBuf   []sortableBufferEntry
	comparator  dbutils.CmpFunc
}

func (b *appendSortableBuffer) Put(k, v []byte) {
	ks := string(k)
	stored, ok := b.entries[ks]
	if !ok {
		b.size += len(k)
	}
	b.size += len(v)
	stored = append(stored, v...)
	b.entries[ks] = stored
}

func (b *appendSortableBuffer) SetComparator(cmp dbutils.CmpFunc) {
	b.comparator = cmp
}

func (b *appendSortableBuffer) Size() int {
	return b.size
}

func (b *appendSortableBuffer) Len() int {
	return len(b.entries)
}
func (b *appendSortableBuffer) Sort() {
	for i := range b.entries {
		b.sortedBuf = append(b.sortedBuf, sortableBufferEntry{key: []byte(i), value: b.entries[i]})
	}
	sort.Stable(b)
}

func (b *appendSortableBuffer) Less(i, j int) bool {
	if b.comparator != nil {
		return b.comparator(b.sortedBuf[i].key, b.sortedBuf[j].key, b.sortedBuf[i].value, b.sortedBuf[j].value) < 0
	}
	return bytes.Compare(b.sortedBuf[i].key, b.sortedBuf[j].key) < 0
}

func (b *appendSortableBuffer) Swap(i, j int) {
	b.sortedBuf[i], b.sortedBuf[j] = b.sortedBuf[j], b.sortedBuf[i]
}

func (b *appendSortableBuffer) Get(i int) sortableBufferEntry {
	return b.sortedBuf[i]
}
func (b *appendSortableBuffer) Reset() {
	b.sortedBuf = nil
	b.entries = make(map[string][]byte)
	b.size = 0
}

func (b *appendSortableBuffer) GetEntries() []sortableBufferEntry {
	return b.sortedBuf
}

func (b *appendSortableBuffer) CheckFlushSize() bool {
	return b.size >= b.optimalSize
}

func NewOldestEntryBuffer(bufferOptimalSize datasize.ByteSize) *oldestEntrySortableBuffer {
	return &oldestEntrySortableBuffer{
		entries:     make(map[string][]byte),
		size:        0,
		optimalSize: int(bufferOptimalSize.Bytes()),
	}
}

type oldestEntrySortableBuffer struct {
	entries     map[string][]byte
	size        int
	optimalSize int
	sortedBuf   []sortableBufferEntry
	comparator  dbutils.CmpFunc
}

func (b *oldestEntrySortableBuffer) SetComparator(cmp dbutils.CmpFunc) {
	b.comparator = cmp
}

func (b *oldestEntrySortableBuffer) Put(k, v []byte) {
	ks := string(k)
	_, ok := b.entries[ks]
	if ok {
		// if we already had this entry, we are going to keep it and ignore new value
		return
	}

	b.size += len(k)
	b.size += len(v)
	b.entries[ks] = v
}

func (b *oldestEntrySortableBuffer) Size() int {
	return b.size
}

func (b *oldestEntrySortableBuffer) Len() int {
	return len(b.entries)
}

func (b *oldestEntrySortableBuffer) Sort() {
	for k, v := range b.entries {
		b.sortedBuf = append(b.sortedBuf, sortableBufferEntry{key: []byte(k), value: v})
	}
	sort.Stable(b)
}

func (b *oldestEntrySortableBuffer) Less(i, j int) bool {
	if b.comparator != nil {
		return b.comparator(b.sortedBuf[i].key, b.sortedBuf[j].key, b.sortedBuf[i].value, b.sortedBuf[j].value) < 0
	}
	return bytes.Compare(b.sortedBuf[i].key, b.sortedBuf[j].key) < 0
}

func (b *oldestEntrySortableBuffer) Swap(i, j int) {
	b.sortedBuf[i], b.sortedBuf[j] = b.sortedBuf[j], b.sortedBuf[i]
}

func (b *oldestEntrySortableBuffer) Get(i int) sortableBufferEntry {
	return b.sortedBuf[i]
}
func (b *oldestEntrySortableBuffer) Reset() {
	b.sortedBuf = nil
	b.entries = make(map[string][]byte)
	b.size = 0
}

func (b *oldestEntrySortableBuffer) GetEntries() []sortableBufferEntry {
	return b.sortedBuf
}

func (b *oldestEntrySortableBuffer) CheckFlushSize() bool {
	return b.size >= b.optimalSize
}

func NewNewestEntryBuffer(bufferOptimalSize datasize.ByteSize) *newestEntrySortableBuffer {
	return &newestEntrySortableBuffer{
		entries:     make(map[string][]byte),
		size:        0,
		optimalSize: int(bufferOptimalSize.Bytes()),
	}
}

type newestEntrySortableBuffer struct {
	entries     map[string][]byte
	size        int
	optimalSize int
	sortedBuf   []sortableBufferEntry
	comparator  dbutils.CmpFunc
}

func (b *newestEntrySortableBuffer) SetComparator(cmp dbutils.CmpFunc) {
	b.comparator = cmp
}

func (b *newestEntrySortableBuffer) Put(k, v []byte) {
	ks := string(k)
	b.size += len(k)
	b.size += len(v)
	b.entries[ks] = v
}

func (b *newestEntrySortableBuffer) Size() int {
	return b.size
}

func (b *newestEntrySortableBuffer) Len() int {
	return len(b.entries)
}

func (b *newestEntrySortableBuffer) Sort() {
	for k, v := range b.entries {
		b.sortedBuf = append(b.sortedBuf, sortableBufferEntry{key: []byte(k), value: v})
	}
	sort.Stable(b)
}

func (b *newestEntrySortableBuffer) Less(i, j int) bool {
	if b.comparator != nil {
		return b.comparator(b.sortedBuf[i].key, b.sortedBuf[j].key, b.sortedBuf[i].value, b.sortedBuf[j].value) < 0
	}
	return bytes.Compare(b.sortedBuf[i].key, b.sortedBuf[j].key) < 0
}

func (b *newestEntrySortableBuffer) Swap(i, j int) {
	b.sortedBuf[i], b.sortedBuf[j] = b.sortedBuf[j], b.sortedBuf[i]
}

func (b *newestEntrySortableBuffer) Get(i int) sortableBufferEntry {
	return b.sortedBuf[i]
}
func (b *newestEntrySortableBuffer) Reset() {
	b.sortedBuf = nil
	b.entries = make(map[string][]byte)
	b.size = 0
}

func (b *newestEntrySortableBuffer) GetEntries() []sortableBufferEntry {
	return b.sortedBuf
}

func (b *newestEntrySortableBuffer) CheckFlushSize() bool {
	return b.size >= b.optimalSize
}

func getBufferByType(tp int, size datasize.ByteSize) Buffer {
	switch tp {
	case SortableSliceBuffer:
		return NewSortableBuffer(size)
	case SortableAppendBuffer:
		return NewAppendBuffer(size)
	case SortableOldestAppearedBuffer:
		return NewOldestEntryBuffer(size)
	case SortableNewestAppearedBuffer:
		return NewNewestEntryBuffer(size)
	default:
		panic("unknown buffer type " + strconv.Itoa(tp))
	}
}

func getTypeByBuffer(b Buffer) int {
	switch b.(type) {
	case *sortableBuffer:
		return SortableSliceBuffer
	case *appendSortableBuffer:
		return SortableAppendBuffer
	case *oldestEntrySortableBuffer:
		return SortableOldestAppearedBuffer
	case *newestEntrySortableBuffer:
		return SortableNewestAppearedBuffer
	default:
		panic(fmt.Sprintf("unknown buffer type: %T ", b))
	}
}

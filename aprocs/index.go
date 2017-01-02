package aprocs

import (
	"errors"
	"io"
	"sync"

	ss "secsplit"
	"secsplit/checksum"
	"secsplit/index"
	"secsplit/seriessort"
)

type Index interface {
	Proc
	EndProc
}

type indexProc struct {
	w        io.Writer
	order    seriessort.Series
	orderMu  sync.Mutex
	finals   map[checksum.Hash]*finals
	finalsMu sync.Mutex
}

type indexEntry struct {
	hash *checksum.Hash
	size int
}

func NewIndex(w io.Writer) Index {
	return &indexProc{
		w:      w,
		order:  seriessort.Series{},
		finals: make(map[checksum.Hash]*finals),
	}
}

func (idx *indexProc) Process(c *ss.Chunk) <-chan Res {
	idx.setOrder(c)
	ch := make(chan Res, 1)
	idx.finalsMu.Lock()
	defer idx.finalsMu.Unlock()
	if _, ok := idx.finals[c.Hash]; !ok {
		idx.finals[c.Hash] = &finals{entries: make([]indexEntry, 0, 1)}
		ch <- Res{Chunk: c}
	}
	close(ch)
	return ch
}

func (idx *indexProc) ProcessFinal(c, final *ss.Chunk) error {
	entry := indexEntry{hash: &final.Hash, size: c.Size}
	idx.finalsMu.Lock()
	defer idx.finalsMu.Unlock()
	finals, ok := idx.finals[c.Hash]
	if !ok {
		return errors.New("attempted to add final to unprocessed chunk")
	}
	finals.entries = append(finals.entries, entry)
	return nil
}

func (idx *indexProc) ProcessEnd(c *ss.Chunk) (err error) {
	err = idx.setFinalsComplete(c)
	if err != nil {
		return
	}
	return idx.flush()
}

func (idx *indexProc) setFinalsComplete(c *ss.Chunk) error {
	idx.finalsMu.Lock()
	defer idx.finalsMu.Unlock()
	finals, ok := idx.finals[c.Hash]
	if !ok {
		return errors.New("attempted to process end of unprocessed chunk")
	}
	finals.complete = true
	return nil
}

func (idx *indexProc) Finish() error {
	idx.orderMu.Lock()
	len := idx.order.Len()
	idx.orderMu.Unlock()
	if len > 0 {
		return ErrShort
	}
	return nil
}

func (idx *indexProc) flush() (err error) {
	idx.orderMu.Lock()
	defer idx.orderMu.Unlock()
	sorted := idx.order.Sorted()
	i := 0
	defer func() {
		idx.order.Drop(i)
	}()
	for n := len(sorted); i < n; i++ {
		hash := sorted[i].(*checksum.Hash)
		entries, ok := idx.completeFinals(*hash)
		if !ok {
			return
		}
		err = writeEntries(idx.w, entries)
		if err != nil {
			return
		}
	}
	return
}

func (idx *indexProc) completeFinals(hash checksum.Hash) (
	entries []indexEntry, ok bool,
) {
	idx.finalsMu.Lock()
	defer idx.finalsMu.Unlock()
	finals, ok := idx.finals[hash]
	if !ok {
		return
	}
	if !finals.complete {
		ok = false
		return
	}
	entries = make([]indexEntry, len(finals.entries))
	copy(entries, finals.entries)
	return
}

func (idx *indexProc) setOrder(c *ss.Chunk) {
	idx.orderMu.Lock()
	defer idx.orderMu.Unlock()
	idx.order.Add(c.Num, &c.Hash)
}

func writeEntries(w io.Writer, entries []indexEntry) (err error) {
	for _, entry := range entries {
		_, err = index.Write(w, *entry.hash, entry.size)
		if err != nil {
			return
		}
	}
	return
}

type finals struct {
	entries  []indexEntry
	complete bool
}

func (f *finals) Add(e indexEntry) {
	f.entries = append(f.entries, e)
}

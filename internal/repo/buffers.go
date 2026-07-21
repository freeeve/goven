package repo

import (
	"bytes"
	"io"
	"sync"
)

// copyBufPool holds 1MB transfer buffers reused across downloads, uploads,
// and hashing passes, keeping large copies at a handful of syscalls with no
// per-operation buffer allocation.
var copyBufPool = sync.Pool{New: func() any {
	b := make([]byte, 1<<20)
	return &b
}}

// writerOnly hides ReaderFrom so io.CopyBuffer honors the supplied buffer
// instead of delegating to the destination's own copy path.
type writerOnly struct{ io.Writer }

// copyPooled copies src to dst through a pooled 1MB buffer. In-memory
// sources short-circuit through WriteTo: they need no transfer buffer, and
// skipping the pool keeps GC-cleared pools from re-allocating 1MB buffers
// for byte-sized copies.
func copyPooled(dst io.Writer, src io.Reader) (int64, error) {
	if r, ok := src.(*bytes.Reader); ok {
		return r.WriteTo(writerOnly{dst})
	}
	bp := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bp)
	return io.CopyBuffer(writerOnly{dst}, src, *bp)
}

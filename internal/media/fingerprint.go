package media

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
)

const fpChunk = 64 << 10

// Fingerprint identifies file content cheaply (size + first and last 64 KiB),
// so a moved or renamed file keeps its identity without reading it fully —
// important on USB-2 disks.
func Fingerprint(path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	binary.Write(h, binary.LittleEndian, size)
	buf := make([]byte, fpChunk)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	h.Write(buf[:n])
	if size > fpChunk {
		// Any file with content beyond the first chunk contributes a tail
		// hash too. For files up to 2*fpChunk the head and tail reads
		// overlap; that's harmless since ReadFull always reads real bytes.
		if _, err := f.Seek(size-fpChunk, io.SeekStart); err != nil {
			return "", err
		}
		n, err = io.ReadFull(f, buf)
		if err != nil && err != io.ErrUnexpectedEOF {
			return "", err
		}
		h.Write(buf[:n])
	}
	return hex.EncodeToString(h.Sum(nil))[:32], nil
}

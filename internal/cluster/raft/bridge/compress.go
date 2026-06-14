package bridge

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Metric/host batches are verbose, highly compressible JSON (docker container
// lists with labels/ports dominate), so the bridge and the intra-cluster metric
// stream gzip the request body. The HMAC is always computed over the ON-WIRE
// bytes (i.e. the compressed body), so Sign/Verify need no change — only the
// decode side gunzips, and only AFTER the signature is verified.
const (
	EncodingHeader = "Content-Encoding"
	EncodingGzip   = "gzip"

	// MaxDecompressed bounds gunzip output to defuse a decompression bomb from a
	// (HMAC-authenticated, but still) peer. 64 MiB is far above any real batch.
	MaxDecompressed = 64 << 20
)

// Gzip compresses b. Returns (compressed, true) on success; on any error it
// returns (b, false) so the caller can ship the body uncompressed (and omit the
// Content-Encoding header).
func Gzip(b []byte) ([]byte, bool) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return b, false
	}
	if err := zw.Close(); err != nil {
		return b, false
	}
	return buf.Bytes(), true
}

// Gunzip decompresses b when encoding == "gzip"; otherwise returns b unchanged
// (so a plain, un-encoded body from an older/rolling peer still works). Output
// is capped at MaxDecompressed.
func Gunzip(encoding string, b []byte) ([]byte, error) {
	if encoding != EncodingGzip {
		return b, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, MaxDecompressed))
}

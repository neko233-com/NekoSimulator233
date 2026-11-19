// Package audioforward implements a minimal timestamped PCM relay for remote
// audio forwarding, with one-way and round-trip latency measurement. The
// transport is independent of the capture source (WASAPI loopback, QEMU audio
// backend, or a synthetic generator) so it can be validated without special
// audio hardware.
package audioforward

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"time"
)

const headerSize = 24

type Chunk struct {
	Seq     uint64
	SentNS  int64
	Samples []byte
}

func writeChunk(w io.Writer, c Chunk) error {
	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint64(header[0:8], c.Seq)
	binary.LittleEndian.PutUint64(header[8:16], uint64(c.SentNS))
	binary.LittleEndian.PutUint64(header[16:24], uint64(len(c.Samples)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(c.Samples)
	return err
}

func readChunk(r io.Reader) (Chunk, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return Chunk{}, err
	}
	seq := binary.LittleEndian.Uint64(header[0:8])
	sent := int64(binary.LittleEndian.Uint64(header[8:16]))
	size := int64(binary.LittleEndian.Uint64(header[16:24]))
	if size < 0 || size > 1<<20 {
		return Chunk{}, errors.New("invalid audio chunk size")
	}
	if _, err := io.CopyN(io.Discard, r, size); err != nil {
		return Chunk{}, err
	}
	return Chunk{Seq: seq, SentNS: sent}, nil
}

// Serve relays timestamped chunks and acknowledges each with the receive time.
// It returns when the client disconnects or the context is canceled.
func Serve(ctx context.Context, listener net.Listener) error {
	go func() { <-ctx.Done(); listener.Close() }()
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	for {
		chunk, err := readChunk(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		ack := make([]byte, 16)
		binary.LittleEndian.PutUint64(ack[0:8], chunk.Seq)
		binary.LittleEndian.PutUint64(ack[8:16], uint64(time.Now().UnixNano()))
		if _, err := conn.Write(ack); err != nil {
			return err
		}
	}
}

type LatencyReport struct {
	Chunks         int     `json:"chunks"`
	Bytes          int     `json:"bytes"`
	AvgOneWayMS    float64 `json:"avg_one_way_ms"`
	P95OneWayMS    float64 `json:"p95_one_way_ms"`
	MaxOneWayMS    float64 `json:"max_one_way_ms"`
	AvgRoundTripMS float64 `json:"avg_round_trip_ms"`
}

// Stream sends synthetic PCM chunks over TCP and measures relay timing.
func Stream(ctx context.Context, address string, chunks, chunkBytes int, interval time.Duration) (LatencyReport, error) {
	payload := make([]byte, chunks*chunkBytes)
	return StreamPCM(ctx, address, payload, chunkBytes, interval)
}

// StreamPCM streams real captured PCM (e.g. decoded guest audio) through the
// relay and measures the forwarding latency for every chunk.
func StreamPCM(ctx context.Context, address string, pcm []byte, chunkBytes int, interval time.Duration) (LatencyReport, error) {
	var report LatencyReport
	if chunkBytes <= 0 || len(pcm) == 0 {
		return report, errors.New("chunkBytes must be positive and PCM non-empty")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return report, err
	}
	defer conn.Close()
	var oneWay, rtt []float64
	for offset := 0; offset < len(pcm); offset += chunkBytes {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		end := offset + chunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		samples := pcm[offset:end]
		sent := time.Now()
		chunk := Chunk{Seq: uint64(len(oneWay) + 1), SentNS: sent.UnixNano(), Samples: samples}
		if err := writeChunk(conn, chunk); err != nil {
			return report, err
		}
		ack := make([]byte, 16)
		if _, err := io.ReadFull(conn, ack); err != nil {
			return report, err
		}
		if binary.LittleEndian.Uint64(ack[0:8]) != chunk.Seq {
			return report, errors.New("ack sequence mismatch")
		}
		received := int64(binary.LittleEndian.Uint64(ack[8:16]))
		oneWay = append(oneWay, float64(received-sent.UnixNano())/1e6)
		rtt = append(rtt, float64(time.Since(sent).Microseconds())/1000)
		report.Bytes += len(samples)
		if interval > 0 {
			select {
			case <-ctx.Done():
				return report, ctx.Err()
			case <-time.After(interval):
			}
		}
	}
	report.Chunks = len(oneWay)
	sort.Float64s(oneWay)
	sum := 0.0
	for _, v := range oneWay {
		sum += v
	}
	report.AvgOneWayMS = sum / float64(len(oneWay))
	report.P95OneWayMS = oneWay[int(float64(len(oneWay))*0.95)-1]
	report.MaxOneWayMS = oneWay[len(oneWay)-1]
	sum = 0
	for _, v := range rtt {
		sum += v
	}
	report.AvgRoundTripMS = sum / float64(len(rtt))
	return report, nil
}

// Listen opens a local relay on an ephemeral port.
func Listen() (net.Listener, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	return listener, listener.Addr().String(), nil
}

func (r LatencyReport) String() string {
	return fmt.Sprintf("%d chunks, avg %.3f ms, p95 %.3f ms, max %.3f ms, rtt %.3f ms",
		r.Chunks, r.AvgOneWayMS, r.P95OneWayMS, r.MaxOneWayMS, r.AvgRoundTripMS)
}

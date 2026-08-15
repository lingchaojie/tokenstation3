package spool

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

var (
	ErrSpoolCap    = errors.New("spool_cap")
	ErrFreeReserve = errors.New("spool_free_reserve")
)

const filesystemBlockSize int64 = 4096

var reservationSizer = mustReservationSizer()

type CapacityConfig struct {
	RootDir                  string
	MaxBytes                 int64
	MinFreeBytes             int64
	OperationalHeadroomBytes int64
}

type usage struct {
	Allocated            int64
	OperationalAllocated int64
	Free                 int64
	BlockSize            int64
}

type usageFunc func() (usage, error)

type Capacity struct {
	mu sync.Mutex

	config              CapacityConfig
	usageFn             usageFunc
	reservedContent     int64
	reservedOperational int64
}

type reservationState struct {
	mu          sync.Mutex
	capacity    *Capacity
	want        int64
	operational bool
	done        bool
}

// Reservation is an idempotently releasable entry in the concurrent capacity
// ledger. Copies share the same state and therefore cannot double-release.
type Reservation struct {
	state *reservationState
}

func newCapacity(config CapacityConfig, current usageFunc) (*Capacity, error) {
	if config.MaxBytes <= 0 {
		return nil, errors.New("spool max bytes must be positive")
	}
	if config.MinFreeBytes < 0 {
		return nil, errors.New("spool minimum free bytes cannot be negative")
	}
	if config.OperationalHeadroomBytes < 0 || config.OperationalHeadroomBytes >= config.MaxBytes {
		return nil, errors.New("invalid spool operational headroom")
	}
	if current == nil {
		if config.RootDir == "" {
			return nil, errors.New("spool root directory is required")
		}
		current = func() (usage, error) { return scanUsage(config.RootDir) }
	}
	return &Capacity{config: config, usageFn: current}, nil
}

// Reserve accounts space intended for new record content. New content may not
// consume the operational headroom reserved for later batch metadata and acks.
func (c *Capacity) Reserve(recordID uuid.UUID, want int64) (Reservation, error) {
	return c.ReserveContent(recordID, want)
}

func (c *Capacity) ReserveContent(_ uuid.UUID, want int64) (Reservation, error) {
	if want < 0 {
		return Reservation{}, errors.New("reservation size cannot be negative")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.usageFn()
	if err != nil {
		return Reservation{}, fmt.Errorf("measure spool capacity: %w", err)
	}
	return c.reserveContentLocked(current, want)
}

func (c *Capacity) ReserveFrame(_ uuid.UUID, frameBytes int) (Reservation, error) {
	if frameBytes < 0 {
		return Reservation{}, errors.New("frame size cannot be negative")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.usageFn()
	if err != nil {
		return Reservation{}, fmt.Errorf("measure spool capacity: %w", err)
	}
	blockSize := current.BlockSize
	if blockSize <= 0 {
		blockSize = filesystemBlockSize
	}
	want := worstCaseFrameReservationForBlock(frameBytes, blockSize)
	return c.reserveContentLocked(current, want)
}

func (c *Capacity) reserveContentLocked(current usage, want int64) (Reservation, error) {
	totalReserved := c.reservedContent + c.reservedOperational
	contentLimit := c.config.MaxBytes - c.config.OperationalHeadroomBytes
	if exceeds(current.Allocated, totalReserved, want, contentLimit) {
		return Reservation{}, ErrSpoolCap
	}
	if current.Free-totalReserved-want < c.config.MinFreeBytes {
		return Reservation{}, ErrFreeReserve
	}
	c.reservedContent += want
	return Reservation{state: &reservationState{capacity: c, want: want}}, nil
}

// ReserveOperational permits bounded metadata to use the final operational
// headroom, but never permits the physical cap or free-space reserve to be
// crossed.
func (c *Capacity) ReserveOperational(want int64) error {
	reservation, err := c.reserveOperational(want)
	if err != nil {
		return err
	}
	reservation.Release()
	return nil
}

func (c *Capacity) reserveOperational(want int64) (Reservation, error) {
	if want < 0 {
		return Reservation{}, errors.New("reservation size cannot be negative")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.usageFn()
	if err != nil {
		return Reservation{}, fmt.Errorf("measure spool capacity: %w", err)
	}
	totalReserved := c.reservedContent + c.reservedOperational
	if exceeds(current.OperationalAllocated, c.reservedOperational, want, c.config.OperationalHeadroomBytes) {
		return Reservation{}, ErrSpoolCap
	}
	if exceeds(current.Allocated, totalReserved, want, c.config.MaxBytes) {
		return Reservation{}, ErrSpoolCap
	}
	if current.Free-totalReserved-want < c.config.MinFreeBytes {
		return Reservation{}, ErrFreeReserve
	}
	c.reservedOperational += want
	return Reservation{state: &reservationState{capacity: c, want: want, operational: true}}, nil
}

// BeforeFrame performs the same pessimistic admission check used by attempts.
// The temporary reservation is released because callers that write must retain
// the Reservation returned by ReserveContent until their flush/stat reconcile.
func (c *Capacity) BeforeFrame(recordID uuid.UUID, frame []byte) error {
	r, err := c.ReserveFrame(recordID, len(frame))
	if err != nil {
		return err
	}
	r.Release()
	return nil
}

// Consume reconciles a completed disk write. Actual allocation is obtained by
// the capacity scanner, so reconciliation only retires the pessimistic ledger
// entry. The argument is validated to catch bad callers without risking
// negative accounting.
func (r Reservation) Consume(actual int64) error {
	if actual < 0 {
		return errors.New("consumed allocation cannot be negative")
	}
	r.release()
	return nil
}

func (r Reservation) Release() {
	r.release()
}

func (r Reservation) release() {
	if r.state == nil {
		return
	}
	r.state.mu.Lock()
	if r.state.done {
		r.state.mu.Unlock()
		return
	}
	r.state.done = true
	c := r.state.capacity
	want := r.state.want
	operational := r.state.operational
	r.state.mu.Unlock()

	c.mu.Lock()
	if operational {
		c.reservedOperational -= want
		if c.reservedOperational < 0 {
			c.reservedOperational = 0
		}
	} else {
		c.reservedContent -= want
		if c.reservedContent < 0 {
			c.reservedContent = 0
		}
	}
	c.mu.Unlock()
}

func (c *Capacity) reservedBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reservedContent + c.reservedOperational
}

func (c *Capacity) snapshot() (usage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.usageFn()
	if err != nil {
		return usage{}, err
	}
	current.Allocated += c.reservedContent + c.reservedOperational
	current.Free -= c.reservedContent + c.reservedOperational
	if current.Free < 0 {
		current.Free = 0
	}
	return current, nil
}

func exceeds(values ...int64) bool {
	if len(values) < 2 {
		return false
	}
	limit := values[len(values)-1]
	var total int64
	for _, value := range values[:len(values)-1] {
		if value > limit-total {
			return true
		}
		total += value
	}
	return false
}

func worstCaseFrameReservation(size int) int64 {
	return worstCaseFrameReservationForBlock(size, filesystemBlockSize)
}

func worstCaseFrameReservationForBlock(size int, blockSize int64) int64 {
	want := int64(reservationSizer.MaxEncodedSize(size))
	return roundUp(want, blockSize)
}

func mustReservationSizer() *zstd.Encoder {
	encoder, err := zstd.NewWriter(io.Discard, zstd.WithEncoderConcurrency(1), zstd.WithLowerEncoderMem(true))
	if err != nil {
		panic(fmt.Sprintf("create zstd reservation sizer: %v", err))
	}
	return encoder
}

func roundUp(value, block int64) int64 {
	if value <= 0 {
		return 0
	}
	return ((value + block - 1) / block) * block
}

func scanUsage(root string) (usage, error) {
	var allocated int64
	var operationalAllocated int64
	for _, name := range []string{"partial", "ready", "sending"} {
		directory := filepath.Join(root, name)
		var directoryAllocated int64
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				directoryAllocated += stat.Blocks * 512
			} else if info.Mode().IsRegular() {
				directoryAllocated += roundUp(info.Size(), filesystemBlockSize)
			}
			return nil
		})
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return usage{}, err
		}
		allocated += directoryAllocated
		if name == "sending" {
			operationalAllocated += directoryAllocated
		}
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return usage{}, err
	}
	return usage{
		Allocated:            allocated,
		OperationalAllocated: operationalAllocated,
		Free:                 int64(stat.Bavail) * int64(stat.Bsize),
		BlockSize:            int64(stat.Bsize),
	}, nil
}

func allocatedBytes(path string) (int64, error) {
	var allocated int64
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		allocated += allocatedFileInfo(info)
		return nil
	})
	return allocated, err
}

func allocatedFileInfo(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Blocks * 512
	}
	if info.Mode().IsRegular() {
		return roundUp(info.Size(), filesystemBlockSize)
	}
	return 0
}

func isCapacityError(err error) bool {
	return errors.Is(err, ErrSpoolCap) || errors.Is(err, ErrFreeReserve)
}

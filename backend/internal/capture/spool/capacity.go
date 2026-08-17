package spool

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

var (
	ErrSpoolCap    = errors.New("spool_cap")
	ErrFreeReserve = errors.New("spool_free_reserve")
)

const filesystemBlockSize int64 = 4096

const (
	maxCapacityScanDepth = 64
	// Allow several directory entries per minimum allocated block across the
	// entire 12 GiB safety envelope while still bounding adversarial zero-block
	// entries and total scanner work.
	maxCapacityScanEntries = int(defaultMaxBytes/filesystemBlockSize) * 4
	capacityScanChunkSize  = 128
)

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
type freeFunc func() (int64, error)

type Capacity struct {
	mu         sync.Mutex
	mutationMu sync.Mutex

	config               CapacityConfig
	allocated            int64
	operationalAllocated int64
	free                 int64
	blockSize            int64
	currentFree          freeFunc
	reservedContent      int64
	reservedOperational  int64
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
	productionScan := current == nil
	if productionScan {
		if config.RootDir == "" {
			return nil, errors.New("spool root directory is required")
		}
		current = func() (usage, error) { return scanUsage(config.RootDir) }
	}
	initial, err := current()
	if err != nil {
		return nil, fmt.Errorf("measure spool capacity: %w", err)
	}
	if initial.Allocated < 0 || initial.OperationalAllocated < 0 || initial.OperationalAllocated > initial.Allocated || initial.Free < 0 {
		return nil, ErrSpoolCorrupt
	}
	if initial.BlockSize <= 0 {
		initial.BlockSize = filesystemBlockSize
	}
	capacity := &Capacity{
		config:               config,
		allocated:            initial.Allocated,
		operationalAllocated: initial.OperationalAllocated,
		free:                 initial.Free,
		blockSize:            initial.BlockSize,
	}
	if productionScan {
		capacity.currentFree = func() (int64, error) { return filesystemFreeBytes(config.RootDir) }
	}
	return capacity, nil
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
	measuredFree, err := c.measureCurrentFree()
	if err != nil {
		return Reservation{}, fmt.Errorf("measure spool capacity: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.currentLocked(measuredFree)
	return c.reserveContentLocked(current, want)
}

func (c *Capacity) ReserveFrame(_ uuid.UUID, frameBytes int) (Reservation, error) {
	if frameBytes < 0 {
		return Reservation{}, errors.New("frame size cannot be negative")
	}
	measuredFree, err := c.measureCurrentFree()
	if err != nil {
		return Reservation{}, fmt.Errorf("measure spool capacity: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.currentLocked(measuredFree)
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
	contentAllocated := current.Allocated - current.OperationalAllocated
	if contentAllocated < 0 {
		contentAllocated = 0
	}
	if exceeds(contentAllocated, c.reservedContent, want, contentLimit) {
		return Reservation{}, ErrSpoolCap
	}
	if exceeds(current.Allocated, totalReserved, want, c.config.MaxBytes) {
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
	return c.reserveOperationalWithFreeReserve(want, true)
}

// reserveOperationalFilesUnblocking admits only pre-bounded sending metadata.
// It may cross the normal filesystem free-space reserve so an existing ready
// backlog can still be uploaded and deleted, but it cannot cross actual free
// space, the spool's physical cap, or its dedicated operational headroom.
func (c *Capacity) reserveOperationalFilesUnblocking(fileSizes ...int64) (Reservation, error) {
	measuredFree, err := c.measureCurrentFree()
	if err != nil {
		return Reservation{}, fmt.Errorf("measure spool capacity: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.currentLocked(measuredFree)
	blockSize := current.BlockSize
	if blockSize <= 0 {
		blockSize = filesystemBlockSize
	}
	var want int64
	for _, size := range fileSizes {
		if size < 0 {
			return Reservation{}, errors.New("reservation size cannot be negative")
		}
		allocated := roundUp(size, blockSize)
		if allocated > c.config.OperationalHeadroomBytes-blockSize {
			return Reservation{}, ErrSpoolCap
		}
		allocated += blockSize // worst-case sending-directory growth for one new entry
		if allocated < 0 || allocated > c.config.OperationalHeadroomBytes-want {
			return Reservation{}, ErrSpoolCap
		}
		want += allocated
	}
	return c.reserveOperationalLocked(current, want, false)
}

func (c *Capacity) reserveOperationalWithFreeReserve(want int64, enforceFreeReserve bool) (Reservation, error) {
	if want < 0 {
		return Reservation{}, errors.New("reservation size cannot be negative")
	}
	measuredFree, err := c.measureCurrentFree()
	if err != nil {
		return Reservation{}, fmt.Errorf("measure spool capacity: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.currentLocked(measuredFree)
	return c.reserveOperationalLocked(current, want, enforceFreeReserve)
}

func (c *Capacity) reserveOperationalLocked(current usage, want int64, enforceFreeReserve bool) (Reservation, error) {
	totalReserved := c.reservedContent + c.reservedOperational
	if exceeds(current.OperationalAllocated, c.reservedOperational, want, c.config.OperationalHeadroomBytes) {
		return Reservation{}, ErrSpoolCap
	}
	if exceeds(current.Allocated, totalReserved, want, c.config.MaxBytes) {
		return Reservation{}, ErrSpoolCap
	}
	remainingFree := current.Free - totalReserved
	if remainingFree < 0 || remainingFree < want {
		return Reservation{}, ErrFreeReserve
	}
	if enforceFreeReserve && remainingFree-want < c.config.MinFreeBytes {
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

// Consume atomically transfers a completed disk allocation from the
// pessimistic reservation ledger into the cached exact allocation ledger.
func (r Reservation) Consume(actual int64) error {
	if actual < 0 {
		return errors.New("consumed allocation cannot be negative")
	}
	return r.finish(actual, true)
}

func (r Reservation) Release() {
	_ = r.finish(0, false)
}

func (r Reservation) consumeAllocationDelta(delta int64) error {
	return r.finish(delta, true)
}

func (r Reservation) finish(actual int64, consumed bool) error {
	if r.state == nil {
		return nil
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.done {
		return nil
	}
	c := r.state.capacity
	want := r.state.want
	operational := r.state.operational

	c.mu.Lock()
	defer c.mu.Unlock()
	if consumed {
		if err := c.adjustAllocatedLocked(actual, operational); err != nil {
			// Keep the pessimistic reservation live when reconciliation cannot be
			// applied; dropping both would under-count durable bytes.
			return err
		}
	}
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
	r.state.done = true
	return nil
}

func (c *Capacity) reservedBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reservedContent + c.reservedOperational
}

func (c *Capacity) snapshot() (usage, error) {
	measuredFree, err := c.measureCurrentFree()
	if err != nil {
		return usage{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.currentLocked(measuredFree)
	current.Allocated += c.reservedContent + c.reservedOperational
	current.Free -= c.reservedContent + c.reservedOperational
	if current.Free < 0 {
		current.Free = 0
	}
	return current, nil
}

// measureCurrentFree performs the only per-admission filesystem operation
// before taking the admission ledger lock. The allocation tree is never
// rescanned, and a slow statfs cannot serialize reservations or reconciliation.
func (c *Capacity) measureCurrentFree() (*int64, error) {
	c.mu.Lock()
	currentFree := c.currentFree
	c.mu.Unlock()
	if currentFree == nil {
		return nil, nil
	}
	free, err := currentFree()
	if err != nil {
		return nil, err
	}
	return &free, nil
}

func (c *Capacity) currentLocked(measuredFree *int64) usage {
	free := c.free
	if measuredFree != nil {
		free = *measuredFree
	}
	return usage{
		Allocated:            c.allocated,
		OperationalAllocated: c.operationalAllocated,
		Free:                 free,
		BlockSize:            c.blockSize,
	}
}

func (c *Capacity) releaseAllocated(actual int64, operational bool) error {
	if actual < 0 {
		return errors.New("released allocation cannot be negative")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.adjustAllocatedLocked(-actual, operational)
}

func (c *Capacity) trackAllocationMutation(paths []string, operational bool, mutate func() error) error {
	return c.trackAllocationMutationKind(paths, operational, false, mutate)
}

// trackAllocationMutationWithOperationalGrowth reserves one worst-case block
// for each parent path before an unreserved additive metadata mutation. The
// guard is visible to concurrent admissions until the exact post-mutation
// allocation delta has been installed in the cached ledger.
func (c *Capacity) trackAllocationMutationWithOperationalGrowth(paths []string, operational bool, mutate func() error) error {
	fileSizes := make([]int64, len(paths))
	reservation, err := c.reserveOperationalFilesUnblocking(fileSizes...)
	if err != nil {
		return err
	}
	if err := c.trackAllocationMutation(paths, operational, mutate); err != nil {
		// The mutation may have changed allocation before reporting failure.
		// Retain the pessimistic guard until restart rather than under-count an
		// allocation whose exact durable state is not trustworthy.
		return err
	}
	reservation.Release()
	return nil
}

// trackAllocationDeletion leaves the pre-delete allocation charged throughout
// the filesystem operation. Admissions therefore see an exact or conservative
// over-count until the post-delete measurement is applied. A failed post-delete
// measurement also leaves the old charge in place for restart recovery.
func (c *Capacity) trackAllocationDeletion(paths []string, operational bool, mutate func() error) error {
	return c.trackAllocationMutationKind(paths, operational, true, mutate)
}

func (c *Capacity) trackAllocationMutationKind(paths []string, operational, deletion bool, mutate func() error) error {
	if mutate == nil {
		return errors.New("capacity mutation is required")
	}
	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()
	before, err := allocatedPaths(paths)
	if err != nil {
		return err
	}
	mutationErr := mutate()
	after, measureErr := allocatedPaths(paths)
	if measureErr != nil {
		if mutationErr != nil {
			return mutationErr
		}
		return measureErr
	}
	if deletion && after > before {
		return ErrSpoolCorrupt
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.adjustAllocatedLocked(after-before, operational); err != nil {
		return err
	}
	return mutationErr
}

func (c *Capacity) adjustAllocatedLocked(delta int64, operational bool) error {
	if delta < 0 && -delta > c.allocated {
		return ErrSpoolCorrupt
	}
	if delta > 0 && c.allocated > int64(^uint64(0)>>1)-delta {
		return ErrSpoolCorrupt
	}
	if operational {
		if delta < 0 && -delta > c.operationalAllocated {
			return ErrSpoolCorrupt
		}
		if delta > 0 && c.operationalAllocated > int64(^uint64(0)>>1)-delta {
			return ErrSpoolCorrupt
		}
	}
	c.allocated += delta
	if operational {
		c.operationalAllocated += delta
	}
	if c.currentFree == nil {
		c.free -= delta
		if c.free < 0 {
			c.free = 0
		}
	}
	return nil
}

func allocatedPaths(paths []string) (int64, error) {
	var total int64
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		allocated := allocatedFileInfo(info)
		if allocated > int64(^uint64(0)>>1)-total {
			return 0, ErrSpoolCorrupt
		}
		total += allocated
	}
	return total, nil
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

func isCapacityError(err error) bool {
	return errors.Is(err, ErrSpoolCap) || errors.Is(err, ErrFreeReserve)
}

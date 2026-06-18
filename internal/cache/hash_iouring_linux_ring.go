//go:build linux

package cache

import (
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// io_uring syscall numbers (stable since kernel 5.1 on all arches).
const (
	sysIoUringSetup = 425
	sysIoUringEnter = 426
)

// IORING_* constants from linux/io_uring.h.
const (
	opRead     = 22
	getEvents  = 1 // IORING_ENTER_GETEVENTS
	offSqRing  = uintptr(0)
	offCqRing  = uintptr(0x8000000)
	offSqes    = uintptr(0x10000000)
	featSingle = uint32(1) // IORING_FEAT_SINGLE_MMAP
)

// ringParams mirrors struct io_uring_params (linux/io_uring.h, 120 bytes).
type ringParams struct {
	sqEntries    uint32
	cqEntries    uint32
	flags        uint32
	sqThreadCPU  uint32
	sqThreadIdle uint32
	features     uint32
	wqFD         uint32
	_resv        [3]uint32
	sqOff        sqringOff // 40 bytes
	cqOff        cqringOff // 40 bytes
}

type sqringOff struct {
	head, tail, ringMask, ringEntries, flags, dropped, array uint32
	_resv1                                                   uint32
	_resv2                                                   uint64
}

type cqringOff struct {
	head, tail, ringMask, ringEntries, overflow, cqes, flags uint32
	_resv1                                                   uint32
	_resv2                                                   uint64
}

// sqe mirrors struct io_uring_sqe (64 bytes).
type sqe struct {
	opcode   uint8
	flags    uint8
	ioprio   uint16
	fd       int32
	off      uint64
	addr     uint64
	length   uint32
	rwFlags  uint32
	userData uint64
	_pad     [3]uint64
}

// cqe mirrors struct io_uring_cqe (16 bytes).
type cqe struct {
	userData uint64
	res      int32
	flags    uint32
}

// CQE is a copy-by-value io_uring completion event.
type CQE struct {
	UserData uint64 // tag attached at submission
	Result   int32  // bytes read on success, negated errno on failure
}

// ReadErr returns nil if the read completed exactly wantLen bytes,
// a syscall.Errno for a kernel error, or an error for a short read.
func (c CQE) ReadErr(wantLen int) error {
	if c.Result < 0 {
		return syscall.Errno(-c.Result)
	}
	if int(c.Result) != wantLen {
		return fmt.Errorf("iouring: short read: got %d want %d", c.Result, wantLen)
	}
	return nil
}

// Ring is an io_uring instance with its three mmaps.
type Ring struct {
	fd      int
	params  ringParams
	sqMem   []byte // mmap for SQ ring
	cqMem   []byte // mmap for CQ ring (may alias sqMem)
	sqesMem []byte // mmap for SQE array
	single  bool   // IORING_FEAT_SINGLE_MMAP: sqMem == cqMem
}

// checkKernelVersion returns an error if the running kernel is older than major.minor.
// IORING_OP_READ requires kernel ≥ 5.6.
func checkKernelVersion(major, minor int) error {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return fmt.Errorf("iouring: uname: %w", err)
	}
	// Utsname.Release is a fixed [65]byte buffer; copy up to the NUL terminator.
	b := make([]byte, 0, len(u.Release))
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	var kmaj, kmin int
	if _, err := fmt.Sscanf(string(b), "%d.%d", &kmaj, &kmin); err != nil {
		return fmt.Errorf("iouring: parse kernel version %q: %w", string(b), err)
	}
	if kmaj < major || (kmaj == major && kmin < minor) {
		return fmt.Errorf("iouring: kernel %d.%d < required %d.%d; IORING_OP_READ unavailable",
			kmaj, kmin, major, minor)
	}
	return nil
}

// NewRing creates a new io_uring with at least entries SQEs and the
// matching number of CQEs (chosen by the kernel; usually 2× entries).
func NewRing(entries uint32) (*Ring, error) {
	if err := checkKernelVersion(5, 6); err != nil {
		return nil, err
	}
	r := &Ring{}
	fd, _, errno := syscall.Syscall(sysIoUringSetup,
		uintptr(entries),
		uintptr(unsafe.Pointer(&r.params)),
		0)
	if errno != 0 {
		return nil, fmt.Errorf("io_uring_setup: %w", errno)
	}
	r.fd = int(fd)
	r.single = r.params.features&featSingle != 0

	sqRingSize := uintptr(r.params.sqOff.array) + uintptr(r.params.sqEntries)*4
	sqMem, err := syscall.Mmap(r.fd, int64(offSqRing), int(sqRingSize),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_POPULATE)
	if err != nil {
		_ = syscall.Close(r.fd)
		return nil, fmt.Errorf("mmap SQ ring: %w", err)
	}
	r.sqMem = sqMem

	if r.single {
		r.cqMem = sqMem
	} else {
		cqRingSize := uintptr(r.params.cqOff.cqes) + uintptr(r.params.cqEntries)*unsafe.Sizeof(cqe{})
		cqMem, err := syscall.Mmap(r.fd, int64(offCqRing), int(cqRingSize),
			syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_POPULATE)
		if err != nil {
			_ = syscall.Munmap(sqMem)
			_ = syscall.Close(r.fd)
			return nil, fmt.Errorf("mmap CQ ring: %w", err)
		}
		r.cqMem = cqMem
	}

	sqeSize := uintptr(r.params.sqEntries) * unsafe.Sizeof(sqe{})
	sqesMem, err := syscall.Mmap(r.fd, int64(offSqes), int(sqeSize),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_POPULATE)
	if err != nil {
		if !r.single {
			_ = syscall.Munmap(r.cqMem)
		}
		_ = syscall.Munmap(sqMem)
		_ = syscall.Close(r.fd)
		return nil, fmt.Errorf("mmap SQEs: %w", err)
	}
	r.sqesMem = sqesMem
	return r, nil
}

// Close unmaps the ring and closes its fd. Subsequent operations on r
// are undefined.
func (r *Ring) Close() error {
	_ = syscall.Munmap(r.sqesMem)
	if !r.single {
		_ = syscall.Munmap(r.cqMem)
	}
	_ = syscall.Munmap(r.sqMem)
	return syscall.Close(r.fd)
}

func (r *Ring) sqHeadPtr() *uint32 {
	return (*uint32)(unsafe.Pointer(&r.sqMem[r.params.sqOff.head]))
}

func (r *Ring) sqTailPtr() *uint32 {
	return (*uint32)(unsafe.Pointer(&r.sqMem[r.params.sqOff.tail]))
}

func (r *Ring) sqMask() uint32 {
	return *(*uint32)(unsafe.Pointer(&r.sqMem[r.params.sqOff.ringMask]))
}

func (r *Ring) sqArrayEntry(i uint32) *uint32 {
	return (*uint32)(unsafe.Pointer(&r.sqMem[r.params.sqOff.array+i*4]))
}

func (r *Ring) sqeAt(slot uint32) *sqe {
	return (*sqe)(unsafe.Pointer(&r.sqesMem[slot*uint32(unsafe.Sizeof(sqe{}))]))
}

func (r *Ring) cqHeadPtr() *uint32 {
	return (*uint32)(unsafe.Pointer(&r.cqMem[r.params.cqOff.head]))
}

func (r *Ring) cqTailPtr() *uint32 {
	return (*uint32)(unsafe.Pointer(&r.cqMem[r.params.cqOff.tail]))
}

func (r *Ring) cqMask() uint32 {
	return *(*uint32)(unsafe.Pointer(&r.cqMem[r.params.cqOff.ringMask]))
}

func (r *Ring) cqeAt(slot uint32) *cqe {
	return (*cqe)(unsafe.Pointer(&r.cqMem[r.params.cqOff.cqes+slot*uint32(unsafe.Sizeof(cqe{}))]))
}

// SubmitRead queues an IORING_OP_READ for fd into buf tagged with userData.
// buf must remain alive until the matching CQE is drained.
func (r *Ring) SubmitRead(fd int, buf []byte, userData uint64) error {
	if len(buf) == 0 {
		return errors.New("iouring: SubmitRead: empty buffer")
	}

	tail := atomic.LoadUint32(r.sqTailPtr())
	slot := tail & r.sqMask()

	s := r.sqeAt(slot)
	s.opcode = opRead
	s.flags = 0
	s.ioprio = 0
	s.fd = int32(fd)
	s.off = 0
	s.addr = uint64(uintptr(unsafe.Pointer(&buf[0])))
	s.length = uint32(len(buf))
	s.rwFlags = 0
	s.userData = userData

	*r.sqArrayEntry(slot) = slot
	atomic.StoreUint32(r.sqTailPtr(), tail+1)
	return nil
}

// SubmitAndWait submits pending SQEs and blocks until minComplete CQEs are available.
// io_uring_enter(GETEVENTS) is the release/acquire fence for CQ reads.
func (r *Ring) SubmitAndWait(minComplete uint32) (int, error) {
	toSubmit := atomic.LoadUint32(r.sqTailPtr()) - atomic.LoadUint32(r.sqHeadPtr())
	var n uintptr
	var errno syscall.Errno
	for {
		n, _, errno = syscall.Syscall6(sysIoUringEnter,
			uintptr(r.fd),
			uintptr(toSubmit),
			uintptr(minComplete),
			uintptr(getEvents),
			0, 0)
		if errno != syscall.EINTR {
			break
		}
	}
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

// DrainCompletions visits every available CQE and advances the CQ head.
// Call only after a successful SubmitAndWait.
func (r *Ring) DrainCompletions(visit func(CQE)) {
	head := atomic.LoadUint32(r.cqHeadPtr())
	tail := atomic.LoadUint32(r.cqTailPtr())
	for head != tail {
		c := r.cqeAt(head & r.cqMask())
		visit(CQE{UserData: c.userData, Result: c.res})
		head++
	}
	atomic.StoreUint32(r.cqHeadPtr(), head)
}

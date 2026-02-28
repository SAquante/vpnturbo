package transport

import (
	"sync"
)

const (
	// DefaultWindowSize is the default size of the anti-replay window
	DefaultWindowSize = 1024
)

// AntiReplayWindow implements a sliding window for sequence numbers to prevent replay attacks
type AntiReplayWindow struct {
	mu     sync.Mutex
	window []bool
	head   uint32
	size   uint32
}

// NewAntiReplayWindow creates a new AntiReplayWindow
func NewAntiReplayWindow(size uint32) *AntiReplayWindow {
	if size == 0 {
		size = DefaultWindowSize
	}
	return &AntiReplayWindow{
		window: make([]bool, size),
		head:   0,
		size:   size,
	}
}

// Check verifies if a sequence number is acceptable (not seen before and within the window).
// If acceptable, it marks the sequence number as seen and returns true.
// If it's a replay or too old, it returns false.
func (ar *AntiReplayWindow) Check(seq uint32) bool {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	// Handle the very first packet
	if ar.head == 0 && seq == 0 {
		ar.window[0] = true
		return true
	}

	// Sequence number is too old to track (falls behind the sliding window)
	if ar.head >= ar.size && seq < ar.head-ar.size {
		return false // Drop old packet
	}

	// New highest sequence number, slide the window
	if seq > ar.head {
		shift := seq - ar.head
		if shift >= ar.size {
			// Shift is larger than window size, clear the whole window
			for i := uint32(0); i < ar.size; i++ {
				ar.window[i] = false
			}
		} else {
			// Clear bits for the new window positions
			for i := uint32(1); i <= shift; i++ {
				ar.window[(ar.head+i)%ar.size] = false
			}
		}
		ar.head = seq
		ar.window[seq%ar.size] = true
		return true
	}

	// Sequence number is within the window, check if it was already seen
	index := seq % ar.size
	if ar.window[index] {
		return false // Replay attack detected
	}

	// Mark as seen
	ar.window[index] = true
	return true
}

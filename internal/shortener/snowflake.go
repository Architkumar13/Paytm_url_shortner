package shortener

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Snowflake is a coordination-free IDGenerator for distributed deployments.
// Each 64-bit ID packs:
//
//	| 1 unused | 41-bit ms since epoch | 10-bit machine id | 12-bit sequence |
//
// IDs are unique across up to 1024 machines without any shared state, and are
// time-ordered. It is an alternative to SequenceGenerator selectable via the
// ID_GENERATOR=snowflake environment variable; enabling it lets the service
// scale horizontally without a centralized counter.
const (
	snowflakeEpochMs = int64(1704067200000) // 2024-01-01T00:00:00Z
	machineIDBits    = 10
	sequenceBits     = 12
	maxMachineID     = -1 ^ (-1 << machineIDBits) // 1023
	maxSequence      = -1 ^ (-1 << sequenceBits)  // 4095
	machineIDShift   = sequenceBits
	timestampShift   = sequenceBits + machineIDBits
)

// Snowflake is safe for concurrent use.
type Snowflake struct {
	mu        sync.Mutex
	machineID int64
	lastMs    int64
	sequence  int64
}

// NewSnowflake returns a Snowflake generator for the given machine id
// (0..1023). Distinct instances must use distinct machine ids.
func NewSnowflake(machineID int64) (*Snowflake, error) {
	if machineID < 0 || machineID > maxMachineID {
		return nil, fmt.Errorf("machine id %d out of range [0,%d]", machineID, maxMachineID)
	}
	return &Snowflake{machineID: machineID}, nil
}

func (s *Snowflake) NextID(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Never emit a smaller timestamp than the last one: if the clock moved
	// backwards, pin to the last observed millisecond so IDs stay monotonic.
	ms := max(time.Now().UnixMilli(), s.lastMs)
	if ms == s.lastMs {
		s.sequence = (s.sequence + 1) & maxSequence
		if s.sequence == 0 {
			// Sequence exhausted this millisecond; spin to the next one.
			for ms <= s.lastMs {
				ms = time.Now().UnixMilli()
			}
		}
	} else {
		s.sequence = 0
	}
	s.lastMs = ms

	id := ((ms - snowflakeEpochMs) << timestampShift) |
		(s.machineID << machineIDShift) |
		s.sequence
	return uint64(id), nil
}

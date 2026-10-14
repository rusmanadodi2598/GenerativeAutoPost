// Package media defines upload object shapes and the resumable chunk planner.
//
// @file      internal/media/domain.go
// @for       Chunk size rules and the byte range planner for uploads.
// @uses      internal/apierror
// @reason    Holds the only rules that decide how an upload is split.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     domain
// @stability stable
// @since     2026-10-12
package media

import "github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"

const (
	// MinChunkSize is the smallest accepted chunk size (5 MB).
	MinChunkSize int64 = 5 << 20
	// MaxChunkSize is the largest accepted chunk size (64 MB).
	MaxChunkSize int64 = 64 << 20
	// MaxChunks is the highest accepted chunk count for one object.
	MaxChunks = 1000
)

// ChunkPlan is one half open byte range of an object.
type ChunkPlan struct {
	Index int   `json:"index"`
	First int64 `json:"first"`
	Last  int64 `json:"last"`
	Total int64 `json:"total"`
}

// Size returns the byte length of the range.
func (c ChunkPlan) Size() int64 { return c.Last - c.First }

// PlanChunks splits totalBytes into ordered ranges of chunkSize bytes.
//
// A trailing remainder smaller than MinChunkSize merges into the previous
// chunk, so the final range may exceed chunkSize up to MaxChunkSize.
func PlanChunks(totalBytes, chunkSize int64) ([]ChunkPlan, error) {
	if chunkSize < MinChunkSize || chunkSize > MaxChunkSize {
		return nil, apierror.New(apierror.CodeValidationFailed, 0,
			"chunk size must be between 5 MB and 64 MB")
	}
	if totalBytes <= 0 {
		return nil, apierror.New(apierror.CodeValidationFailed, 0,
			"total bytes must be greater than zero")
	}

	count := totalBytes / chunkSize
	if totalBytes%chunkSize != 0 {
		count++
	}
	if count > MaxChunks {
		return nil, apierror.New(apierror.CodeValidationFailed, 0,
			"object would need more than 1000 chunks")
	}

	if remainder := totalBytes % chunkSize; remainder != 0 && remainder < MinChunkSize && count > 1 {
		mergedSize := remainder + chunkSize
		if mergedSize > MaxChunkSize {
			return nil, apierror.New(apierror.CodeValidationFailed, 0,
				"merged trailing chunk would exceed 64 MB")
		}
		merged := count - 1
		plans := make([]ChunkPlan, 0, merged)
		for i := int64(0); i < merged-1; i++ {
			plans = append(plans, ChunkPlan{
				Index: int(i),
				First: i * chunkSize,
				Last:  (i + 1) * chunkSize,
				Total: totalBytes,
			})
		}
		start := (merged - 1) * chunkSize
		return append(plans, ChunkPlan{
			Index: int(merged - 1),
			First: start,
			Last:  totalBytes,
			Total: totalBytes,
		}), nil
	}

	plans := make([]ChunkPlan, 0, count)
	for i := int64(0); i < count; i++ {
		last := (i + 1) * chunkSize
		if last > totalBytes {
			last = totalBytes
		}
		plans = append(plans, ChunkPlan{
			Index: int(i),
			First: i * chunkSize,
			Last:  last,
			Total: totalBytes,
		})
	}
	return plans, nil
}

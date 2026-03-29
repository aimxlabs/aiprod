package memory

import (
	"encoding/json"
	"fmt"
	"sync"

	covidx "github.com/viant/sqlite-vec/index/cover"
)

// VecIndex manages an in-memory cover tree index for semantic search over memories.
// The index is built from embeddings stored in the memories table and rebuilt
// incrementally as new memories are added.
type VecIndex struct {
	mu    sync.RWMutex
	index *covidx.Index
	ids   []string       // memory IDs in index order
	dim   int            // embedding dimension (0 if not yet known)
	built bool
}

func NewVecIndex() *VecIndex {
	return &VecIndex{
		index: covidx.New(
			covidx.WithDistance(covidx.DistanceFunctionCosine),
		),
	}
}

// Build constructs the index from a set of memory IDs and their embedding vectors.
func (v *VecIndex) Build(ids []string, vectors [][]float32) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if len(ids) == 0 {
		v.ids = nil
		v.dim = 0
		v.built = true
		return nil
	}

	v.dim = len(vectors[0])
	v.ids = ids

	idx := covidx.New(
		covidx.WithDistance(covidx.DistanceFunctionCosine),
	)
	if err := idx.Build(ids, vectors); err != nil {
		return fmt.Errorf("building vector index: %w", err)
	}
	v.index = idx
	v.built = true
	return nil
}

// Query finds the k most similar memories to the query vector.
// Returns memory IDs and similarity scores (0-1, higher is more similar).
func (v *VecIndex) Query(query []float32, k int) ([]string, []float64, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if !v.built || v.dim == 0 {
		return nil, nil, nil
	}
	if len(query) != v.dim {
		return nil, nil, fmt.Errorf("query dim %d != index dim %d", len(query), v.dim)
	}

	return v.index.Query(query, k)
}

// Count returns the number of vectors in the index.
func (v *VecIndex) Count() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.ids)
}

// IsBuilt returns whether the index has been built.
func (v *VecIndex) IsBuilt() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.built
}

// --- Helpers for loading embeddings from the database ---

// ParseEmbedding converts a JSON-encoded float64 array to float32 for the index.
func ParseEmbedding(embJSON string) ([]float32, error) {
	if embJSON == "" {
		return nil, nil
	}
	var f64 []float64
	if err := json.Unmarshal([]byte(embJSON), &f64); err != nil {
		return nil, err
	}
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32, nil
}

// Float64ToFloat32 converts a float64 slice to float32.
func Float64ToFloat32(v []float64) []float32 {
	f32 := make([]float32, len(v))
	for i, x := range v {
		f32[i] = float32(x)
	}
	return f32
}

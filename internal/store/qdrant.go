package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"github.com/vanducvt0305/zeus/internal/model"
)

// Named vectors in the collection: one dense (semantic) and one sparse
// (keyword). Hybrid search fuses results from both.
const (
	denseVector  = "dense"
	sparseVector = "sparse"
)

// payloadKey constants are the Qdrant payload fields we set on every point.
const (
	fieldMCPID      = "mcp_id"
	fieldKind       = "kind"
	fieldToolName   = "tool_name"
	fieldSource     = "source"
	fieldCategories = "categories"
	fieldMCPJSON    = "mcp_json"
)

// Qdrant implements Store on top of a Qdrant collection. Each MCP contributes
// one "server" point and one "tool" point per tool; searches over-fetch and
// then deduplicate to one Hit per MCP.
type Qdrant struct {
	client     *qdrant.Client
	collection string
}

// NewQdrant connects to a Qdrant instance over gRPC.
func NewQdrant(host string, port int, apiKey, collection string) (*Qdrant, error) {
	client, err := qdrant.NewClient(&qdrant.Config{
		Host:   host,
		Port:   port,
		APIKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to qdrant: %w", err)
	}
	return &Qdrant{client: client, collection: collection}, nil
}

func (q *Qdrant) EnsureCollection(ctx context.Context, dim int) error {
	exists, err := q.client.CollectionExists(ctx, q.collection)
	if err != nil {
		return fmt.Errorf("checking collection: %w", err)
	}
	if exists {
		return nil
	}
	return q.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: q.collection,
		VectorsConfig: qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
			denseVector: {Size: uint64(dim), Distance: qdrant.Distance_Cosine},
		}),
		SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
			sparseVector: {},
		}),
	})
}

func (q *Qdrant) Upsert(ctx context.Context, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	points := make([]*qdrant.PointStruct, 0, len(records))
	for _, r := range records {
		raw, err := json.Marshal(r.MCP)
		if err != nil {
			return fmt.Errorf("marshaling mcp %q: %w", r.MCP.ID, err)
		}
		allCats := r.MCP.AllCategories()
		cats := make([]any, len(allCats))
		for i, c := range allCats {
			cats[i] = c
		}
		payload := map[string]any{
			fieldMCPID:      r.MCP.ID,
			fieldKind:       string(r.Kind),
			fieldToolName:   r.ToolName,
			fieldSource:     r.MCP.Source,
			fieldCategories: cats,
			fieldMCPJSON:    string(raw),
		}
		vectors := map[string]*qdrant.Vector{
			denseVector: qdrant.NewVector(r.Vector...),
		}
		if !r.Sparse.Empty() {
			vectors[sparseVector] = qdrant.NewVectorSparse(r.Sparse.Indices, r.Sparse.Values)
		}
		points = append(points, &qdrant.PointStruct{
			Id:      qdrant.NewID(pointID(r.MCP.ID, r.Kind, r.discriminator())),
			Vectors: qdrant.NewVectorsMap(vectors),
			Payload: qdrant.NewValueMap(payload),
		})
	}
	_, err := q.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: q.collection,
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("upserting %d points: %w", len(points), err)
	}
	return nil
}

func (q *Qdrant) Search(ctx context.Context, sq SearchQuery) ([]Hit, error) {
	topK := sq.TopK
	if topK <= 0 {
		topK = 10
	}
	// Over-fetch: a single MCP can match via several tool/query points plus its
	// server point, so we ask for more and then collapse to distinct MCPs.
	limit := uint64(topK*4 + 10)
	filter := buildFilter(sq.Filter)

	req := &qdrant.QueryPoints{
		CollectionName: q.collection,
		Limit:          &limit,
		Filter:         filter,
		WithPayload:    qdrant.NewWithPayload(true),
	}

	if sq.Sparse.Empty() {
		// Dense-only retrieval.
		using := denseVector
		req.Query = qdrant.NewQueryNearest(qdrant.NewVectorInput(sq.Dense...))
		req.Using = &using
	} else {
		// Hybrid: run a dense and a sparse prefetch, then fuse with Reciprocal
		// Rank Fusion. RRF combines by rank, so the two scoring scales don't
		// need to be comparable.
		prefetchLimit := uint64(topK*8 + 20)
		denseUsing, sparseUsing := denseVector, sparseVector
		req.Prefetch = []*qdrant.PrefetchQuery{
			{
				Query:  qdrant.NewQueryNearest(qdrant.NewVectorInput(sq.Dense...)),
				Using:  &denseUsing,
				Limit:  &prefetchLimit,
				Filter: filter,
			},
			{
				Query:  qdrant.NewQueryNearest(qdrant.NewVectorInputSparse(sq.Sparse.Indices, sq.Sparse.Values)),
				Using:  &sparseUsing,
				Limit:  &prefetchLimit,
				Filter: filter,
			},
		}
		req.Query = qdrant.NewQueryFusion(qdrant.Fusion_RRF)
	}

	points, err := q.client.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("querying: %w", err)
	}

	best := make(map[string]Hit)
	for _, p := range points {
		hit, ok := hitFromPayload(p.Payload, p.Score)
		if !ok {
			continue
		}
		if cur, seen := best[hit.MCP.ID]; !seen || hit.Score > cur.Score {
			best[hit.MCP.ID] = hit
		}
	}

	hits := make([]Hit, 0, len(best))
	for _, h := range best {
		hits = append(hits, h)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

func (q *Qdrant) Get(ctx context.Context, id string) (*model.MCP, error) {
	limit := uint32(1)
	points, err := q.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: q.collection,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatch(fieldMCPID, id),
				qdrant.NewMatch(fieldKind, string(KindServer)),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("scrolling for %q: %w", id, err)
	}
	if len(points) == 0 {
		return nil, nil
	}
	m, err := mcpFromPayload(points[0].Payload)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (q *Qdrant) Categories(ctx context.Context) ([]string, error) {
	limit := uint32(10000)
	points, err := q.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: q.collection,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{qdrant.NewMatch(fieldKind, string(KindServer))},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("scrolling categories: %w", err)
	}
	set := make(map[string]struct{})
	for _, p := range points {
		m, err := mcpFromPayload(p.Payload)
		if err != nil {
			continue
		}
		for _, c := range m.Categories {
			set[c] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// buildFilter turns a Filter into Qdrant conditions: source is a hard
// constraint (must), categories are OR-ed (should, at least one matches).
func buildFilter(f Filter) *qdrant.Filter {
	if f.Source == "" && len(f.Categories) == 0 {
		return nil
	}
	out := &qdrant.Filter{}
	if f.Source != "" {
		out.Must = append(out.Must, qdrant.NewMatch(fieldSource, f.Source))
	}
	for _, c := range f.Categories {
		out.Should = append(out.Should, qdrant.NewMatch(fieldCategories, c))
	}
	return out
}

func hitFromPayload(payload map[string]*qdrant.Value, score float32) (Hit, bool) {
	m, err := mcpFromPayload(payload)
	if err != nil {
		return Hit{}, false
	}
	hit := Hit{MCP: m, Score: score, MatchKind: KindServer}
	if v, ok := payload[fieldKind]; ok {
		hit.MatchKind = Kind(v.GetStringValue())
	}
	if v, ok := payload[fieldToolName]; ok {
		hit.ToolName = v.GetStringValue()
	}
	return hit, true
}

func mcpFromPayload(payload map[string]*qdrant.Value) (model.MCP, error) {
	var m model.MCP
	v, ok := payload[fieldMCPJSON]
	if !ok {
		return m, fmt.Errorf("payload missing %s", fieldMCPJSON)
	}
	if err := json.Unmarshal([]byte(v.GetStringValue()), &m); err != nil {
		return m, fmt.Errorf("decoding stored mcp: %w", err)
	}
	return m, nil
}

// pointID derives a stable Qdrant point UUID from the logical key, so that
// re-indexing the same MCP overwrites its existing points instead of
// duplicating them.
func pointID(mcpID string, kind Kind, disc string) string {
	key := mcpID + "|" + string(kind) + "|" + disc
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String()
}

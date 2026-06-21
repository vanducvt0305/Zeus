package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

// payloadKey constants are the Qdrant payload fields we set on points. The full
// MCP JSON lives ONLY on the server point of each MCP; tool/query points carry
// just the lightweight fields, so the large blob isn't duplicated across every
// point (and isn't transferred for every search candidate).
const (
	fieldMCPID      = "mcp_id"
	fieldKind       = "kind"
	fieldToolName   = "tool_name"
	fieldSource     = "source"
	fieldCategories = "categories"
	fieldMCPJSON    = "mcp_json"
)

// indexedFields get a Qdrant payload index so filters and lookups stay fast as
// the collection grows.
var indexedFields = []string{fieldMCPID, fieldKind, fieldSource, fieldCategories}

// Qdrant implements Store on top of a Qdrant collection.
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

// Close releases the underlying gRPC connection.
func (q *Qdrant) Close() error { return q.client.Close() }

func (q *Qdrant) EnsureCollection(ctx context.Context, dim int) error {
	exists, err := q.client.CollectionExists(ctx, q.collection)
	if err != nil {
		return fmt.Errorf("checking collection: %w", err)
	}
	if !exists {
		if err := q.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: q.collection,
			VectorsConfig: qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
				denseVector: {Size: uint64(dim), Distance: qdrant.Distance_Cosine},
			}),
			SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
				sparseVector: {},
			}),
		}); err != nil {
			return fmt.Errorf("creating collection: %w", err)
		}
	} else if got, err := q.denseDim(ctx); err == nil && got != 0 && got != uint64(dim) {
		// A pre-existing collection with a different vector size means the
		// embedder changed; upserts would fail with a cryptic error.
		return fmt.Errorf("collection %q has dense dim %d but embedder produces %d; recreate it or use a new QDRANT_COLLECTION", q.collection, got, dim)
	}
	q.ensureIndexes(ctx)
	return nil
}

// denseDim returns the configured size of the dense vector, or 0 if unknown.
func (q *Qdrant) denseDim(ctx context.Context) (uint64, error) {
	info, err := q.client.GetCollectionInfo(ctx, q.collection)
	if err != nil {
		return 0, err
	}
	params := info.GetConfig().GetParams().GetVectorsConfig().GetParamsMap().GetMap()
	if vp, ok := params[denseVector]; ok {
		return vp.GetSize(), nil
	}
	return 0, nil
}

// ensureIndexes creates payload indexes idempotently (errors, e.g. "already
// exists", are best-effort and only logged).
func (q *Qdrant) ensureIndexes(ctx context.Context) {
	ft := qdrant.FieldType_FieldTypeKeyword
	for _, f := range indexedFields {
		_, err := q.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: q.collection,
			FieldName:      f,
			FieldType:      &ft,
		})
		if err != nil {
			log.Printf("qdrant: field index %q: %v (continuing)", f, err)
		}
	}
}

func (q *Qdrant) Upsert(ctx context.Context, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	points := make([]*qdrant.PointStruct, 0, len(records))
	for _, r := range records {
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
		}
		// Store the full record only once, on the server point.
		if r.Kind == KindServer {
			raw, err := json.Marshal(r.MCP)
			if err != nil {
				return fmt.Errorf("marshaling mcp %q: %w", r.MCP.ID, err)
			}
			payload[fieldMCPJSON] = string(raw)
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
	wait := true // block until points are durably written and searchable
	if _, err := q.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: q.collection,
		Points:         points,
		Wait:           &wait,
	}); err != nil {
		return fmt.Errorf("upserting %d points: %w", len(points), err)
	}
	return nil
}

// candidate is a lightweight first-stage match (no full payload).
type candidate struct {
	mcpID     string
	score     float32
	matchKind Kind
	toolName  string
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
		// Only the light fields are needed to rank/collapse candidates; the full
		// record is fetched once per winner below.
		WithPayload: qdrant.NewWithPayloadInclude(fieldMCPID, fieldKind, fieldToolName),
	}

	if sq.Sparse.Empty() {
		using := denseVector
		req.Query = qdrant.NewQueryNearest(qdrant.NewVectorInput(sq.Dense...))
		req.Using = &using
	} else {
		// Hybrid: dense + sparse prefetch fused with Reciprocal Rank Fusion.
		prefetchLimit := uint64(topK*8 + 20)
		denseUsing, sparseUsing := denseVector, sparseVector
		req.Prefetch = []*qdrant.PrefetchQuery{
			{Query: qdrant.NewQueryNearest(qdrant.NewVectorInput(sq.Dense...)), Using: &denseUsing, Limit: &prefetchLimit, Filter: filter},
			{Query: qdrant.NewQueryNearest(qdrant.NewVectorInputSparse(sq.Sparse.Indices, sq.Sparse.Values)), Using: &sparseUsing, Limit: &prefetchLimit, Filter: filter},
		}
		req.Query = qdrant.NewQueryFusion(qdrant.Fusion_RRF)
	}

	points, err := q.client.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("querying: %w", err)
	}

	// Collapse candidate points to one best entry per MCP, while counting how
	// many of each MCP's representations (server/tool/query points) surfaced —
	// matching on several points is a stronger signal than a single lucky match.
	best := make(map[string]candidate)
	counts := make(map[string]int)
	for _, p := range points {
		c := candidate{
			mcpID:     payloadString(p.Payload, fieldMCPID),
			score:     p.Score,
			matchKind: Kind(payloadString(p.Payload, fieldKind)),
			toolName:  payloadString(p.Payload, fieldToolName),
		}
		if c.mcpID == "" {
			continue
		}
		counts[c.mcpID]++
		if cur, seen := best[c.mcpID]; !seen || c.score > cur.score {
			best[c.mcpID] = c
		}
	}

	ranked := make([]candidate, 0, len(best))
	for _, c := range best {
		ranked = append(ranked, c)
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}
	if len(ranked) == 0 {
		return nil, nil
	}

	// Fetch the full record once per winning MCP (from its server point).
	ids := make([]string, len(ranked))
	for i, c := range ranked {
		ids[i] = c.mcpID
	}
	mcps, err := q.getServers(ctx, ids)
	if err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, len(ranked))
	for _, c := range ranked {
		m, ok := mcps[c.mcpID]
		if !ok {
			continue
		}
		hits = append(hits, Hit{MCP: m, Score: c.score, MatchKind: c.matchKind, ToolName: c.toolName, MatchCount: counts[c.mcpID]})
	}
	return hits, nil
}

// getServers batch-fetches the full MCP records for the given ids by their
// deterministic server-point IDs.
func (q *Qdrant) getServers(ctx context.Context, ids []string) (map[string]model.MCP, error) {
	pointIDs := make([]*qdrant.PointId, len(ids))
	for i, id := range ids {
		pointIDs[i] = qdrant.NewID(pointID(id, KindServer, ""))
	}
	points, err := q.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: q.collection,
		Ids:            pointIDs,
		WithPayload:    qdrant.NewWithPayloadInclude(fieldMCPJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("fetching server payloads: %w", err)
	}
	out := make(map[string]model.MCP, len(points))
	for _, p := range points {
		m, err := mcpFromPayload(p.Payload)
		if err != nil {
			continue
		}
		out[m.ID] = m
	}
	return out, nil
}

func (q *Qdrant) Get(ctx context.Context, id string) (*model.MCP, error) {
	mcps, err := q.getServers(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	if m, ok := mcps[id]; ok {
		return &m, nil
	}
	return nil, nil
}

func (q *Qdrant) Categories(ctx context.Context) ([]string, error) {
	limit := uint64(1000)
	exact := true
	hits, err := q.client.Facet(ctx, &qdrant.FacetCounts{
		CollectionName: q.collection,
		Key:            fieldCategories,
		Limit:          &limit,
		Exact:          &exact,
	})
	if err != nil {
		return nil, fmt.Errorf("faceting categories: %w", err)
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		if v := h.GetValue().GetStringValue(); v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

// DeleteByMCPs removes all points for the given MCP ids, in chunks.
func (q *Qdrant) DeleteByMCPs(ctx context.Context, ids []string) error {
	const chunk = 256
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		batch := ids[start:end]
		sel := qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{qdrant.NewMatchKeywords(fieldMCPID, batch...)},
		})
		wait := true // ensure the delete is applied before subsequent upserts
		if _, err := q.client.Delete(ctx, &qdrant.DeletePoints{
			CollectionName: q.collection,
			Points:         sel,
			Wait:           &wait,
		}); err != nil {
			return fmt.Errorf("deleting points for %d mcps: %w", len(batch), err)
		}
	}
	return nil
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

func payloadString(payload map[string]*qdrant.Value, key string) string {
	if v, ok := payload[key]; ok {
		return v.GetStringValue()
	}
	return ""
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

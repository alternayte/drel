package drel

import (
	"context"
	"fmt"

	"github.com/alternayte/drel/internal/ast"
	"github.com/alternayte/drel/internal/dialect"
)

// RelationType describes the kind of relationship between two models.
type RelationType int

const (
	// HasMany indicates the parent has many related entities (1:N).
	HasMany RelationType = iota
	// HasOne indicates the parent has one related entity (1:1).
	HasOne
	// BelongsTo indicates the entity references a parent via a foreign key.
	BelongsTo
	// ManyToMany indicates a many-to-many relationship via a pivot table.
	ManyToMany
)

// RelationInfo describes a relationship between two models.
type RelationInfo struct {
	Name        string
	Type        RelationType
	FKColumn    string
	JoinTable   string // pivot table name (many-to-many only)
	RefColumn   string // target FK column in pivot table (many-to-many only)
	RelatedMeta *ModelMetaBase
	FieldSetter func(parent any, related any)
}

// IncludeSpec wraps a RelationInfo for use with Include queries.
type IncludeSpec struct {
	rel           *RelationInfo
	unscoped      bool
	withoutFilter []string
	wheres        []ast.WhereClause
	orderBy       []ast.OrderByExpr
	limit         *int
	then          []IncludeSpec
}

// NewIncludeSpec creates an IncludeSpec from a RelationInfo.
func NewIncludeSpec(rel *RelationInfo) IncludeSpec {
	return IncludeSpec{rel: rel}
}

// Then returns a copy of the IncludeSpec that also eagerly loads the given
// relationships on the related entities, enabling nested includes such as
// Users.Posts.Then(Posts.Tags). The child specs describe relationships on the
// related model (here Post), and may themselves be nested with Then. Nested
// loading uses split queries on each level, so it never produces a cartesian
// product regardless of depth.
func (s IncludeSpec) Then(children ...IncludeSpec) IncludeSpec {
	c := s
	c.then = append(append([]IncludeSpec(nil), s.then...), children...)
	return c
}

// Unscoped returns a copy of the IncludeSpec that bypasses global filters
// (e.g. soft delete) when loading the related entities.
func (s IncludeSpec) Unscoped() IncludeSpec {
	c := s
	c.unscoped = true
	return c
}

// WithoutFilter returns a copy of the IncludeSpec that drops the named global
// filter when loading the related entities (the others still apply).
func (s IncludeSpec) WithoutFilter(name string) IncludeSpec {
	c := s
	c.withoutFilter = append(append([]string(nil), s.withoutFilter...), name)
	return c
}

// Where returns a copy of the IncludeSpec with an additional filter predicate
// applied to the related entities query.
func (s IncludeSpec) Where(pred Predicate) IncludeSpec {
	c := s
	c.wheres = append(append([]ast.WhereClause(nil), s.wheres...), pred.clause)
	return c
}

// OrderBy returns a copy of the IncludeSpec with the given ordering applied
// to the related entities query.
func (s IncludeSpec) OrderBy(exprs ...OrderExpr) IncludeSpec {
	c := s
	newOrder := make([]ast.OrderByExpr, len(s.orderBy))
	copy(newOrder, s.orderBy)
	for _, e := range exprs {
		newOrder = append(newOrder, e.ToAST())
	}
	c.orderBy = newOrder
	return c
}

// Limit returns a copy of the IncludeSpec that limits the number of related
// entities loaded per parent. For multi-parent loads this is enforced with a
// window function (ROW_NUMBER() partitioned by the foreign key); for a single
// parent it is a plain LIMIT.
func (s IncludeSpec) Limit(n int) IncludeSpec {
	c := s
	c.limit = &n
	return c
}

// IncludableQuery constructs queries that eagerly load related entities. The
// root query may be refined with Where/OrderBy/Limit/Skip/Take exactly like a
// plain QueryBuilder; the configured relationships are loaded via split queries
// after the root result set is materialized.
type IncludableQuery[T any] struct {
	repo     *Repository[T]
	builder  *QueryBuilder[T]
	includes []IncludeSpec
}

// Include begins a query that will eagerly load the given relationships.
func (r *Repository[T]) Include(rels ...IncludeSpec) *IncludableQuery[T] {
	return &IncludableQuery[T]{
		repo:     r,
		builder:  r.newBuilder(),
		includes: rels,
	}
}

func (q *IncludableQuery[T]) with(b *QueryBuilder[T]) *IncludableQuery[T] {
	return &IncludableQuery[T]{repo: q.repo, builder: b, includes: q.includes}
}

// Include adds more relationships to eagerly load.
func (q *IncludableQuery[T]) Include(rels ...IncludeSpec) *IncludableQuery[T] {
	c := q.with(q.builder)
	c.includes = append(append([]IncludeSpec(nil), q.includes...), rels...)
	return c
}

// Where adds a filter predicate to the root query.
func (q *IncludableQuery[T]) Where(pred Predicate) *IncludableQuery[T] {
	return q.with(q.builder.Where(pred))
}

// OrderBy sets the ordering for the root query.
func (q *IncludableQuery[T]) OrderBy(exprs ...OrderExpr) *IncludableQuery[T] {
	return q.with(q.builder.OrderBy(exprs...))
}

// Limit restricts the number of root records returned.
func (q *IncludableQuery[T]) Limit(n int) *IncludableQuery[T] {
	return q.with(q.builder.Limit(n))
}

// Take restricts the number of root records returned (alias for Limit).
func (q *IncludableQuery[T]) Take(n int) *IncludableQuery[T] {
	return q.with(q.builder.Limit(n))
}

// Skip sets the offset for the root query.
func (q *IncludableQuery[T]) Skip(n int) *IncludableQuery[T] {
	return q.with(q.builder.Skip(n))
}

// Unscoped removes all global filters from the root query.
func (q *IncludableQuery[T]) Unscoped() *IncludableQuery[T] {
	return q.with(q.builder.Unscoped())
}

// WithoutFilter removes the named global filter from the root query.
func (q *IncludableQuery[T]) WithoutFilter(name string) *IncludableQuery[T] {
	return q.with(q.builder.WithoutFilter(name))
}

// Primary forces both the root query and all included sub-queries to read from
// the primary connection instead of a read replica. Use it for read-your-writes
// consistency immediately after a write. No effect when no replicas configured.
func (q *IncludableQuery[T]) Primary() *IncludableQuery[T] {
	return q.with(q.builder.Primary())
}

func (q *IncludableQuery[T]) loadInto(ctx context.Context, entities []*T) error {
	if len(entities) == 0 {
		return nil
	}
	parents := make([]any, len(entities))
	for i, e := range entities {
		parents[i] = e
	}
	exec := &includeExecutor{
		reader:     q.repo.engine,
		parentMeta: ToMetaBase(&q.repo.meta),
		primary:    q.builder.primary,
		tracker:    q.builder.tracker,
	}
	return exec.loadRelations(ctx, parents, q.includes)
}

// Find looks up a single record by primary key and loads included relationships.
func (q *IncludableQuery[T]) Find(ctx context.Context, id any) (*T, error) {
	entity, err := q.builder.Where(pkPredicate(q.repo.meta.PKColumns, keyValuesOf(q.repo.meta.KeyValues, id))).First(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.loadInto(ctx, []*T{entity}); err != nil {
		return nil, err
	}
	return entity, nil
}

// First returns the first matching record (honoring Where/OrderBy) and loads
// included relationships, or ErrNotFound if none match.
func (q *IncludableQuery[T]) First(ctx context.Context) (*T, error) {
	entity, err := q.builder.First(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.loadInto(ctx, []*T{entity}); err != nil {
		return nil, err
	}
	return entity, nil
}

// All returns all matching records (honoring Where/OrderBy/Limit) and loads
// included relationships.
func (q *IncludableQuery[T]) All(ctx context.Context) ([]*T, error) {
	entities, err := q.builder.All(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.loadInto(ctx, entities); err != nil {
		return nil, err
	}
	return entities, nil
}

// TxIncludableQuery eagerly loads relationships within an explicit transaction.
// Root reads and all split sub-queries run on the transaction connection, and
// loaded children are tracked by the transaction's change tracker (unless the
// root builder is AsNoTracking) so their edits are flushed on commit.
type TxIncludableQuery[T any] struct {
	repo     *TxRepository[T]
	builder  *TxQueryBuilder[T]
	includes []IncludeSpec
}

// Where adds a filter predicate to the root query.
func (q *TxIncludableQuery[T]) Where(pred Predicate) *TxIncludableQuery[T] {
	return &TxIncludableQuery[T]{repo: q.repo, builder: q.builder.Where(pred), includes: q.includes}
}

// OrderBy sets the ordering for the root query.
func (q *TxIncludableQuery[T]) OrderBy(exprs ...OrderExpr) *TxIncludableQuery[T] {
	return &TxIncludableQuery[T]{repo: q.repo, builder: q.builder.OrderBy(exprs...), includes: q.includes}
}

// Limit restricts the number of root records returned.
func (q *TxIncludableQuery[T]) Limit(n int) *TxIncludableQuery[T] {
	return &TxIncludableQuery[T]{repo: q.repo, builder: q.builder.Limit(n), includes: q.includes}
}

// Include adds more relationships to eagerly load.
func (q *TxIncludableQuery[T]) Include(rels ...IncludeSpec) *TxIncludableQuery[T] {
	return &TxIncludableQuery[T]{
		repo:     q.repo,
		builder:  q.builder,
		includes: append(append([]IncludeSpec(nil), q.includes...), rels...),
	}
}

func (q *TxIncludableQuery[T]) loadInto(ctx context.Context, entities []*T) error {
	if len(entities) == 0 {
		return nil
	}
	parents := make([]any, len(entities))
	for i, e := range entities {
		parents[i] = e
	}
	var tracker *changeTracker
	if !q.builder.noTrack {
		tracker = q.builder.tx.tracker
	}
	exec := &includeExecutor{
		reader:     txIncludeReader{tx: q.builder.tx},
		parentMeta: ToMetaBase(&q.repo.meta),
		primary:    true, // a single tx connection: no replica routing
		tracker:    tracker,
	}
	return exec.loadRelations(ctx, parents, q.includes)
}

// Find looks up a single record by primary key and loads included relationships.
func (q *TxIncludableQuery[T]) Find(ctx context.Context, id any) (*T, error) {
	entity, err := q.builder.Where(pkPredicate(q.repo.meta.PKColumns, keyValuesOf(q.repo.meta.KeyValues, id))).First(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.loadInto(ctx, []*T{entity}); err != nil {
		return nil, err
	}
	return entity, nil
}

// First returns the first matching record and loads included relationships.
func (q *TxIncludableQuery[T]) First(ctx context.Context) (*T, error) {
	entity, err := q.builder.First(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.loadInto(ctx, []*T{entity}); err != nil {
		return nil, err
	}
	return entity, nil
}

// All returns all matching records and loads included relationships.
func (q *TxIncludableQuery[T]) All(ctx context.Context) ([]*T, error) {
	entities, err := q.builder.All(ctx)
	if err != nil {
		return nil, err
	}
	if err := q.loadInto(ctx, entities); err != nil {
		return nil, err
	}
	return entities, nil
}

// includeReader abstracts the connection a split include query runs on: the
// engine (replica/primary routing) or an active transaction connection.
type includeReader interface {
	queryRouted(ctx context.Context, primary bool, sql string, args ...any) (Rows, error)
	includeDialect() dialect.Dialect
}

// txIncludeReader runs include split queries on a transaction connection.
type txIncludeReader struct{ tx *Tx }

func (r txIncludeReader) queryRouted(ctx context.Context, _ bool, sql string, args ...any) (Rows, error) {
	return r.tx.queryInternal(ctx, sql, args...)
}

func (r txIncludeReader) includeDialect() dialect.Dialect { return r.tx.engine.dialect() }

// includeExecutor runs split queries to load related entities. When tracker is
// non-nil, each loaded child is snapshotted and tracked so mutations to it are
// flushed by SaveChanges (UnitOfWork read-your-writes semantics).
type includeExecutor struct {
	reader     includeReader
	parentMeta *ModelMetaBase
	primary    bool           // force primary reads (UoW read-your-writes / in-tx)
	tracker    *changeTracker // nil ⇒ children are not tracked (read-only)
}

func (ie *includeExecutor) loadRelations(ctx context.Context, parents []any, includes []IncludeSpec) error {
	for _, inc := range includes {
		if err := ie.loadRelation(ctx, parents, inc); err != nil {
			return fmt.Errorf("drel: include %s: %w", inc.rel.Name, err)
		}
	}
	return nil
}

func (ie *includeExecutor) loadRelation(ctx context.Context, parents []any, inc IncludeSpec) error {
	var related []any
	var err error
	switch inc.rel.Type {
	case HasMany, HasOne:
		related, err = ie.loadHasManyOrOne(ctx, parents, inc)
	case BelongsTo:
		related, err = ie.loadBelongsTo(ctx, parents, inc)
	case ManyToMany:
		related, err = ie.loadManyToMany(ctx, parents, inc)
	default:
		return fmt.Errorf("unknown relation type %d", inc.rel.Type)
	}
	if err != nil {
		return err
	}

	// Nested includes: treat the freshly loaded related entities as parents and
	// load the next level via split queries on the related model's metadata.
	if len(inc.then) > 0 && len(related) > 0 {
		childExec := &includeExecutor{
			reader:     ie.reader,
			parentMeta: inc.rel.RelatedMeta,
			primary:    ie.primary,
			tracker:    ie.tracker,
		}
		if err := childExec.loadRelations(ctx, related, inc.then); err != nil {
			return err
		}
	}
	return nil
}

// loadHasManyOrOne executes: SELECT * FROM related_table WHERE fk_column IN (pk1, pk2, ...)
// then groups results by the FK value and sets them on each parent.
func (ie *includeExecutor) loadHasManyOrOne(ctx context.Context, parents []any, inc IncludeSpec) ([]any, error) {
	rel := inc.rel
	// Collect parent PK values.
	pkValues := make([]any, len(parents))
	for i, p := range parents {
		pkValues[i] = ie.parentMeta.PKValue(p)
	}

	// Query related entities.
	related, err := ie.queryByColumn(ctx, rel.RelatedMeta, rel.FKColumn, pkValues, inc)
	if err != nil {
		return nil, err
	}

	// Find the FK column index in the related meta.
	fkIdx := findColumnIndex(rel.RelatedMeta.Columns, rel.FKColumn)
	if fkIdx < 0 {
		return nil, fmt.Errorf("FK column %q not found in %s columns", rel.FKColumn, rel.RelatedMeta.Table)
	}

	if rel.Type == HasMany {
		// Group related entities by FK value.
		grouped := make(map[any][]any)
		for _, r := range related {
			fkVal := rel.RelatedMeta.ColumnValue(r, fkIdx)
			grouped[fkVal] = append(grouped[fkVal], r)
		}
		// Set on each parent.
		for _, p := range parents {
			pk := ie.parentMeta.PKValue(p)
			items := grouped[pk]
			if items == nil {
				items = []any{}
			}
			rel.FieldSetter(p, items)
		}
	} else {
		// HasOne: index by FK value.
		byFK := make(map[any]any)
		for _, r := range related {
			fkVal := rel.RelatedMeta.ColumnValue(r, fkIdx)
			byFK[fkVal] = r
		}
		for _, p := range parents {
			pk := ie.parentMeta.PKValue(p)
			if r, ok := byFK[pk]; ok {
				rel.FieldSetter(p, r)
			}
		}
	}

	return related, nil
}

// loadBelongsTo executes: SELECT * FROM related_table WHERE pk IN (fk1, fk2, ...)
// Collects FK values from parents, then matches related entities by PK.
func (ie *includeExecutor) loadBelongsTo(ctx context.Context, parents []any, inc IncludeSpec) ([]any, error) {
	rel := inc.rel
	// Find the FK column index in the parent meta.
	fkIdx := findColumnIndex(ie.parentMeta.Columns, rel.FKColumn)
	if fkIdx < 0 {
		return nil, fmt.Errorf("FK column %q not found in %s columns", rel.FKColumn, ie.parentMeta.Table)
	}

	// Collect FK values from parents (deduplicating).
	seen := make(map[any]bool)
	var fkValues []any
	for _, p := range parents {
		fk := ie.parentMeta.ColumnValue(p, fkIdx)
		if fk != nil && !seen[fk] {
			seen[fk] = true
			fkValues = append(fkValues, fk)
		}
	}

	if len(fkValues) == 0 {
		return nil, nil
	}

	// Query related entities by their PK. [0] is safe here: a composite-key
	// model can never be a relationship target (codegen enforces this), so
	// RelatedMeta.PKColumns is always exactly one column.
	related, err := ie.queryByColumn(ctx, rel.RelatedMeta, rel.RelatedMeta.PKColumns[0], fkValues, inc)
	if err != nil {
		return nil, err
	}

	// Index related by PK.
	byPK := make(map[any]any)
	for _, r := range related {
		pk := rel.RelatedMeta.PKValue(r)
		byPK[pk] = r
	}

	// Set on each parent.
	for _, p := range parents {
		fk := ie.parentMeta.ColumnValue(p, fkIdx)
		if r, ok := byPK[fk]; ok {
			rel.FieldSetter(p, r)
		}
	}

	return related, nil
}

const includeBatchSize = 1000

// queryByColumn executes SELECT * FROM table WHERE column IN (values...)
// batching the IN list to stay within Postgres parameter limits.
// When inc.unscoped is false, meta.Filters are applied to the query.
// Any inc.wheres, inc.orderBy, and inc.limit are also applied.
func (ie *includeExecutor) queryByColumn(ctx context.Context, meta *ModelMetaBase, column string, values []any, inc IncludeSpec) ([]any, error) {
	if len(values) == 0 {
		return nil, nil
	}

	var allItems []any
	for i := 0; i < len(values); i += includeBatchSize {
		end := i + includeBatchSize
		if end > len(values) {
			end = len(values)
		}
		batch := values[i:end]

		if err := ctx.Err(); err != nil {
			return nil, err
		}

		inClause := ast.WhereClause{
			Comparison: &ast.ComparisonNode{
				Column: column,
				Op:     ast.OpIn,
				Values: batch,
			},
		}

		// Collect global filters, honoring Unscoped and WithoutFilter.
		var activeFilters []NamedFilter
		if !inc.unscoped {
			for _, f := range meta.Filters {
				if containsString(inc.withoutFilter, f.Name) {
					continue
				}
				activeFilters = append(activeFilters, f)
			}
		}

		var where *ast.WhereClause
		hasFilters := len(activeFilters) > 0
		hasIncWheres := len(inc.wheres) > 0
		if hasFilters || hasIncWheres {
			capacity := 1 + len(activeFilters) + len(inc.wheres)
			allWheres := make([]ast.WhereClause, 0, capacity)
			for _, f := range activeFilters {
				allWheres = append(allWheres, f.Clause)
			}
			allWheres = append(allWheres, inClause)
			allWheres = append(allWheres, inc.wheres...)
			combined := ast.WhereClause{
				LogicalOp: ast.LogicalAnd,
				Children:  allWheres,
			}
			where = &combined
		} else {
			where = &inClause
		}

		node := ast.SelectNode{
			Table:   meta.Table,
			Columns: meta.Columns,
			Where:   where,
			OrderBy: inc.orderBy,
			Limit:   inc.limit,
			Type:    ast.QuerySelect,
		}

		// Per-parent limit: when more than one parent value is in this batch and a
		// limit is set, apply it per parent via a window function instead of a
		// single batch-wide LIMIT (which would cap the total rows across parents).
		if inc.limit != nil && len(batch) > 1 {
			partitionOrder := inc.orderBy
			if len(partitionOrder) == 0 {
				partitionOrder = make([]ast.OrderByExpr, len(meta.PKColumns))
				for i, c := range meta.PKColumns {
					partitionOrder[i] = ast.OrderByExpr{Column: c, Direction: ast.Asc}
				}
			}
			node.OrderBy = nil
			node.Limit = nil
			node.PartitionLimit = &ast.PartitionLimit{
				PartitionBy: column,
				OrderBy:     partitionOrder,
				Limit:       *inc.limit,
			}
		}

		result := ie.reader.includeDialect().BuildSelect(node)
		rows, err := ie.reader.queryRouted(ctx, ie.primary, result.SQL, result.Args...)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			item, err := meta.ScanRow(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if ie.tracker != nil && meta.Snapshot != nil {
				ie.tracker.Track(item, meta.Snapshot(item), meta)
			}
			allItems = append(allItems, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return allItems, nil
}

// loadManyToMany loads related entities through a pivot table in 3 steps:
// 1. SELECT source_fk, target_fk FROM pivot WHERE source_fk IN (...)
// 2. SELECT * FROM target WHERE pk IN (collected target PKs)
// 3. Build source→[]target mapping and assign via FieldSetter
func (ie *includeExecutor) loadManyToMany(ctx context.Context, parents []any, inc IncludeSpec) ([]any, error) {
	rel := inc.rel
	pkValues := make([]any, len(parents))
	for i, p := range parents {
		pkValues[i] = ie.parentMeta.PKValue(p)
	}

	type pivotRow struct {
		sourceFK, targetFK any
	}
	var pivotRows []pivotRow

	for i := 0; i < len(pkValues); i += includeBatchSize {
		end := i + includeBatchSize
		if end > len(pkValues) {
			end = len(pkValues)
		}
		batch := pkValues[i:end]

		if err := ctx.Err(); err != nil {
			return nil, err
		}

		node := ast.SelectNode{
			Table:   rel.JoinTable,
			Columns: []string{rel.FKColumn, rel.RefColumn},
			Where: &ast.WhereClause{
				Comparison: &ast.ComparisonNode{
					Column: rel.FKColumn,
					Op:     ast.OpIn,
					Values: batch,
				},
			},
			Type: ast.QuerySelect,
		}

		result := ie.reader.includeDialect().BuildSelect(node)
		rows, err := ie.reader.queryRouted(ctx, ie.primary, result.SQL, result.Args...)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var src, tgt any
			if err := rows.Scan(&src, &tgt); err != nil {
				rows.Close()
				return nil, err
			}
			pivotRows = append(pivotRows, pivotRow{
				sourceFK: normalizeKeyWith(ie.parentMeta, src),
				targetFK: normalizeKeyWith(rel.RelatedMeta, tgt),
			})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	if len(pivotRows) == 0 {
		for _, p := range parents {
			rel.FieldSetter(p, []any{})
		}
		return nil, nil
	}

	seen := make(map[any]bool)
	var targetPKs []any
	for _, pr := range pivotRows {
		if !seen[pr.targetFK] {
			seen[pr.targetFK] = true
			targetPKs = append(targetPKs, pr.targetFK)
		}
	}

	// [0] is safe here: a composite-key model can never be a relationship
	// target (codegen enforces this), so RelatedMeta.PKColumns is always
	// exactly one column.
	targets, err := ie.queryByColumn(ctx, rel.RelatedMeta, rel.RelatedMeta.PKColumns[0], targetPKs, inc)
	if err != nil {
		return nil, err
	}

	// Map each target PK to the source PK(s) that reference it, from the pivot.
	sourcesByTarget := make(map[any][]any)
	for _, pr := range pivotRows {
		sourcesByTarget[pr.targetFK] = append(sourcesByTarget[pr.targetFK], pr.sourceFK)
	}

	// Build per-parent groups by walking targets in requested (query) order so
	// the assigned collection preserves inc.orderBy.
	grouped := make(map[any][]any)
	for _, t := range targets {
		tpk := rel.RelatedMeta.PKValue(t)
		for _, srcPK := range sourcesByTarget[tpk] {
			grouped[srcPK] = append(grouped[srcPK], t)
		}
	}

	for _, p := range parents {
		pk := ie.parentMeta.PKValue(p)
		items := grouped[pk]
		if items == nil {
			items = []any{}
		}
		rel.FieldSetter(p, items)
	}

	return targets, nil
}

// normalizeKeyWith applies meta.NormalizeKey when present, else int normalization.
func normalizeKeyWith(meta *ModelMetaBase, v any) any {
	if meta != nil && meta.NormalizeKey != nil {
		return meta.NormalizeKey(v)
	}
	return normalizeInt(v)
}

// normalizeInt converts int64/int32 to int so that map lookups match
// PKValue functions that return Go int. pgx scans INTEGER columns as int64
// when the target is any, causing type mismatches in map keys.
func normalizeInt(v any) any {
	switch n := v.(type) {
	case int64:
		return int(n)
	case int32:
		return int(n)
	default:
		return v
	}
}

// containsString reports whether s contains v.
func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// findColumnIndex returns the index of the named column, or -1 if not found.
func findColumnIndex(columns []string, name string) int {
	for i, c := range columns {
		if c == name {
			return i
		}
	}
	return -1
}

package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmitDBFile_WithRelations(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "User", PkgPath: "app/models/users", PkgName: "users",
			PKType: "int", TableName: "users",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name"},
				{Name: "posts", GoType: "[]*models.Post", Relation: &RelationFieldInfo{
					Type: "has_many", FK: "user_id", TargetModel: "Post",
				}},
			},
		},
		{
			Name: "Post", PkgPath: "app/models/posts", PkgName: "posts",
			PKType: "int", TableName: "posts",
			Fields: []FieldInfo{
				{Name: "title", GoType: "string", ColumnName: "title"},
				{Name: "author", GoType: "*models.User", Relation: &RelationFieldInfo{
					Type: "belongs_to", FK: "user_id", TargetModel: "User",
				}},
			},
		},
	}

	out := EmitDBFile(models, "db")

	assert.Contains(t, out, "var UserPostsRel = drel.RelationInfo{")
	assert.Contains(t, out, "drel.ToMetaBase(&posts.PostMeta)")
	assert.Contains(t, out, "p := parent.(*users.User)")
	assert.Contains(t, out, "item.(*posts.Post)")

	assert.Contains(t, out, "var PostAuthorRel = drel.RelationInfo{")
	assert.Contains(t, out, "drel.ToMetaBase(&users.UserMeta)")

	assert.Contains(t, out, "var UserIncludePosts = drel.NewIncludeSpec(&UserPostsRel)")
	assert.Contains(t, out, "var PostIncludeAuthor = drel.NewIncludeSpec(&PostAuthorRel)")
}

func TestEmitDBFile_TypedRepositories(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "User", PkgPath: "app/models/users", PkgName: "users",
			PKType: "int", TableName: "users",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name"},
			},
		},
	}

	out := EmitDBFile(models, "db")

	assert.Contains(t, out, "Users *users.UserRepository")
	assert.Contains(t, out, "&users.UserRepository{Repository: drel.NewRepository(engine, users.UserMeta)}")
}

func TestEmitDBFile_ManyToManyRelation(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "Author", PkgPath: "app/models/authors", PkgName: "authors",
			PKType: "int", TableName: "authors",
			Fields: []FieldInfo{
				{Name: "tags", GoType: "[]*models.Tag", Relation: &RelationFieldInfo{
					Type: "many_to_many", FK: "author_id", JoinTable: "author_tags",
					RefColumn: "tag_id", TargetModel: "Tag",
				}},
			},
		},
		{
			Name: "Tag", PkgPath: "app/models/tags", PkgName: "tags",
			PKType: "int", TableName: "tags",
			Fields: []FieldInfo{
				{Name: "name", GoType: "string", ColumnName: "name"},
			},
		},
	}

	out := EmitDBFile(models, "db")

	assert.Contains(t, out, "var AuthorTagsRel = drel.RelationInfo{")
	assert.Contains(t, out, "drel.ManyToMany")
	assert.Contains(t, out, `JoinTable:   "author_tags"`)
	assert.Contains(t, out, `RefColumn:   "tag_id"`)
	assert.Contains(t, out, "drel.ToMetaBase(&tags.TagMeta)")
	assert.Contains(t, out, "var AuthorIncludeTags = drel.NewIncludeSpec(&AuthorTagsRel)")
}

// TestEmitDBFile_ContextTransaction covers the context transaction surface: the
// TxRepos struct, the Tx accessor and the WithTx forwarder. It also proves that
// the deleted UnitOfWork surface is gone.
func TestEmitDBFile_ContextTransaction(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "User", PkgPath: "app/features/users", PkgName: "users",
			PKType: "int", TableName: "users",
			Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}},
		},
		{
			Name: "Post", PkgPath: "app/features/posts", PkgName: "posts",
			PKType: "int", TableName: "posts",
			Fields: []FieldInfo{{Name: "title", GoType: "string", ColumnName: "title"}},
		},
	}

	out := EmitDBFile(models, "db")

	assert.Contains(t, out, `"context"`)

	assert.Contains(t, out, "type TxRepos struct {")
	assert.Contains(t, out, "Users *users.TxUserRepository")
	assert.Contains(t, out, "Posts *posts.TxPostRepository")

	assert.Contains(t, out, "func (db *DB) Tx(ctx context.Context) TxRepos {")
	assert.Contains(t, out, "tx := drel.MustFromContext(ctx)")
	assert.Contains(t, out, "&users.TxUserRepository{TxRepository: drel.NewTxRepository(tx, users.UserMeta)}")
	assert.Contains(t, out, "&posts.TxPostRepository{TxRepository: drel.NewTxRepository(tx, posts.PostMeta)}")

	assert.Contains(t, out, "func (db *DB) WithTx(ctx context.Context, fn func(ctx context.Context) error, opts ...drel.TxOption) error {")
	assert.Contains(t, out, "return db.Engine.WithTx(ctx, fn, opts...)")

	assert.NotContains(t, out, "UnitOfWork")
	assert.NotContains(t, out, "UoW")
}

// TestEmitDBFile_ModuleSets covers the slice-scoped repository sets. A module
// named "posts" that holds the model Post would give DB a field Posts and a
// method Posts(), which Go rejects, so the sets live in a Modules holder.
func TestEmitDBFile_ModuleSets(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "Post", PkgPath: "app/features/posts", PkgName: "posts", Module: "posts",
			PKType: "int", TableName: "posts",
			Fields: []FieldInfo{{Name: "title", GoType: "string", ColumnName: "title"}},
		},
		{
			Name: "Comment", PkgPath: "app/features/posts", PkgName: "posts", Module: "posts",
			PKType: "int", TableName: "comments",
			Fields: []FieldInfo{{Name: "body", GoType: "string", ColumnName: "body"}},
		},
		{
			Name: "User", PkgPath: "app/features/users", PkgName: "users", Module: "users",
			PKType: "int", TableName: "users",
			Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}},
		},
	}

	out := EmitDBFile(models, "db")

	// The flat fields stay, so nothing that exists today breaks.
	assert.Contains(t, out, "Posts *posts.PostRepository")
	assert.Contains(t, out, "Users *users.UserRepository")

	// The untracked sets.
	assert.Contains(t, out, "type PostsRepos struct {")
	assert.Contains(t, out, "type UsersRepos struct {")
	assert.Contains(t, out, "type Modules struct {")
	assert.Contains(t, out, "Posts PostsRepos")
	assert.Contains(t, out, "Users UsersRepos")

	// The tracked sets hang off TxRepos, so a slice reaches only its own
	// repositories while the transaction stays shared.
	assert.Contains(t, out, "type PostsTxRepos struct {")
	assert.Contains(t, out, "type TxModules struct {")
	assert.Contains(t, out, "Modules TxModules")

	// A module holds every model of its packages.
	assert.Contains(t, out, "Comments *posts.CommentRepository")
	assert.Contains(t, out, "Comments *posts.TxCommentRepository")

	// The holders must be filled, not only declared. A struct field that is
	// declared and never assigned leaves a nil repository, which panics at the
	// first call.
	assert.Contains(t, out, "Modules: Modules{",
		"Open must fill the untracked holder")
	assert.Contains(t, out, "Modules: TxModules{",
		"Tx must fill the tracked holder")
	assert.Contains(t, out, "Posts: PostsRepos{")
	assert.Contains(t, out, "Posts: PostsTxRepos{")
	assert.Contains(t, out, "Users: UsersRepos{")
	assert.Contains(t, out, "Users: UsersTxRepos{")

	// Every model of a module reaches both holders.
	assert.Equal(t, 2, strings.Count(out, "drel.NewRepository(engine, posts.CommentMeta)"),
		"the comment repository appears in the flat field and in the module set")
	assert.Equal(t, 2, strings.Count(out, "drel.NewTxRepository(tx, posts.CommentMeta)"),
		"the tracked comment repository appears in the flat field and in the module set")
}

// TestEmitDBFile_NoModulesEmitsNoHolder proves a project that lists packages
// instead of modules gets no extra code.
func TestEmitDBFile_NoModulesEmitsNoHolder(t *testing.T) {
	models := []ModelInfo{
		{
			Name: "User", PkgPath: "app/models", PkgName: "models",
			PKType: "int", TableName: "users",
			Fields: []FieldInfo{{Name: "name", GoType: "string", ColumnName: "name"}},
		},
	}

	out := EmitDBFile(models, "db")

	assert.NotContains(t, out, "type Modules struct")
	assert.NotContains(t, out, "type TxModules struct")
	assert.Contains(t, out, "Users *models.UserRepository")
}

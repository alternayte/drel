package codegen

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssignModules_TagsByDirectory(t *testing.T) {
	root := filepath.FromSlash("/app")
	models := []ModelInfo{
		{Name: "Post", Dir: filepath.Join(root, "internal/features/posts")},
		{Name: "User", Dir: filepath.Join(root, "internal/features/users")},
		{Name: "Orphan", Dir: filepath.Join(root, "internal/other")},
	}
	mods := []ModuleConfig{
		{Name: "posts", Packages: []string{"./internal/features/posts"}},
		{Name: "users", Packages: []string{"./internal/features/users"}},
	}

	assignModules(models, mods, root)

	assert.Equal(t, "posts", models[0].Module)
	assert.Equal(t, "users", models[1].Module)
	assert.Equal(t, "", models[2].Module, "a model outside every module stays untagged")
}

func TestAssignModules_WildcardAndNesting(t *testing.T) {
	root := filepath.FromSlash("/app")
	models := []ModelInfo{
		{Name: "Post", Dir: filepath.Join(root, "features/posts/domain")},
		{Name: "Draft", Dir: filepath.Join(root, "features/posts/drafts")},
	}
	mods := []ModuleConfig{
		{Name: "posts", Packages: []string{"./features/posts/..."}},
		{Name: "drafts", Packages: []string{"./features/posts/drafts"}},
	}

	assignModules(models, mods, root)

	assert.Equal(t, "posts", models[0].Module, "a wildcard pattern covers the subtree")
	assert.Equal(t, "drafts", models[1].Module, "the longest matching prefix wins")
}

package posts

import "github.com/alternayte/drel"

// Post is owned by the posts slice. Deleting features/posts removes the model,
// the generated code and the migrations of this feature together.
type Post struct {
	drel.Model[int]
	Title    string `db:"title"`
	AuthorID int    `db:"author_id"`
}

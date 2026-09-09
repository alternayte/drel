package users

import "github.com/alternayte/drel"

// User is owned by the users slice. Its migrations live next to it, in
// features/users/migrations.
type User struct {
	drel.Model[int]
	Name  string `db:"name"`
	Email string `db:"email"`
}

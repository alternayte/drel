// Example: api
//
// Demonstrates dynamic query composition from HTTP request parameters —
// the drel equivalent of EF Core's IQueryable pattern. The immutable
// builder pattern makes conditional WHERE chaining natural: each
// .Where() call returns a new QueryBuilder without mutating the original.
//
// The GET /products endpoint builds a query incrementally from query
// params (minPrice, maxPrice, category, inStock, sort, limit), showing
// how server-side filtering composes cleanly with drel's type-safe
// column references.
//
// Usage:
//
//	cd examples/api
//	go run ../../cmd/drel generate   # generates products/product_drel.go + db/drel_gen.go
//	export DATABASE_URL="postgres://localhost:5432/drelexample?sslmode=disable"
//	go run .
package main

//go:generate go run ../../cmd/drel generate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/alternayte/drel"
	"github.com/alternayte/drel/examples/api/db"
	"github.com/alternayte/drel/examples/api/products"
)

// productResponse is the JSON representation of a Product.
// drel.Model[int] fields are unexported, so we map to an explicit struct.
type productResponse struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Price     int       `json:"price"`
	Category  string    `json:"category"`
	InStock   bool      `json:"in_stock"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toResponse(p *products.Product) productResponse {
	return productResponse{
		ID:        p.ID(),
		Name:      p.Name,
		Price:     p.Price,
		Category:  p.Category,
		InStock:   p.InStock,
		CreatedAt: p.CreatedAt(),
		UpdatedAt: p.UpdatedAt(),
	}
}

func toResponseList(ps []*products.Product) []productResponse {
	out := make([]productResponse, len(ps))
	for i, p := range ps {
		out[i] = toResponse(p)
	}
	return out
}

// createProductRequest is the JSON body for POST /products.
type createProductRequest struct {
	Name     string `json:"name"`
	Price    int    `json:"price"`
	Category string `json:"category"`
	InStock  bool   `json:"in_stock"`
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost:5432/drelexample?sslmode=disable"
	}

	ctx := context.Background()

	database, err := db.Open(dsn, drel.WithContext(ctx))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer database.Close()

	setup(ctx, database)
	seed(ctx, database)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", handleListProducts(database))
	mux.HandleFunc("POST /products", handleCreateProduct(database))
	mux.HandleFunc("DELETE /products/{id}", handleDeleteProduct(database))

	fmt.Println("drel API example — dynamic query composition")
	fmt.Println()
	fmt.Println("Example requests:")
	fmt.Println("  curl localhost:8080/products")
	fmt.Println("  curl localhost:8080/products?category=electronics")
	fmt.Println("  curl localhost:8080/products?minPrice=2000&maxPrice=10000")
	fmt.Println("  curl localhost:8080/products?inStock=true&sort=price")
	fmt.Println("  curl localhost:8080/products?category=books&sort=price_desc&limit=3")
	fmt.Println("  curl -X POST localhost:8080/products -d '{\"name\":\"Tablet\",\"price\":4999,\"category\":\"electronics\",\"in_stock\":true}'")
	fmt.Println("  curl -X DELETE localhost:8080/products/1")
	fmt.Println()
	fmt.Println("Listening on :8080")

	log.Fatal(http.ListenAndServe(":8080", mux))
}

// handleListProducts builds a query dynamically from URL query parameters.
// This is the key pattern: start with a QueryBuilder, then conditionally
// chain .Where() calls based on which params are present.
func handleListProducts(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		params := r.URL.Query()

		// Collect predicates from query params.
		var preds []drel.Predicate

		if v := params.Get("minPrice"); v != "" {
			price, err := strconv.Atoi(v)
			if err != nil {
				http.Error(w, "invalid minPrice", http.StatusBadRequest)
				return
			}
			preds = append(preds, products.Products.Price.GTE(price))
		}

		if v := params.Get("maxPrice"); v != "" {
			price, err := strconv.Atoi(v)
			if err != nil {
				http.Error(w, "invalid maxPrice", http.StatusBadRequest)
				return
			}
			preds = append(preds, products.Products.Price.LTE(price))
		}

		if v := params.Get("category"); v != "" {
			preds = append(preds, products.Products.Category.Eq(v))
		}

		if v := params.Get("inStock"); v == "true" {
			preds = append(preds, products.Products.InStock.IsTrue())
		}

		// Start with a default sort to get a QueryBuilder.
		qb := database.Products.OrderBy(products.Products.Name.Asc())

		// Apply collected predicates.
		for _, p := range preds {
			qb = qb.Where(p)
		}

		// Override sort if requested.
		switch params.Get("sort") {
		case "price":
			qb = qb.OrderBy(products.Products.Price.Asc())
		case "price_desc":
			qb = qb.OrderBy(products.Products.Price.Desc())
		case "name":
			qb = qb.OrderBy(products.Products.Name.Asc())
		}

		// Apply limit.
		if v := params.Get("limit"); v != "" {
			limit, err := strconv.Atoi(v)
			if err != nil {
				http.Error(w, "invalid limit", http.StatusBadRequest)
				return
			}
			qb = qb.Limit(limit)
		}

		items, err := qb.All(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toResponseList(items))
	}
}

// handleCreateProduct inserts a new product inside a transaction.
func handleCreateProduct(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var req createProductRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		product := &products.Product{
			Name:     req.Name,
			Price:    req.Price,
			Category: req.Category,
			InStock:  req.InStock,
		}

		err := database.WithTx(ctx, func(ctx context.Context) error {
			database.Tx(ctx).Products.Add(product)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(toResponse(product))
	}
}

// handleDeleteProduct deletes a product by ID inside a transaction.
func handleDeleteProduct(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}

		err = database.WithTx(ctx, func(ctx context.Context) error {
			repo := database.Tx(ctx).Products
			product, err := repo.Find(ctx, id)
			if err != nil {
				return err
			}
			return repo.Remove(product)
		})
		if err != nil {
			if err == drel.ErrNotFound {
				http.Error(w, "product not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func setup(ctx context.Context, database *db.DB) {
	database.Exec(ctx, `DROP TABLE IF EXISTS products`)
	database.Exec(ctx, `
		CREATE TABLE products (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			price INTEGER NOT NULL,
			category TEXT NOT NULL,
			in_stock BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
}

func seed(ctx context.Context, database *db.DB) {
	err := database.WithTx(ctx, func(ctx context.Context) error {
		repo := database.Tx(ctx).Products

		repo.Add(&products.Product{Name: "Laptop", Price: 99999, Category: "electronics", InStock: true})
		repo.Add(&products.Product{Name: "Mechanical Keyboard", Price: 14999, Category: "electronics", InStock: true})
		repo.Add(&products.Product{Name: "USB-C Hub", Price: 4999, Category: "electronics", InStock: false})
		repo.Add(&products.Product{Name: "Go Programming Language", Price: 3499, Category: "books", InStock: true})
		repo.Add(&products.Product{Name: "Designing Data-Intensive Apps", Price: 4299, Category: "books", InStock: true})
		repo.Add(&products.Product{Name: "Clean Code", Price: 2999, Category: "books", InStock: false})
		repo.Add(&products.Product{Name: "Wool Sweater", Price: 7999, Category: "clothing", InStock: true})
		repo.Add(&products.Product{Name: "Running Shoes", Price: 12999, Category: "clothing", InStock: true})
		repo.Add(&products.Product{Name: "Rain Jacket", Price: 15999, Category: "clothing", InStock: false})
		repo.Add(&products.Product{Name: "Wireless Mouse", Price: 2999, Category: "electronics", InStock: true})

		return nil
	})
	if err != nil {
		log.Fatalf("seed: %v", err)
	}
}
